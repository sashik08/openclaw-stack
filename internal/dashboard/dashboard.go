// Package dashboard — веб-дашборд мониторинга с Basic Auth. Отдаёт статус
// сервисов, ресурсы, историю рестартов и логи из monitor.Store, а также
// принимает команды ручного перезапуска.
package dashboard

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"time"

	"deploystack/internal/config"
	"deploystack/internal/monitor"
)

//go:embed index.html
var indexHTML []byte

// Server — HTTP-дашборд.
type Server struct {
	cfg   *config.Config
	store *monitor.Store
	mon   *monitor.Monitor
}

// New создаёт сервер дашборда.
func New(cfg *config.Config, store *monitor.Store, mon *monitor.Monitor) *Server {
	return &Server{cfg: cfg, store: store, mon: mon}
}

// Handler возвращает готовый http.Handler со всеми маршрутами и авторизацией.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/restart", s.handleRestart)
	return s.basicAuth(mux)
}

// basicAuth защищает все маршруты логином/паролем из конфига.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		// constant-time сравнение, чтобы не давать тайминговую утечку.
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.DashboardUser)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.DashboardPass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="OpenClaw Stack Monitor"`)
			http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Snapshot())
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "только POST", http.StatusMethodNotAllowed)
		return
	}
	service := r.URL.Query().Get("service")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.mon.Restart(ctx, service); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "перезапуск запущен", "service": service})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
