package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
)

// The outbound webhook: MCP is pull, and an operator agent only learns a run
// settled when someone asks it to look. The engine already knows the moment —
// its bus carries every human_needed, run_end and autopilot stop — so it
// announces them to one configured URL and the agent's platform wakes it.
//
// Best-effort by construction: a dead receiver must never block or slow a
// run. Five-second timeout, one retry, and failures are dropped silently —
// the record on disk is the source of truth, the webhook is a doorbell.
func (s *Service) startNotifier() {
	url := s.cfg.Notify.WebhookURL
	if url == "" {
		return
	}
	secret := s.cfg.Notify.Secret
	interesting := map[string]bool{
		"human_needed":    true,
		"run_end":         true,
		"autopilot":       true,
		"run_paused":      true,
		"question_asked":  true,
		"distress":        true,
		"failure_streak":  true,
		"repetition_loop": true,
		"budget_pause":    true,
	}
	if s.bus == nil {
		return
	}
	sub, _ := s.bus.Subscribe("notify-webhook", func(e bus.Event) bool {
		return interesting[e.Type]
	})
	client := &http.Client{Timeout: 5 * time.Second}
	go func() {
		for e := range sub.Ch {
			body, err := json.Marshal(map[string]interface{}{
				"event":      e.Type,
				"run_id":     e.RunID,
				"project_id": e.ProjectID,
				"ts":         e.TS.UTC().Format(time.RFC3339),
				"data":       e.Data,
			})
			if err != nil {
				continue
			}
			for attempt := 0; attempt < 2; attempt++ {
				req, err := http.NewRequest("POST", url, bytes.NewReader(body))
				if err != nil {
					break
				}
				req.Header.Set("Content-Type", "application/json")
				if secret != "" {
					// The GitHub convention — sha256=<hex> in
					// X-Hub-Signature-256 — because that is what webhook
					// receivers already verify; a bespoke header worked for
					// nobody, starting with Hermes.
					mac := hmac.New(sha256.New, []byte(secret))
					mac.Write(body)
					req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
				}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode < 500 {
						break
					}
				}
				time.Sleep(2 * time.Second)
			}
		}
	}()
}
