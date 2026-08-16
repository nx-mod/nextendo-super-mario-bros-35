package main

// SMB35-specific protocols nextendo-nex's core doesn't implement generically:
// Ranking2 (0x7A), MatchmakeReferee (0x78), and MessageDelivery (0x1B), plus a
// GetIntegerSettings override on the core Utility protocol (0x6E). Wire formats
// (protocol/method ids, struct field order/types) are taken from
// kinnay/NintendoClients' generated protocol sources — ranking2_eagle.py,
// matchmaking_eagle.py (the MatchmakeReferee section), messaging.py, utility.py
// (all MIT) — behavior from kinnay/SMB35's source/main.py (AGPL-3.0, read only,
// nothing copied verbatim). Every store here is in-memory, matching every other
// Nextendo game server (no Postgres).

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

const (
	protocolRanking2         uint16 = 0x7A
	protocolMatchmakeReferee uint16 = 0x78
	protocolMessageDelivery  uint16 = 0x1B

	methodR2GetCommonData          uint32 = 2
	methodR2PutCommonData          uint32 = 3
	methodR2GetRanking             uint32 = 5
	methodR2GetCategorySetting     uint32 = 7
	methodR2GetEstimateMyScoreRank uint32 = 11

	methodMMRStartRound              uint32 = 1
	methodMMRGetStartRoundParam      uint32 = 2
	methodMMREndRound                uint32 = 3
	methodMMREndRoundWithPartialRept uint32 = 4

	methodMsgDeliverMessage uint32 = 1

	methodUtilAssociateNexUniqueID       uint32 = 3
	methodUtilGetAssociatedNexUniqueID   uint32 = 5

	resultRanking2InvalidArgument             uint32 = 0x00710002
	resultMatchmakeRefereeInvalidArgument      uint32 = 0x006F0002
	resultMatchmakeRefereeNotParticipatedGath  uint32 = 0x006F0004
	resultMatchmakeRefereeRoundNotFound        uint32 = 0x006F0007
)

// ---------------------------------------------------------------------------
// Ranking2 (0x7A) — common per-player data blob + always-empty rankings.
// SMB35 doesn't have a real leaderboard here (get_ranking/estimate always come
// back empty in the reference too); only the common-data store is real.
// ---------------------------------------------------------------------------

type ranking2CommonData struct {
	Username   string
	Mii        []byte
	BinaryData []byte
}

func (d *ranking2CommonData) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) { o.String(d.Username); o.QBuffer(d.Mii); o.QBuffer(d.BinaryData) },
		Load: func(i *nex.StreamIn) { d.Username = i.String(); d.Mii = i.QBuffer(); d.BinaryData = i.QBuffer() },
	}}
}

type ranking2GetParam struct {
	UniqueID        uint64
	PID             uint64
	Category        uint32
	Offset          uint32
	Count           uint32
	SortFlags       uint32
	OptionFlags     uint32
	Mode            uint8
	SeasonsToGoBack uint8
}

func (p *ranking2GetParam) Levels() []nex.Level {
	return []nex.Level{{
		Load: func(i *nex.StreamIn) {
			p.UniqueID = i.U64()
			p.PID = i.PID()
			p.Category = i.U32()
			p.Offset = i.U32()
			p.Count = i.U32()
			p.SortFlags = i.U32()
			p.OptionFlags = i.U32()
			p.Mode = i.U8()
			p.SeasonsToGoBack = i.U8()
		},
	}}
}

type ranking2Info struct {
	LowestRank uint32
	NumEntries uint32
	Season     int32
}

func (r *ranking2Info) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) {
			o.U32(0) // data: List<Ranking2RankData>, always empty here
			o.U32(r.LowestRank)
			o.U32(r.NumEntries)
			o.S32(r.Season)
		},
	}}
}

type ranking2CategorySetting struct {
	MinScore, MaxScore, LowestRank      uint32
	ResetMonth                          uint16
	ResetDay, ResetHour, ResetMode      uint8
	MaxSeasonsToGoBack                  uint8
	ScoreOrder                          bool
}

func (c *ranking2CategorySetting) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) {
			o.U32(c.MinScore)
			o.U32(c.MaxScore)
			o.U32(c.LowestRank)
			o.U16(c.ResetMonth)
			o.U8(c.ResetDay)
			o.U8(c.ResetHour)
			o.U8(c.ResetMode)
			o.U8(c.MaxSeasonsToGoBack)
			o.Bool(c.ScoreOrder)
		},
	}}
}

