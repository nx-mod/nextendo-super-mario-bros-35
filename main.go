// Command super-mario-bros-35 runs the Super Mario Bros. 35 online servers (auth +
// secure + Eagle relay) on the Nextendo NEX stack.
//
// Unlike arms/mk8/splatoon-2/ssbu, SMB35 is NOT peer-to-peer: NEX matchmaking only
// forms the 35-player lobby, then hands each joiner off (via a notification event)
// to a per-gathering Eagle relay session that carries the actual gameplay traffic.
// See nextendo-nex's eagle.go for the relay itself.
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"time"

	nex "github.com/NextendoNetwork/nextendo-nex"
)

const (
	accessKey  = "0a69c592" // kinnay/NintendoClients wiki's Game Server List entry for SMB35
	serverID   = "21F12900" // same entry — also the sni-router hostname suffix ("g21f12900")
	nexVersion = 40600      // kinnay/SMB35's source/main.py: s.configure("0a69c592", 40600, 0)
	securePID  = 2

	sessionKeyLen = 32
)

var securePassword = envOr("NEXTENDO_SECURE_PASSWORD", "securepasswordplz1")

var (
	nextendoHost = envOr("NEXTENDO_HOST", "127.0.0.1")
	authPort     = envOrInt("AUTH_PORT", 443)
	securePort   = envOrInt("SECURE_PORT", 60008)
	eaglePort    = envOrInt("EAGLE_PORT", 60009)
	certFile     = envOr("CERT_FILE", "cert.pem")
	keyFile      = envOr("KEY_FILE", "key.pem")

	// See arms/main.go for why these two must be byte-identical to
	// nextendo-account's own secret/derivation.
	nextendoSecret = loadNextendoSecret()
	requireAccount = os.Getenv("NEXTENDO_REQUIRE_ACCOUNT") == "1"
)

