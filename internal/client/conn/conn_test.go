package conn

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/server/hub"
	"github.com/jvitoroc/tincan/internal/transport"
)

func startServer(t *testing.T) (addr, fingerprint string, shutdown context.CancelFunc) {
	t.Helper()
	cert, err := transport.LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen("127.0.0.1:0", cert)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		hub.New(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		cancel()
		<-served
		ln.Close()
	})
	return ln.Addr().String(), transport.Fingerprint(cert), cancel
}

func dial(t *testing.T, addr, pin string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr, pin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDialAndPing(t *testing.T) {
	addr, fp, _ := startServer(t)
	c := dial(t, addr, "")

	if p := c.Peer(); p.Trust != transport.TrustNew || p.Fingerprint != fp {
		t.Fatalf("peer = %+v, want new server with %s", p, fp)
	}
	if c.ServerVersion() == "" {
		t.Fatal("no server version from hello")
	}
	rtt, err := c.Ping(context.Background())
	if err != nil || rtt <= 0 {
		t.Fatalf("ping: %v %v", rtt, err)
	}

	pinned := dial(t, addr, fp)
	if pinned.Peer().Trust != transport.TrustPinned {
		t.Fatalf("trust = %v, want pinned", pinned.Peer().Trust)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = Dial(ctx, addr, "SHA256:not-it")
	var mismatch *transport.MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("wrong pin: got %v", err)
	}
}

func TestConcurrentCallsGetTheirOwnReplies(t *testing.T) {
	addr, _, _ := startServer(t)
	c := dial(t, addr, "")

	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Go(func() {
			if _, err := c.Ping(context.Background()); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	err := c.Call(context.Background(), "nonsense", nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeUnknownType {
		t.Fatalf("unknown request: got %v", err)
	}
}

func TestServerShutdown(t *testing.T) {
	addr, _, shutdown := startServer(t)
	c := dial(t, addr, "")

	shutdown()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client didn't notice the shutdown")
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "server shutting down") {
		t.Fatalf("Err() = %v, want the server's reason", err)
	}
	if _, err := c.Ping(context.Background()); err == nil {
		t.Fatal("ping succeeded on a dead connection")
	}
	select {
	case _, ok := <-c.Events():
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Events() not closed after disconnect")
	}
}

func TestClose(t *testing.T) {
	addr, _, _ := startServer(t)
	c := dial(t, addr, "")
	c.Close()
	if _, err := c.Ping(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("ping after Close: %v", err)
	}
}

// The pump must accept events while nobody reads them, deliver them in order,
// and close Events once the reader is gone and the queue is drained.
func TestEventPump(t *testing.T) {
	c := &Client{
		incoming: make(chan proto.Envelope),
		events:   make(chan proto.Envelope),
		closing:  make(chan struct{}),
	}
	go c.pumpEvents()

	for i := range 100 {
		select {
		case c.incoming <- proto.Envelope{Type: "e", ID: uint64(i)}:
		case <-time.After(time.Second):
			t.Fatalf("pump blocked the reader at event %d", i)
		}
	}
	close(c.incoming)

	for i := range 100 {
		ev, ok := <-c.events
		if !ok || ev.ID != uint64(i) {
			t.Fatalf("event %d: got %+v ok=%v", i, ev, ok)
		}
	}
	if _, ok := <-c.events; ok {
		t.Fatal("events not closed after drain")
	}
}
