// Package conn is the client's connection to a tincan server. It dials, says
// hello, and turns the control stream into request/response calls plus a
// stream of events pushed by the server.
package conn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/transport"
	"github.com/jvitoroc/tincan/internal/version"
	"github.com/quic-go/quic-go"
)

// ErrClosed is returned by calls made after Close.
var ErrClosed = errors.New("connection closed")

type Client struct {
	qc     *quic.Conn
	stream *quic.Stream
	peer   transport.Peer
	server proto.HelloReply

	writeMu sync.Mutex
	nextID  atomic.Uint64

	mu      sync.Mutex
	pending map[uint64]chan proto.Envelope

	// Events flow reader → incoming → pumpEvents → events. The pump buffers
	// without limit so a slow consumer never stalls the reader, which also
	// delivers replies.
	incoming  chan proto.Envelope
	events    chan proto.Envelope
	closing   chan struct{} // closed by Close
	closeOnce sync.Once

	done     chan struct{}
	err      error // why the connection ended; written before done is closed
	failOnce sync.Once
}

// Dial connects to addr, checks the server's certificate against pin (see
// transport.Dial for the rules), and performs the hello handshake. A
// *proto.Error with CodeIncompatibleProtocol means client and server need to be
// on matching versions.
func Dial(ctx context.Context, addr, pin string) (*Client, error) {
	qc, peer, err := transport.Dial(ctx, addr, pin)
	if err != nil {
		return nil, err
	}
	stream, err := qc.OpenStreamSync(ctx)
	if err != nil {
		qc.CloseWithError(0, "")
		return nil, err
	}

	c := &Client{
		qc:       qc,
		stream:   stream,
		peer:     peer,
		pending:  make(map[uint64]chan proto.Envelope),
		incoming: make(chan proto.Envelope),
		events:   make(chan proto.Envelope),
		closing:  make(chan struct{}),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	go c.pumpEvents()

	hello := proto.Hello{Protocol: version.Protocol, ClientVersion: version.Version()}
	if err := c.Call(ctx, proto.TypeHello, hello, &c.server); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Peer reports the server's certificate fingerprint and why it was trusted.
func (c *Client) Peer() transport.Peer { return c.peer }

// ServerVersion is the server's build version, from the hello reply.
func (c *Client) ServerVersion() string { return c.server.ServerVersion }

// Events delivers server-pushed events in order. It is closed when the
// connection ends and every buffered event has been delivered.
func (c *Client) Events() <-chan proto.Envelope { return c.events }

// Done is closed when the connection ends; Err then says why.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Call sends a request and waits for its reply, decoding the reply's data into
// resp (which may be nil). A failed request returns a *proto.Error.
func (c *Client) Call(ctx context.Context, typ string, req, resp any) error {
	id := c.nextID.Add(1)
	env, err := proto.NewRequest(id, typ, req)
	if err != nil {
		return err
	}

	ch := make(chan proto.Envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	c.writeMu.Lock()
	err = proto.WriteFrame(c.stream, env)
	c.writeMu.Unlock()
	if err != nil {
		if cerr := c.Err(); cerr != nil {
			return cerr
		}
		return err
	}

	select {
	case rep := <-ch:
		if rep.Error != nil {
			return rep.Error
		}
		if resp != nil {
			return rep.Decode(resp)
		}
		return nil
	case <-c.done:
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Ping measures a round trip through the server's request loop.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := c.Call(ctx, proto.TypePing, nil, nil); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// Close ends the connection. Calls in flight return ErrClosed.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.closing) })
	c.fail(ErrClosed)
	return c.qc.CloseWithError(0, "")
}

func (c *Client) fail(err error) {
	c.failOnce.Do(func() {
		var appErr *quic.ApplicationError
		if errors.As(err, &appErr) && appErr.Remote {
			if appErr.ErrorMessage != "" {
				err = fmt.Errorf("server closed the connection: %s", appErr.ErrorMessage)
			} else {
				err = errors.New("server closed the connection")
			}
		}
		c.err = err
		close(c.done)
	})
}

func (c *Client) readLoop() {
	defer close(c.incoming)
	r := bufio.NewReader(c.stream)
	for {
		var env proto.Envelope
		if err := proto.ReadFrame(r, &env); err != nil {
			c.fail(err)
			return
		}
		switch {
		case env.IsReply():
			c.mu.Lock()
			ch := c.pending[env.Re]
			c.mu.Unlock()
			if ch != nil {
				ch <- env // buffered, and each ID gets exactly one reply
			}
		case env.IsEvent():
			select {
			case c.incoming <- env:
			case <-c.closing:
				return
			}
		}
	}
}

func (c *Client) pumpEvents() {
	defer close(c.events)
	in := c.incoming
	var queue []proto.Envelope
	for in != nil || len(queue) > 0 {
		var out chan<- proto.Envelope
		var next proto.Envelope
		if len(queue) > 0 {
			out, next = c.events, queue[0]
		}
		select {
		case env, ok := <-in:
			if !ok {
				in = nil
				continue
			}
			queue = append(queue, env)
		case out <- next:
			queue = queue[1:]
		case <-c.closing:
			return
		}
	}
}