func main() {
	settings := nex.NewSwitchSettings(accessKey, nexVersion)

	// --- Eagle relay (wss, its own port) ---
	eagleCfg := nex.SMB35EagleConfig(loadEagleSigningKey())
	eagleMgr := nex.NewEagleMgr(eagleCfg)

	// --- Auth server (:443 via sni-router) ---
	secureURL := nex.NewStationURL("prudps")
	secureURL.Set("address", nextendoHost)
	secureURL.SetInt("port", securePort)
	secureURL.SetInt("CID", 1)
	secureURL.SetInt("PID", securePID)
	secureURL.SetInt("sid", 1)
	secureURL.SetInt("stream", 10)
	secureURL.SetInt("type", 2)

	authEndpoint := nex.NewEndpoint(settings)
	authCfg := &nex.AuthConfig{
		Settings:         settings,
		SecurePID:        securePID,
		SecurePassword:   securePassword,
		SecureStationURL: secureURL,
		ServerName:       "Super Mario Bros. 35",
		SessionKeyLength: sessionKeyLen,
		ResolveUser:      resolveUser,
	}
	authEndpoint.Register(nex.ProtocolTicketGranting, authCfg.Handler())
	authEndpoint.OnRMC = logRMC("Auth")
	authServer := nex.NewServer(authEndpoint)

	// --- Secure server ---
	secureSettings := *settings
	secureSettings.PrudpMinorVersion = 0 // see arms/main.go: the retail secure server answers minor version 0, not the library default
	secureEndpoint := nex.NewEndpoint(&secureSettings)
	secureEndpoint.SetSecureAccount(securePassword, securePID)

	mm := nex.NewMatchmaking()
	mm.OnParticipantJoined = func(gid uint32, pid uint64) {
		eagleMgr.Start(gid) // idempotent: no-op if this gathering's session already exists
		target := secureEndpoint.FindConnectionByPID(pid)
		if target == nil {
			return
		}
		wsURL := fmt.Sprintf("wss://%s:%d/%d", nextendoHost, eaglePort, gid)
		// Map (not StrParam): kinnay/SMB35's real reference server (source/main.py,
		// MatchMaker.join()) sends this exact shape -- event.type=200000, param1=gid,
		// event.map={"url":...,"token":...} -- confirmed 2026-08-18 via the actual
		// upstream source, contradicting this package's earlier (wrong) note that the
		// Map variant was "rejected outright". StrParam never got a single real client
		// to even open an Eagle socket in two separate real-player tests tonight.
		event := &nex.NotificationEvent{
			PIDSource: securePID,
			Type:      200000,
			Param1:    uint64(gid),
			Map:       nex.EagleHandoffMap(eagleCfg, wsURL, gid, pid),
		}
		nex.SendNotification(target, event)
		fmt.Printf("[SMB35 Eagle] pid=%d gid=%d -> handed off to %s\n", pid, gid, wsURL)
	}

	secureEndpoint.Register(nex.ProtocolSecureConnection, nex.SecureConnectionHandlerWithConfig(nex.LegacyPiaConfig()))
	secureEndpoint.Register(nex.ProtocolMatchmakeExtension, mm.ExtensionHandler())
	secureEndpoint.Register(nex.ProtocolMatchMaking, mm.MatchMakingHandler())
	secureEndpoint.Register(nex.ProtocolMatchMakingExt, mm.MatchMakingExtHandler())
	// No NATTraversal: SMB35 gameplay goes through the Eagle relay, not P2P.
	secureEndpoint.Register(protocolMatchmakeReferee, matchmakeRefereeHandler(mm, func(pid uint64, e *nex.NotificationEvent) {
		if target := secureEndpoint.FindConnectionByPID(pid); target != nil {
			nex.SendNotification(target, e)
		}
	}))
	secureEndpoint.Register(protocolMessageDelivery, messageDeliveryHandler(secureEndpoint, mm))
	secureEndpoint.Register(protocolRanking2, ranking2Handler(newRanking2Store()))
	secureEndpoint.Register(nex.ProtocolUtility, smb35UtilityHandler(newAssociatedIDStore()))

	logSecure := logRMC("Secure")
	secureEndpoint.OnRMC = func(c *nex.Connection, req *nex.RMCMessage) {
		logSecure(c, req)
		noteRMC(c, req)
		notePresenceSeen(c.PID)
	}
	secureEndpoint.OnConnect = func(c *nex.Connection) {
		fmt.Printf("[SMB35 Secure] connected pid=%d id=%d addr=%s\n", c.PID, c.ID, c.RemoteAddr)
	}
	secureEndpoint.OnDisconnect = func(c *nex.Connection) {
		mm.RemovePlayer(c.PID)
	}
	secureServer := nex.NewServer(secureEndpoint)

	secureEndpoint.StartReaper()
	go startDashboard(secureEndpoint, mm)
	startPresenceReporter()

	proxyProto := os.Getenv("NEXTENDO_PROXY_PROTOCOL") == "1"
	go func() {
		fmt.Printf("[SMB35 Auth] listening WSS :%d (proxyProto=%v, secure URL -> %s)\n", authPort, proxyProto, secureURL.String())
		var err error
		if proxyProto {
			err = authServer.ListenSecureProxy(authPort, certFile, keyFile)
		} else {
			err = authServer.ListenSecure(authPort, certFile, keyFile)
		}
		if err != nil {
			fmt.Printf("[SMB35 Auth] stopped: %v\n", err)
		}
	}()

	go func() {
		fmt.Printf("[SMB35 Eagle] listening WSS :%d\n", eaglePort)
		if err := eagleMgr.ListenSecure(eaglePort, certFile, keyFile); err != nil {
			fmt.Printf("[SMB35 Eagle] stopped: %v\n", err)
		}
	}()

	fmt.Printf("[SMB35 Secure] listening WSS :%d\n", securePort)
	if err := secureServer.ListenSecure(securePort, certFile, keyFile); err != nil {
		fmt.Printf("[SMB35 Secure] stopped: %v\n", err)
	}
}

