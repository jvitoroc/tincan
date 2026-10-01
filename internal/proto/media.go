package proto

import (
	"encoding/binary"
	"errors"
	"io"
)

// Stream and datagram kinds: the first byte of every voice datagram and of every
// unidirectional video stream.
const (
	KindVoice        byte = 0x01 // datagram: one Opus packet
	KindVideoPublish byte = 0x02 // uni stream, client → server: frames from one camera
	KindVideoRelay   byte = 0x03 // uni stream, server → client: frames from every camera in the room
)

// Voice datagram layout: [kind u8][ssrc u32][seq u16][opus payload…]

const VoiceHeaderSize = 7

// VoiceHeader precedes each Opus packet. SSRC identifies the sender within a
// voice room; the server overwrites it with the sender's assigned value.
type VoiceHeader struct {
	SSRC uint32
	Seq  uint16
}

var ErrShortDatagram = errors.New("proto: datagram too short")

// AppendVoice appends a complete voice datagram to dst.
func AppendVoice(dst []byte, h VoiceHeader, payload []byte) []byte {
	dst = append(dst, KindVoice)
	dst = binary.BigEndian.AppendUint32(dst, h.SSRC)
	dst = binary.BigEndian.AppendUint16(dst, h.Seq)
	return append(dst, payload...)
}

// ParseVoice splits a voice datagram into its header and payload. The payload
// aliases b.
func ParseVoice(b []byte) (VoiceHeader, []byte, error) {
	if len(b) < VoiceHeaderSize {
		return VoiceHeader{}, nil, ErrShortDatagram
	}
	if b[0] != KindVoice {
		return VoiceHeader{}, nil, errors.New("proto: not a voice datagram")
	}
	h := VoiceHeader{
		SSRC: binary.BigEndian.Uint32(b[1:5]),
		Seq:  binary.BigEndian.Uint16(b[5:7]),
	}
	return h, b[VoiceHeaderSize:], nil
}

// StampVoiceSSRC overwrites a voice datagram's SSRC in place, so the server never
// relays an identity the client chose itself.
func StampVoiceSSRC(b []byte, ssrc uint32) error {
	if len(b) < VoiceHeaderSize || b[0] != KindVoice {
		return ErrShortDatagram
	}
	binary.BigEndian.PutUint32(b[1:5], ssrc)
	return nil
}

// Video frames follow the kind byte on a unidirectional stream:
//
//	published (client → server): [len u32][seq u32][jpeg…]
//	relayed   (server → client): [len u32][ssrc u32][seq u32][jpeg…]
//
// len counts only the JPEG bytes.

// MaxVideoFrameSize bounds one JPEG; real frames are a few KB.
const MaxVideoFrameSize = 256 << 10

var ErrVideoFrameTooLarge = errors.New("proto: video frame too large")

type VideoFrame struct {
	SSRC uint32 // only set on relayed frames
	Seq  uint32
	JPEG []byte
}

func WritePublishedFrame(w io.Writer, f VideoFrame) error {
	return writeVideoFrame(w, f, false)
}

func ReadPublishedFrame(r io.Reader) (VideoFrame, error) {
	return readVideoFrame(r, false)
}

func WriteRelayedFrame(w io.Writer, f VideoFrame) error {
	return writeVideoFrame(w, f, true)
}

func ReadRelayedFrame(r io.Reader) (VideoFrame, error) {
	return readVideoFrame(r, true)
}

func writeVideoFrame(w io.Writer, f VideoFrame, withSSRC bool) error {
	if len(f.JPEG) > MaxVideoFrameSize {
		return ErrVideoFrameTooLarge
	}
	buf := make([]byte, 0, 12+len(f.JPEG))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(f.JPEG)))
	if withSSRC {
		buf = binary.BigEndian.AppendUint32(buf, f.SSRC)
	}
	buf = binary.BigEndian.AppendUint32(buf, f.Seq)
	buf = append(buf, f.JPEG...)
	_, err := w.Write(buf)
	return err
}

func readVideoFrame(r io.Reader, withSSRC bool) (VideoFrame, error) {
	hdrLen := 8
	if withSSRC {
		hdrLen = 12
	}
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:hdrLen]); err != nil {
		return VideoFrame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	if n > MaxVideoFrameSize {
		return VideoFrame{}, ErrVideoFrameTooLarge
	}
	var f VideoFrame
	if withSSRC {
		f.SSRC = binary.BigEndian.Uint32(hdr[4:8])
		f.Seq = binary.BigEndian.Uint32(hdr[8:12])
	} else {
		f.Seq = binary.BigEndian.Uint32(hdr[4:8])
	}
	f.JPEG = make([]byte, n)
	if _, err := io.ReadFull(r, f.JPEG); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return VideoFrame{}, err
	}
	return f, nil
}
