package cli

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestStandbyTakesOverWhenLeaderGoes(t *testing.T) {
	url := storetest.URL(t)
	ctx := context.Background()
	open := func() *store.Store {
		s, err := store.Open(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	a, b := open(), open()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	leader, err := becomeLeader(ctx, log, a, freeAddr(t))
	if err != nil || leader == nil {
		t.Fatalf("first replica should lead: %v", err)
	}

	addr := freeAddr(t)
	got := make(chan *store.Lock, 1)
	go func() {
		l, _ := becomeLeader(ctx, log, b, addr)
		got <- l
	}()

	// While waiting, the standby is alive but not ready.
	var health, ready int
	for i := 0; i < 50 && health != 200; i++ {
		time.Sleep(100 * time.Millisecond)
		if r, err := http.Get("http://" + addr + "/healthz"); err == nil {
			health = r.StatusCode
			r.Body.Close()
		}
	}
	if r, err := http.Get("http://" + addr + "/readyz"); err == nil {
		ready = r.StatusCode
		r.Body.Close()
	}
	if health != 200 || ready != http.StatusServiceUnavailable {
		t.Errorf("standby health %d ready %d", health, ready)
	}
	select {
	case <-got:
		t.Fatal("standby must not lead while the leader holds the lock")
	default:
	}

	leader.Release()
	select {
	case l := <-got:
		if l == nil {
			t.Fatal("standby did not become leader")
		}
		l.Release()
	case <-time.After(10 * time.Second):
		t.Fatal("standby did not take over within 10s")
	}
}
