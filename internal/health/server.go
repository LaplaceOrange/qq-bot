package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fsykk/qq-bot/internal/qq"
	"github.com/fsykk/qq-bot/internal/store"
)

type Server struct {
	Store   *store.Store
	QQ      *qq.Client
	Gateway *qq.Gateway
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.Store.Ping() != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, err := s.QQ.AccessToken(ctx)
		checks := map[string]bool{"database": s.Store.Ping() == nil, "qq_gateway": s.Gateway.Connected(), "qq_token": err == nil}
		status, state := http.StatusOK, "ready"
		for _, ok := range checks {
			if !ok {
				status, state = http.StatusServiceUnavailable, "not_ready"
			}
		}
		writeJSON(w, status, map[string]any{"status": state, "checks": checks})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
