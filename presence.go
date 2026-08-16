// presence.go: every PID sending PRUDP packets is playing SMB35 right now.
// Report active PIDs to nextendo-account every 30s so it keeps them ONLINE via
// its TTL and serves them back to the Switch's own friend list — same
// mechanism as arms/presence.go (this file's original).
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const (
	presenceInterval = 30 * time.Second
	presenceTTL      = 60 * time.Second
	presenceStatus   = 2 // "playing"
	smb35AppID       = "0100277011F1A000"
)

var (
	presMu   sync.Mutex
	presSeen = map[uint64]time.Time{}
)

func notePresenceSeen(pid uint64) {
	if pid == 0 {
		return
	}
	presMu.Lock()
	presSeen[pid] = time.Now()
	presMu.Unlock()
}

func activePIDs() []uint64 {
	now := time.Now()
	active := []uint64{}
	presMu.Lock()
	for pid, t := range presSeen {
		if now.Sub(t) < presenceTTL {
			active = append(active, pid)
		} else {
			delete(presSeen, pid)
		}
	}
	presMu.Unlock()
	return active
}

func startPresenceReporter() {
	base := envOr("NEXTENDO_ACCOUNT_URL", "http://nextendo-account:8080")
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		for {
			time.Sleep(presenceInterval)
			active := activePIDs()
			if len(active) == 0 {
				continue
			}
			body, err := json.Marshal(map[string]any{"appId": smb35AppID, "status": presenceStatus, "pids": active})
			if err != nil {
				continue
			}
			req, err := http.NewRequest("POST", base+"/internal/presence-batch", bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			if resp, err := client.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}()
}
