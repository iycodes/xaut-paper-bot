package monitor

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"xaut-paper-bot/internal/domain"
)

type Store struct {
	mu     sync.RWMutex
	status domain.RuntimeStatus
}

func NewStore(start time.Time) *Store {
	return &Store{status: domain.RuntimeStatus{StartedAt: start, UpdatedAt: start}}
}
func (s *Store) Set(v domain.RuntimeStatus) { s.mu.Lock(); s.status = v; s.mu.Unlock() }
func (s *Store) Get() domain.RuntimeStatus  { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }

type Server struct {
	http  *http.Server
	store *Store
}

func New(address string, store *Store) *Server {
	s := &Server{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/status", s.statusHandler)
	mux.HandleFunc("/metrics", s.statusHandler)
	s.http = &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return s
}

func (s *Server) Start() error    { return s.http.ListenAndServe() }
func (s *Server) Shutdown() error { return s.http.Close() }
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	st := s.store.Get()
	code := http.StatusOK
	if !st.Ready {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": st.Ready, "mode": st.Mode, "last_error": st.LastError})
}
func (s *Server) statusHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Get())
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
