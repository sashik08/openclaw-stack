package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"deploystack/internal/config"
	"deploystack/internal/monitor"
)

func testServer(t *testing.T) (*httptest.Server, *config.Config) {
	t.Helper()
	cfg := config.Defaults()
	cfg.DashboardUser = "admin"
	cfg.DashboardPass = "s3cret"
	specs := monitor.DefaultSpecs(cfg)
	store := monitor.NewStore(specs)
	mon := monitor.New(store, cfg, specs)
	srv := httptest.NewServer(New(cfg, store, mon).Handler())
	t.Cleanup(srv.Close)
	return srv, cfg
}

func TestBasicAuthRequired(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("без авторизации ожидали 401, получили %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("ожидали заголовок WWW-Authenticate")
	}
}

func TestBasicAuthWrongPassword(t *testing.T) {
	srv, _ := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/status", nil)
	req.SetBasicAuth("admin", "неверный")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("с неверным паролем ожидали 401, получили %d", resp.StatusCode)
	}
}

func TestStatusReturnsJSON(t *testing.T) {
	srv, cfg := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/status", nil)
	req.SetBasicAuth(cfg.DashboardUser, cfg.DashboardPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200, получили %d", resp.StatusCode)
	}
	var snap monitor.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("ответ не JSON Snapshot: %v", err)
	}
	if len(snap.Services) != 3 {
		t.Errorf("ожидали 3 сервиса в статусе, получили %d", len(snap.Services))
	}
}

func TestRestartRequiresPOST(t *testing.T) {
	srv, cfg := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/restart?service=OpenClaw", nil)
	req.SetBasicAuth(cfg.DashboardUser, cfg.DashboardPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET на /api/restart должен давать 405, получили %d", resp.StatusCode)
	}
}

func TestIndexServed(t *testing.T) {
	srv, cfg := testServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.SetBasicAuth(cfg.DashboardUser, cfg.DashboardPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ожидали 200 на /, получили %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("неожиданный Content-Type: %q", ct)
	}
}
