package monitor

import (
	"encoding/json"
	"errors"
	"net"
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
	http       *http.Server
	store      *Store
	staleAfter time.Duration
	errors     chan error
}

func New(address string, store *Store, staleAfter ...time.Duration) *Server {
	maxAge := 30 * time.Second
	if len(staleAfter) > 0 && staleAfter[0] > maxAge {
		maxAge = staleAfter[0]
	}
	s := &Server{store: store, staleAfter: maxAge, errors: make(chan error, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/status", s.statusHandler)
	mux.HandleFunc("/metrics", s.statusHandler)
	s.http = &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return s
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	go func() {
		if serveErr := s.http.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case s.errors <- serveErr:
			default:
			}
		}
	}()
	return nil
}
func (s *Server) Errors() <-chan error { return s.errors }
func (s *Server) Shutdown() error      { return s.http.Close() }
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	st := s.store.Get()
	if s.statusStale(st) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "engine status stale", "updated_at": st.UpdatedAt})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	st := s.store.Get()
	code := http.StatusOK
	stale := s.statusStale(st)
	if !st.Ready || stale {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": st.Ready && !stale, "mode": st.Mode, "last_error": st.LastError, "status_stale": stale})
}
func (s *Server) statusHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Get())
}
func (s *Server) statusStale(st domain.RuntimeStatus) bool {
	return st.UpdatedAt.IsZero() || time.Since(st.UpdatedAt) > s.staleAfter
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