type ranking2EstimateMyScoreRankInput struct {
	Category        uint32
	SeasonsToGoBack uint8
}

func (in *ranking2EstimateMyScoreRankInput) Levels() []nex.Level {
	return []nex.Level{{
		Load: func(i *nex.StreamIn) { in.Category = i.U32(); in.SeasonsToGoBack = i.U8() },
	}}
}

type ranking2EstimateScoreRankOutput struct {
	Rank, Length, Score, Category uint32
	Season                        int32
	SamplingRate                  uint8
}

func (o2 *ranking2EstimateScoreRankOutput) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) {
			o.U32(o2.Rank)
			o.U32(o2.Length)
			o.U32(o2.Score)
			o.U32(o2.Category)
			o.S32(o2.Season)
			o.U8(o2.SamplingRate)
		},
	}}
}

type ranking2Store struct {
	mu   sync.Mutex
	data map[uint64]map[uint64]ranking2CommonData // pid -> unique_id -> data
}

func newRanking2Store() *ranking2Store {
	return &ranking2Store{data: map[uint64]map[uint64]ranking2CommonData{}}
}

func (s *ranking2Store) get(pid, uniqueID uint64) (ranking2CommonData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.data[pid]
	if !ok {
		return ranking2CommonData{}, false
	}
	v, ok := d[uniqueID]
	return v, ok
}

func (s *ranking2Store) put(pid, uniqueID uint64, data ranking2CommonData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[pid] == nil {
		s.data[pid] = map[uint64]ranking2CommonData{}
	}
	s.data[pid][uniqueID] = data
}

// ranking2Handler answers the 5 Ranking2 methods SMB35 actually calls
// (kinnay/SMB35's main.py doesn't implement the other 5 either — put_score,
// delete_common_data, get_ranking_by_principal_id, get_ranking_chart(s) all
// fall through to Core::NotImplemented there too).
func ranking2Handler(store *ranking2Store) nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		switch req.Method {
		case methodR2GetCommonData:
			in := nex.NewStreamIn(req.Body, s)
			_ = in.U32() // option_flags, unused
			pid := in.PID()
			uniqueID := in.U64()
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolRanking2, req.CallID, resultRanking2InvalidArgument)
			}
			data, ok := store.get(pid, uniqueID)
			if !ok {
				return nex.NewRMCError(s, protocolRanking2, req.CallID, resultRanking2InvalidArgument)
			}
			out := nex.NewStreamOut(s)
			out.Add(&data)
			return nex.NewRMCSuccess(s, protocolRanking2, req.Method, req.CallID, out.Bytes())

		case methodR2PutCommonData:
			in := nex.NewStreamIn(req.Body, s)
			var data ranking2CommonData
			in.Extract(&data)
			uniqueID := in.U64()
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolRanking2, req.CallID, resultRanking2InvalidArgument)
			}
			store.put(conn.PID, uniqueID, data)
			return nex.NewRMCSuccess(s, protocolRanking2, req.Method, req.CallID, nil)

		case methodR2GetRanking:
			in := nex.NewStreamIn(req.Body, s)
			var param ranking2GetParam
			in.Extract(&param)
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolRanking2, req.CallID, resultRanking2InvalidArgument)
			}
			out := nex.NewStreamOut(s)
			out.Add(&ranking2Info{LowestRank: 10000, NumEntries: 0, Season: 0})
			return nex.NewRMCSuccess(s, protocolRanking2, req.Method, req.CallID, out.Bytes())

		case methodR2GetCategorySetting:
			in := nex.NewStreamIn(req.Body, s)
			_ = in.U32() // category, unused: same fixed setting for every category
			out := nex.NewStreamOut(s)
			out.Add(&ranking2CategorySetting{
				MinScore: 0, MaxScore: 999999999, LowestRank: 10000,
				ResetMonth: 4095, ResetDay: 0, ResetHour: 0, ResetMode: 2,
				MaxSeasonsToGoBack: 3, ScoreOrder: true,
			})
			return nex.NewRMCSuccess(s, protocolRanking2, req.Method, req.CallID, out.Bytes())

		case methodR2GetEstimateMyScoreRank:
			in := nex.NewStreamIn(req.Body, s)
			var input ranking2EstimateMyScoreRankInput
			in.Extract(&input)
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolRanking2, req.CallID, resultRanking2InvalidArgument)
			}
			out := nex.NewStreamOut(s)
			out.Add(&ranking2EstimateScoreRankOutput{Category: input.Category})
			return nex.NewRMCSuccess(s, protocolRanking2, req.Method, req.CallID, out.Bytes())

		default:
			fmt.Printf("[SMB35 Ranking2] unhandled method %d pid=%d\n", req.Method, conn.PID)
			return nex.NewRMCError(s, protocolRanking2, req.CallID, nex.ResultCoreNotImplemented)
		}
	}
}

