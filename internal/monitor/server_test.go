package monitor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"xaut-paper-bot/internal/domain"
)

func TestHealthFailsWhenEngineStatusIsStale(t *testing.T) {
	store := NewStore(time.Now().Add(-time.Minute))
	server := New("127.0.0.1:0", store, 30*time.Second)
	recorder := httptest.NewRecorder()
	server.health(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestReadyFailsWhenLastReadyStatusIsStale(t *testing.T) {
	store := NewStore(time.Now())
	store.Set(domain.RuntimeStatus{Ready: true, UpdatedAt: time.Now().Add(-time.Minute)})
	server := New("127.0.0.1:0", store, 30*time.Second)
	recorder := httptest.NewRecorder()
	server.ready(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestStartReportsBindFailureSynchronously(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := New(listener.Addr().String(), NewStore(time.Now()))
	if err := server.Start(); err == nil {
		server.Shutdown()
		t.Fatal("expected bind failure")
	}
}
