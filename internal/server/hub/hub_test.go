package hub

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/transport"
	"github.com/jvitoroc/tincan/internal/version"
	"github.com/quic-go/quic-go"
)

// startServer runs a hub on a random local port. Canceling the returned context
// shuts it down; done is closed once Serve has returned.
func startServer(t *testing.T) (addr string, shutdown context.CancelFunc, done <-chan struct{}) {
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
		if err := New(slog.New(slog.NewTextHandler(io.Discard, nil))).Serve(ctx, ln); err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-served
		ln.Close()
	})
	return ln.Addr().String(), cancel, served
}

// rawClient speaks the control protocol by hand, so tests can send requests the
// real client never would.
type rawClient struct {
	t      *testing.T
	conn   *quic.Conn
	stream *quic.Stream
	r      *bufio.Reader
	nextID uint64
}

func dialRaw(t *testing.T, addr string) *rawClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := transport.Dial(ctx, addr, "")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseWithError(0, "") })
	return &rawClient{t: t, conn: conn, stream: stream, r: bufio.NewReader(stream)}
}

func (c *rawClient) call(typ string, data any) proto.Envelope {
	c.t.Helper()
	c.nextID++
	req, err := proto.NewRequest(c.nextID, typ, data)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := proto.WriteFrame(c.stream, req); err != nil {
		c.t.Fatal(err)
	}
	c.stream.SetReadDeadline(time.Now().Add(5 * time.Second))
	var rep proto.Envelope
	if err := proto.ReadFrame(c.r, &rep); err != nil {
		c.t.Fatalf("reading reply to %s: %v", typ, err)
	}
	if rep.Re != c.nextID {
		c.t.Fatalf("reply to %d, want %d", rep.Re, c.nextID)
	}
	return rep
}

func wantCode(t *testing.T, rep proto.Envelope, code string) {
	t.Helper()
	if rep.Error == nil || rep.Error.Code != code {
		t.Fatalf("got %+v, want error %q", rep, code)
	}
}

func TestHelloAndPing(t *testing.T) {
	addr, _, _ := startServer(t)
	c := dialRaw(t, addr)

	wantCode(t, c.call(proto.TypePing, nil), proto.CodeHelloRequired)

	rep := c.call(proto.TypeHello, proto.Hello{Protocol: version.Protocol, ClientVersion: "test"})
	var hr proto.HelloReply
	if rep.Error != nil || rep.Decode(&hr) != nil || hr.Protocol != version.Protocol {
		t.Fatalf("hello: %+v", rep)
	}

	wantCode(t, c.call(proto.TypeHello, proto.Hello{Protocol: version.Protocol}), proto.CodeBadRequest)
	wantCode(t, c.call("nonsense", nil), proto.CodeUnknownType)

	var pr proto.PingReply
	rep = c.call(proto.TypePing, nil)
	if rep.Error != nil || rep.Decode(&pr) != nil {
		t.Fatalf("ping: %+v", rep)
	}
	if d := time.Since(time.UnixMilli(pr.ServerTime)); d < 0 || d > 5*time.Second {
		t.Fatalf("server time off by %v", d)
	}
}

func TestIncompatibleProtocol(t *testing.T) {
	addr, _, _ := startServer(t)
	c := dialRaw(t, addr)
	wantCode(t, c.call(proto.TypeHello, proto.Hello{Protocol: version.Protocol + 1, ClientVersion: "future"}), proto.CodeIncompatibleProtocol)
	// The connection stays usable for a correct hello.
	if rep := c.call(proto.TypeHello, proto.Hello{Protocol: version.Protocol}); rep.Error != nil {
		t.Fatalf("hello after mismatch: %+v", rep.Error)
	}
}

func TestShutdownDisconnectsClients(t *testing.T) {
	addr, shutdown, done := startServer(t)
	c := dialRaw(t, addr)
	c.call(proto.TypeHello, proto.Hello{Protocol: version.Protocol})

	shutdown()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve didn't return after shutdown")
	}
	select {
	case <-c.conn.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client wasn't disconnected")
	}
}
