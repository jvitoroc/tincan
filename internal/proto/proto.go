// Package proto defines tincan's wire format: the JSON messages exchanged on the
// control stream, their framing, and the binary headers used for voice and video.
package proto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"strings"
)

// ALPN is the TLS application protocol negotiated on every tincan QUIC connection.
const ALPN = "tincan"

// DefaultPort is used when an address has no port.
const DefaultPort = "7777"

// Envelope is the unit of the control stream.
//
// A request carries ID and Type. The response to it carries Re (the request's ID)
// and either Data or Error. An event is pushed by the server and carries only Type
// and Data.
type Envelope struct {
	ID    uint64          `json:"id,omitempty"`
	Re    uint64          `json:"re,omitempty"`
	Type  string          `json:"type,omitempty"`
	Error *Error          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func (e Envelope) IsRequest() bool { return e.ID != 0 && e.Re == 0 }
func (e Envelope) IsReply() bool   { return e.Re != 0 }
func (e Envelope) IsEvent() bool   { return e.ID == 0 && e.Re == 0 && e.Type != "" }

// Decode unmarshals the envelope's data into v. Empty data leaves v untouched.
func (e Envelope) Decode(v any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

func NewRequest(id uint64, typ string, data any) (Envelope, error) {
	raw, err := marshal(data)
	return Envelope{ID: id, Type: typ, Data: raw}, err
}

func NewReply(re uint64, data any) (Envelope, error) {
	raw, err := marshal(data)
	return Envelope{Re: re, Data: raw}, err
}

func NewErrorReply(re uint64, code, message string) Envelope {
	return Envelope{Re: re, Error: &Error{Code: code, Message: message}}
}

func NewEvent(typ string, data any) (Envelope, error) {
	raw, err := marshal(data)
	return Envelope{Type: typ, Data: raw}, err
}

func marshal(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// Error is a failed request's reply. Code is stable and machine-readable; Message
// is meant for humans.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Error codes.
const (
	CodeBadRequest           = "bad_request"
	CodeUnknownType          = "unknown_type"
	CodeHelloRequired        = "hello_required"
	CodeIncompatibleProtocol = "incompatible_protocol"
	CodeInternal             = "internal"
)

// Request types.
const (
	TypeHello = "hello"
	TypePing  = "ping"
)

// Hello must be the first request on a connection.
type Hello struct {
	Protocol      int    `json:"protocol"`
	ClientVersion string `json:"client_version"`
}

type HelloReply struct {
	Protocol      int    `json:"protocol"`
	ServerVersion string `json:"server_version"`
}

type PingReply struct {
	ServerTime int64 `json:"server_time"` // unix milliseconds
}

// Fingerprint identifies a certificate by the SHA-256 of its DER encoding,
// formatted like ssh-keygen: "SHA256:<unpadded base64>".
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// NormalizeAddr adds DefaultPort to an address that has none, so "example.com",
// "10.0.0.2" and "::1" become dialable.
func NormalizeAddr(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), DefaultPort)
}
