package engineapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/service"
)

// An older client may know only one budget field. Omitting the rest must mean
// "leave it alone", whereas explicitly setting a limit to zero remains invalid.
func TestBudgetDefaultsPutPreservesOmittedFieldsButRejectsExplicitZero(t *testing.T) {
	s, err := service.New(config.DefaultGlobal(), service.Options{
		Bus:        bus.New(16),
		ConfigPath: filepath.Join(t.TempDir(), "config.toml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(s, bus.New(16), "token", "test", ""))
	t.Cleanup(server.Close)

	get := func() service.BudgetView {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/defaults/budget", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET status = %d", resp.StatusCode)
		}
		var got service.BudgetView
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	put := func(body string) (int, service.BudgetView) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, server.URL+"/v1/defaults/budget", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got service.BudgetView
		if resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
		}
		return resp.StatusCode, got
	}

	before := get()
	status, saved := put(`{"max_tokens":1500000}`)
	if status != http.StatusOK {
		t.Fatalf("partial PUT status = %d, want %d", status, http.StatusOK)
	}
	if saved.MaxTokens != 1_500_000 {
		t.Errorf("max_tokens = %d, want 1500000", saved.MaxTokens)
	}
	if saved.MaxUSD != before.MaxUSD || saved.MaxTurns != before.MaxTurns || saved.MaxWallclockS != before.MaxWallclockS || saved.WallclockEscalationMultiplier != before.WallclockEscalationMultiplier {
		t.Errorf("partial PUT changed omitted fields: got %+v, before %+v", saved, before)
	}

	status, _ = put(`{"wallclock_escalation_multiplier":0}`)
	if status != http.StatusBadRequest {
		t.Errorf("explicit zero multiplier status = %d, want %d", status, http.StatusBadRequest)
	}
	if got := get(); got.WallclockEscalationMultiplier != before.WallclockEscalationMultiplier {
		t.Errorf("rejected zero multiplier was saved: got %v, want %v", got.WallclockEscalationMultiplier, before.WallclockEscalationMultiplier)
	}
}