// ---------------------------------------------------------------------------
// MatchmakeReferee (0x78) — round bookkeeping for a battle-royale gathering.
// Real enough to track rounds and validate membership; SMB35's own game logic
// (who's eliminated, in what order) lives entirely in the Eagle RPC payloads
// this server never inspects, same as the reference.
// ---------------------------------------------------------------------------

type mmRefereeStartRoundParam struct {
	PersonalDataCategory uint32
	GID                  uint32
	PIDs                 []uint64
	ReportSummaryMode    uint8
	EventID              uint32
}

func (p *mmRefereeStartRoundParam) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) {
			o.U32(p.PersonalDataCategory)
			o.U32(p.GID)
			nex.WriteList(o, p.PIDs, func(o *nex.StreamOut, v uint64) { o.PID(v) })
			o.U8(p.ReportSummaryMode)
			o.U32(p.EventID)
		},
		Load: func(i *nex.StreamIn) {
			p.PersonalDataCategory = i.U32()
			p.GID = i.U32()
			p.PIDs = nex.ReadList(i, func(i *nex.StreamIn) uint64 { return i.PID() })
			p.ReportSummaryMode = i.U8()
			p.EventID = i.U32()
		},
	}}
}

type mmRefereeEndRoundParam struct {
	RoundID uint64
	// Results (per-player win/loss/rating) are read but not otherwise used —
	// same as the reference, which only validates the round exists.
}

func (p *mmRefereeEndRoundParam) Levels() []nex.Level {
	return []nex.Level{{
		Load: func(i *nex.StreamIn) {
			p.RoundID = i.U64()
			// results: List<MatchmakeRefereePersonalRoundResult> — skip without
			// decoding each entry's fields individually; ReadList still needs a
			// per-element reader so it can advance the stream correctly.
			nex.ReadList(i, func(i *nex.StreamIn) struct{} {
				_ = i.PID()
				_ = i.U32()
				_ = i.U32()
				_ = i.S32()
				_ = i.QBuffer()
				_ = i.U8()
				_ = i.U32()
				return struct{}{}
			})
		},
	}}
}

type matchmakeRefereeStore struct {
	mu      sync.Mutex
	nextID  uint64
	rounds  map[uint64]mmRefereeStartRoundParam
}

func newMatchmakeRefereeStore() *matchmakeRefereeStore {
	return &matchmakeRefereeStore{rounds: map[uint64]mmRefereeStartRoundParam{}}
}

// gatheringHasAll reports whether every pid in want is currently a participant
// of gid, per the matchmaking store's live snapshot.
func gatheringHasAll(mm *nex.Matchmaking, gid uint32, want []uint64) bool {
	for _, g := range mm.Snapshot() {
		if g.ID != gid {
			continue
		}
		for _, pid := range want {
			found := false
			for _, p := range g.Participants {
				if p == pid {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}

func matchmakeRefereeHandler(mm *nex.Matchmaking, notify func(pid uint64, event *nex.NotificationEvent)) nex.RMCHandler {
	store := newMatchmakeRefereeStore()
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		switch req.Method {
		case methodMMRStartRound:
			in := nex.NewStreamIn(req.Body, s)
			var param mmRefereeStartRoundParam
			in.Extract(&param)
			if in.Err() != nil || len(param.PIDs) == 0 {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeInvalidArgument)
			}
			if !gatheringHasAll(mm, param.GID, param.PIDs) {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeNotParticipatedGath)
			}
			store.mu.Lock()
			store.nextID++
			roundID := store.nextID
			store.rounds[roundID] = param
			store.mu.Unlock()

			for _, pid := range param.PIDs {
				notify(pid, &nex.NotificationEvent{PIDSource: conn.PID, Type: 116000, Param1: roundID})
			}
			out := nex.NewStreamOut(s)
			out.U64(roundID)
			return nex.NewRMCSuccess(s, protocolMatchmakeReferee, req.Method, req.CallID, out.Bytes())

		case methodMMRGetStartRoundParam:
			in := nex.NewStreamIn(req.Body, s)
			roundID := in.U64()
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeInvalidArgument)
			}
			store.mu.Lock()
			param, ok := store.rounds[roundID]
			store.mu.Unlock()
			if !ok {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeRoundNotFound)
			}
			out := nex.NewStreamOut(s)
			out.Add(&param)
			return nex.NewRMCSuccess(s, protocolMatchmakeReferee, req.Method, req.CallID, out.Bytes())

		case methodMMREndRound, methodMMREndRoundWithPartialRept:
			in := nex.NewStreamIn(req.Body, s)
			var param mmRefereeEndRoundParam
			in.Extract(&param)
			if in.Err() != nil {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeInvalidArgument)
			}
			store.mu.Lock()
			_, ok := store.rounds[param.RoundID]
			store.mu.Unlock()
			if !ok {
				return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, resultMatchmakeRefereeRoundNotFound)
			}
			return nex.NewRMCSuccess(s, protocolMatchmakeReferee, req.Method, req.CallID, nil)

		default:
			fmt.Printf("[SMB35 MatchmakeReferee] unhandled method %d pid=%d\n", req.Method, conn.PID)
			return nex.NewRMCError(s, protocolMatchmakeReferee, req.CallID, nex.ResultCoreNotImplemented)
		}
	}
}

