// Package hub runs the server side of tincan connections: it accepts each
// client's control stream and answers its requests.
package hub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/jvitoroc/tincan/internal/proto"
	"github.com/jvitoroc/tincan/internal/version"
	"github.com/quic-go/quic-go"
)

// QUIC application error codes used when the server closes a connection.
const (
	closeOK       quic.ApplicationErrorCode = 0
	closeProtocol quic.ApplicationErrorCode = 1
	closeShutdown quic.ApplicationErrorCode = 2
)

// controlStreamTimeout is how long a new connection has to open its control stream.
const controlStreamTimeout = 10 * time.Second

type Hub struct {
	log *slog.Logger
	wg  sync.WaitGroup
}

func New(log *slog.Logger) *Hub {
	return &Hub{log: log}
}

// Serve accepts connections until ctx is canceled or the listener fails, then
// waits for every session to end. Canceling ctx disconnects all clients.
func (h *Hub) Serve(ctx context.Context, ln *quic.Listener) error {
	defer h.wg.Wait()
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		h.wg.Go(func() { h.serveConn(ctx, conn) })
	}
}

func (h *Hub) serveConn(ctx context.Context, conn *quic.Conn) {
	log := h.log.With("remote", conn.RemoteAddr().String())

	acceptCtx, cancel := context.WithTimeout(ctx, controlStreamTimeout)
	stream, err := conn.AcceptStream(acceptCtx)
	cancel()
	if err != nil {
		conn.CloseWithError(closeProtocol, "expected a control stream")
		return
	}

	log.Info("connected")
	s := &session{
		conn:   conn,
		stream: stream,
		log:    log,
		send:   make(chan proto.Envelope, 64),
	}
	log.Info("disconnected", "reason", describeEnd(s.run(ctx)))
}

// describeEnd turns the error that ended a session into a log-friendly reason.
func describeEnd(err error) string {
	var appErr *quic.ApplicationError
	switch {
	case errors.Is(err, io.EOF), errors.As(err, &appErr) && appErr.Remote && appErr.ErrorCode == closeOK:
		return "client left"
	case errors.Is(err, context.Canceled):
		return "server shutting down"
	default:
		return err.Error()
	}
}

// session is one client connection. Requests are handled one at a time on the
// read goroutine; everything sent to the client goes through send, drained by a
// single writer goroutine.
type session struct {
	conn    *quic.Conn
	stream  *quic.Stream
	log     *slog.Logger
	send    chan proto.Envelope
	greeted bool
}

func (s *session) run(parent context.Context) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)

	go func() { cancel(s.writeLoop(ctx)) }()
	go func() {
		<-ctx.Done()
		if parent.Err() != nil {
			s.conn.CloseWithError(closeShutdown, "server shutting down")
		} else {
			s.conn.CloseWithError(closeOK, "")
		}
	}()

	cancel(s.readLoop(ctx))
	return context.Cause(ctx)
}

func (s *session) readLoop(ctx context.Context) error {
	r := bufio.NewReader(s.stream)
	for {
		var req proto.Envelope
		if err := proto.ReadFrame(r, &req); err != nil {
			return err
		}
		if !req.IsRequest() {
			return errors.New("client sent a frame that isn't a request")
		}
		select {
		case s.send <- s.handle(req):
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func (s *session) writeLoop(ctx context.Context) error {
	w := bufio.NewWriter(s.stream)
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case env := <-s.send:
			if err := proto.WriteFrame(w, env); err != nil {
				return err
			}
			if len(s.send) == 0 {
				if err := w.Flush(); err != nil {
					return err
				}
			}
		}
	}
}

func (s *session) handle(req proto.Envelope) proto.Envelope {
	if !s.greeted && req.Type != proto.TypeHello {
		return proto.NewErrorReply(req.ID, proto.CodeHelloRequired, "the first request must be hello")
	}
	switch req.Type {
	case proto.TypeHello:
		return s.hello(req)
	case proto.TypePing:
		return reply(req.ID, proto.PingReply{ServerTime: time.Now().UnixMilli()})
	default:
		return proto.NewErrorReply(req.ID, proto.CodeUnknownType, fmt.Sprintf("unknown request type %q", req.Type))
	}
}

func (s *session) hello(req proto.Envelope) proto.Envelope {
	if s.greeted {
		return proto.NewErrorReply(req.ID, proto.CodeBadRequest, "hello was already sent")
	}
	var h proto.Hello
	if err := req.Decode(&h); err != nil {
		return proto.NewErrorReply(req.ID, proto.CodeBadRequest, "malformed hello")
	}
	if h.Protocol != version.Protocol {
		s.log.Info("incompatible client", "client_version", h.ClientVersion, "client_protocol", h.Protocol)
		return proto.NewErrorReply(req.ID, proto.CodeIncompatibleProtocol, fmt.Sprintf(
			"this server (%s) speaks protocol %d, but your client (%s) speaks protocol %d",
			version.Version(), version.Protocol, h.ClientVersion, h.Protocol))
	}
	s.greeted = true
	s.log.Info("hello", "client_version", h.ClientVersion)
	return reply(req.ID, proto.HelloReply{Protocol: version.Protocol, ServerVersion: version.Version()})
}

func reply(re uint64, data any) proto.Envelope {
	env, err := proto.NewReply(re, data)
	if err != nil {
		return proto.NewErrorReply(re, proto.CodeInternal, "internal error")
	}
	return env
}
