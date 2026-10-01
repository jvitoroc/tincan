package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrameSize bounds a control-stream message so a peer can't make us allocate
// arbitrary amounts of memory.
const MaxFrameSize = 64 << 10

var ErrFrameTooLarge = errors.New("proto: frame too large")

// WriteFrame writes v as JSON prefixed by its length (4 bytes, big-endian). The
// frame goes out in a single Write so concurrent writers can't interleave it,
// though callers still need to serialize writes.
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(body) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body)))
	copy(buf[4:], body)
	_, err = w.Write(buf)
	return err
}

// ReadFrame reads one length-prefixed JSON frame into v. It returns io.EOF only
// when the stream ends cleanly between frames.
func ReadFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("proto: bad frame: %w", err)
	}
	return nil
}