// ---------------------------------------------------------------------------
// MessageDelivery (0x1B) — fire-and-forget lobby chat relay. NORESPONSE in the
// real protocol (the reference's own MessageDeliveryClient never awaits a
// response), so this handler always returns nil (no RMC response sent).
// ---------------------------------------------------------------------------

type msgRecipient struct {
	Type uint32
	PID  uint64
	GID  uint32
}

func (r *msgRecipient) Levels() []nex.Level {
	return []nex.Level{{
		Save: func(o *nex.StreamOut) { o.U32(r.Type); o.PID(r.PID); o.U32(r.GID) },
		Load: func(i *nex.StreamIn) { r.Type = i.U32(); r.PID = i.PID(); r.GID = i.U32() },
	}}
}

const (
	msgRecipientPrincipal uint32 = 1
	msgRecipientGathering uint32 = 2
)

// userMessage represents either a TextMessage or a BinaryMessage (the two
// DataHolder-registered subclasses SMB35 exchanges) — Go has no inheritance, so
// the base UserMessage fields and the subclass's one extra field are modeled as
// two Structure Levels, matching how the class hierarchy is actually framed on
// the wire (see notification.go's Map field / matchmaking_acnh.go for the same
// "extra Level = subclass" idea already used elsewhere in this codebase).
type userMessage struct {
	ID, ParentID         uint32
	Sender               uint64
	ReceptionTime        uint64
	LifeTime, Flags      uint32
	Subject, SenderName  string
	Recipient            msgRecipient

	IsBinary  bool // set before Load/Save to pick which of the two fields below applies
	BodyText  string
	BodyBytes []byte
}

func (m *userMessage) Levels() []nex.Level {
	return []nex.Level{
		{ // UserMessage
			Save: func(o *nex.StreamOut) {
				o.U32(m.ID)
				o.U32(m.ParentID)
				o.PID(m.Sender)
				o.DateTime(m.ReceptionTime)
				o.U32(m.LifeTime)
				o.U32(m.Flags)
				o.String(m.Subject)
				o.String(m.SenderName)
				o.Add(&m.Recipient)
			},
			Load: func(i *nex.StreamIn) {
				m.ID = i.U32()
				m.ParentID = i.U32()
				m.Sender = i.PID()
				m.ReceptionTime = i.DateTime()
				m.LifeTime = i.U32()
				m.Flags = i.U32()
				m.Subject = i.String()
				m.SenderName = i.String()
				i.Extract(&m.Recipient)
			},
		},
		{ // TextMessage.body (string) or BinaryMessage.body (qbuffer)
			Save: func(o *nex.StreamOut) {
				if m.IsBinary {
					o.QBuffer(m.BodyBytes)
				} else {
					o.String(m.BodyText)
				}
			},
			Load: func(i *nex.StreamIn) {
				if m.IsBinary {
					m.BodyBytes = i.QBuffer()
				} else {
					m.BodyText = i.String()
				}
			},
		},
	}
}

// readAnyDataRaw reads a DataHolder's (name, inner structure bytes) without
// needing a core StreamIn.AnyData() — the wire shape (String name, redundant
// U32 size, length-prefixed Buffer body) only needs the 3 primitives StreamIn
// already exposes; see StreamOut.AnyData in types.go for the writer side this
// mirrors.
func readAnyDataRaw(i *nex.StreamIn) (name string, body []byte) {
	name = i.String()
	_ = i.U32() // declared total size, redundant with Buffer's own length prefix
	body = i.Buffer()
	return
}

