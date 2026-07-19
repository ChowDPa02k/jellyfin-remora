package supervisor

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChowDPa02K/jellyfin-remora/internal/config"
	"github.com/ChowDPa02K/jellyfin-remora/internal/jellyfin"
	"github.com/ChowDPa02K/jellyfin-remora/internal/model"
)

type blockingHealthStorage struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockingHealthStorage) CheckDisk(ctx context.Context, index int) model.StorageResult {
	s.calls.Add(1)
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return model.StorageResult{Index: index, Healthy: true}
}

func (*blockingHealthStorage) CheckPaths(context.Context) []model.StorageResult { return nil }

func TestImmediateHealthcheckIsNonBlockingSingleFlight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	storage := &blockingHealthStorage{started: make(chan struct{}, 1), release: make(chan struct{})}
	cfg := &config.Config{
		Remora: config.RemoraConfig{IOTimeout: config.Duration{Duration: time.Second}},
		Disks:  []config.DiskConfig{{Type: "physical", Target: "/test"}},
	}
	s := &Supervisor{
		cfg: cfg, storage: storage, client: jellyfin.New(server.URL, time.Second),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	returned := make(chan struct{})
	go func() {
		s.scheduleImmediateHealthcheck()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("healthcheck scheduling blocked on storage I/O")
	}
	select {
	case <-storage.started:
	case <-time.After(time.Second):
		t.Fatal("healthcheck worker did not start")
	}

	s.scheduleImmediateHealthcheck()
	time.Sleep(20 * time.Millisecond)
	if calls := storage.calls.Load(); calls != 1 {
		t.Fatalf("concurrent healthcheck workers=%d, want 1", calls)
	}
	close(storage.release)
	deadline := time.Now().Add(time.Second)
	for s.healthcheckRunning.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.healthcheckRunning.Load() {
		t.Fatal("healthcheck worker did not finish")
	}
}
