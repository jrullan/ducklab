package engineclt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleasePlanCarriesARevisionNote(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/calc/releases" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"r-release-rev"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := client.ReleasePlan("calc", "", "Correct the inventory."); err != nil {
		t.Fatal(err)
	}
	if got["bump"] != "" || got["revise"] != "Correct the inventory." {
		t.Fatalf("body = %#v", got)
	}
}

func TestReleasePlanOmitsAnEmptyRevision(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"id":"r-release"}`))
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := client.ReleasePlan("calc", "minor", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["revise"]; ok {
		t.Fatalf("empty revision should be omitted: %#v", got)
	}
}