func messageDeliveryHandler(ep *nex.Endpoint, mm *nex.Matchmaking) nex.RMCHandler {
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		if req.Method != methodMsgDeliverMessage {
			fmt.Printf("[SMB35 MessageDelivery] unhandled method %d pid=%d\n", req.Method, conn.PID)
			return nil // NORESPONSE protocol: never answer, even for an unknown method
		}

		in := nex.NewStreamIn(req.Body, s)
		name, body := readAnyDataRaw(in)
		if in.Err() != nil {
			return nil
		}
		msg := &userMessage{IsBinary: name == "BinaryMessage"}
		bin := nex.NewStreamIn(body, s)
		bin.Extract(msg)
		if bin.Err() != nil {
			return nil
		}

		msg.Sender = conn.PID
		msg.SenderName = strconv.FormatUint(conn.PID, 10)
		now := time.Now()
		msg.ReceptionTime = nex.MakeDateTime(now.Year(), int(now.Month()), now.Day(), now.Hour(), now.Minute(), now.Second()).Value()

		deliver := func(pid uint64) {
			target := ep.FindConnectionByPID(pid)
			if target == nil {
				return
			}
			out := nex.NewStreamOut(target.Settings)
			out.AnyData(nex.DataHolder{Name: name, Data: msg})
			target.SendRMC(nex.NewRMCRequest(target.Settings, protocolMessageDelivery, methodMsgDeliverMessage, 0, out.Bytes()))
		}

		switch msg.Recipient.Type {
		case msgRecipientPrincipal:
			deliver(msg.Recipient.PID)
		case msgRecipientGathering:
			for _, g := range mm.Snapshot() {
				if g.ID != msg.Recipient.GID {
					continue
				}
				for _, pid := range g.Participants {
					deliver(pid)
				}
				break
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Utility (0x6E) override — core's generic nex.UtilityHandler answers
// GetIntegerSettings with an empty map, but SMB35's client reads real
// battle/round tuning values out of index 0 (see config.go); index 10 and
// everything else falls back to the core handler.
// ---------------------------------------------------------------------------

type associatedIDStore struct {
	mu   sync.Mutex
	byPID map[uint64]nex.UniqueIDInfo
}

func newAssociatedIDStore() *associatedIDStore {
	return &associatedIDStore{byPID: map[uint64]nex.UniqueIDInfo{}}
}

func smb35UtilityHandler(store *associatedIDStore) nex.RMCHandler {
	fallback := nex.UtilityHandler()
	return func(conn *nex.Connection, req *nex.RMCMessage) *nex.RMCMessage {
		s := conn.Settings
		switch req.Method {
		case nex.MethodGetIntegerSettings:
			in := nex.NewStreamIn(req.Body, s)
			index := in.U32()
			if in.Err() != nil {
				return nex.NewRMCError(s, nex.ProtocolUtility, req.CallID, nex.ResultCoreInvalidArgument)
			}
			var table map[uint16]int32
			switch index {
			case 0:
				table = integerSettings1
			case 10:
				table = integerSettings2
			default:
				return nex.NewRMCError(s, nex.ProtocolUtility, req.CallID, nex.ResultCoreInvalidArgument)
			}
			out := nex.NewStreamOut(s)
			nex.WriteMap(out, table, func(o *nex.StreamOut, k uint16) { o.U16(k) }, func(o *nex.StreamOut, v int32) { o.S32(v) })
			return nex.NewRMCSuccess(s, nex.ProtocolUtility, req.Method, req.CallID, out.Bytes())

		case methodUtilAssociateNexUniqueID:
			in := nex.NewStreamIn(req.Body, s)
			var info nex.UniqueIDInfo
			in.Extract(&info)
			if in.Err() != nil {
				return nex.NewRMCError(s, nex.ProtocolUtility, req.CallID, nex.ResultCoreInvalidArgument)
			}
			store.mu.Lock()
			store.byPID[conn.PID] = info
			store.mu.Unlock()
			return nex.NewRMCSuccess(s, nex.ProtocolUtility, req.Method, req.CallID, nil)

		case methodUtilGetAssociatedNexUniqueID:
			store.mu.Lock()
			info, ok := store.byPID[conn.PID]
			store.mu.Unlock()
			if !ok {
				info = nex.UniqueIDInfo{} // matches the reference's "return an empty one" default
			}
			out := nex.NewStreamOut(s)
			out.Add(&info)
			return nex.NewRMCSuccess(s, nex.ProtocolUtility, req.Method, req.CallID, out.Bytes())

		default:
			return fallback(conn, req)
		}
	}
}