// resolveUser: identical shape to arms/main.go — a signed nx2 Nextendo token
// resolves to its persistent PID; a bare numeric username to either that PID
// directly (test/emulator client) or, for a real console's NSA id range, the
// Nextendo account it's linked to; anything else is anonymous unless
// NEXTENDO_REQUIRE_ACCOUNT=1.
func resolveUser(username string, _ []byte) (uint64, []byte, bool) {
	sk := sha256.Sum256([]byte("nextendo-src:" + username))
	sourceKey := sk[:]

	if pid, ok := nextendoPIDFromToken(username); ok {
		if allow, reason := nextendoOnlineCheck(pid, "ryujinx"); !allow {
			fmt.Printf("[Auth] pid=%d online REFUSED (%s)\n", pid, reason)
			return 0, nil, false
		}
		return pid, sourceKey, true
	}

	if n, err := strconv.ParseUint(username, 10, 64); err == nil && n >= 1800000000 {
		if requireSignedToken() {
			fmt.Printf("[Auth] pid=%d REFUSED: bare-PID identity disabled (signed nx2 token required)\n", n)
			return 0, nil, false
		}
		pid, kind := n, "ryujinx"
		if n >= 1810000000 {
			kind = "switch"
			rp, st := resolveNSAtoPID(n)
			switch st {
			case nsaOK:
				pid = rp
			case nsaUnknown:
				fmt.Printf("[Auth] NSA %d REFUSED (no Nextendo account)\n", n)
				return 0, nil, false
			case nsaUnreachable:
				fmt.Printf("[Auth] NSA %d REFUSED (account server unreachable)\n", n)
				return 0, nil, false
			}
		}
		if allow, reason := nextendoOnlineCheck(pid, kind); !allow {
			fmt.Printf("[Auth] pid=%d online REFUSED (%s)\n", pid, reason)
			return 0, nil, false
		}
		return pid, sourceKey, true
	}

	if requireAccount {
		fmt.Printf("[Auth] anonymous login REFUSED (Nextendo account required): %q\n", username)
		return 0, nil, false
	}
	return anonymousPID(username), sourceKey, true
}

func nextendoPIDFromToken(s string) (uint64, bool) {
	if len(nextendoSecret) == 0 || !strings.HasPrefix(s, "nx2.") {
		return 0, false
	}
	parts := strings.Split(s[len("nx2."):], ".")
	if len(parts) != 2 {
		return 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, nextendoSecret)
	mac.Write([]byte("nex:" + string(raw)))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return 0, false
	}
	f := strings.SplitN(string(raw), ".", 3)
	if len(f) != 3 {
		return 0, false
	}
	pid, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil {
		return 0, false
	}
	if exp, err := strconv.ParseInt(f[2], 10, 64); err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	return pid, true
}

func loadNextendoSecret() []byte {
	if v := os.Getenv("NEXTENDO_SECRET"); v != "" {
		return []byte(v)
	}
	path := envOr("NEXTENDO_SECRET_FILE", "nextendo_secret.key")
	if b, err := os.ReadFile(path); err == nil {
		if dec, derr := hex.DecodeString(strings.TrimSpace(string(b))); derr == nil && len(dec) >= 16 {
			return dec
		}
	}
	return nil
}

// loadEagleSigningKey reads (or generates, for a bare dev run) the key that
// signs Eagle handoff tokens. Doesn't need to match anything else in the
// fleet — see eagle.go's package comment for why the token's own byte shape
// is a private contract between this process's signer and its own verifier.
func loadEagleSigningKey() []byte {
	if v := os.Getenv("NEXTENDO_EAGLE_SIGNING_KEY"); v != "" {
		if dec, err := hex.DecodeString(strings.TrimSpace(v)); err == nil && len(dec) >= 16 {
			return dec
		}
		return []byte(v)
	}
	path := envOr("NEXTENDO_EAGLE_SIGNING_KEY_FILE", "eagle_signing.key")
	if b, err := os.ReadFile(path); err == nil {
		if dec, derr := hex.DecodeString(strings.TrimSpace(string(b))); derr == nil && len(dec) >= 16 {
			return dec
		}
	}
	key := nex.RandomEagleSigningKey()
	fmt.Println("[SMB35] NEXTENDO_EAGLE_SIGNING_KEY not set — generated a random one for this run only " +
		"(fine for a solo dev run; set it explicitly so restarts don't invalidate in-flight handoff tokens)")
	return key
}

func anonymousPID(username string) uint64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(username))
	return 1800000000 + uint64(h.Sum32()%100000000)
}

func logRMC(tag string) func(*nex.Connection, *nex.RMCMessage) {
	return func(c *nex.Connection, req *nex.RMCMessage) {
		fmt.Printf("[SMB35 %s] pid=%d proto=%#x method=%d call=%d\n", tag, c.PID, req.Protocol, req.Method, req.CallID)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func requireSignedToken() bool {
	v := os.Getenv("NEXTENDO_REQUIRE_SIGNED_TOKEN")
	return v == "1" || v == "true"
}
