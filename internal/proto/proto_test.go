package proto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	req, err := NewRequest(7, TypeHello, Hello{Protocol: 1, ClientVersion: "v0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := NewReply(7, HelloReply{Protocol: 1, ServerVersion: "v0.0.2"})
	ev, _ := NewEvent("something.happened", map[string]int{"n": 3})
	for _, e := range []Envelope{req, rep, ev, NewErrorReply(9, CodeBadRequest, "nope")} {
		if err := WriteFrame(&buf, e); err != nil {
			t.Fatal(err)
		}
	}

	var got Envelope
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	var hello Hello
	if err := got.Decode(&hello); err != nil {
		t.Fatal(err)
	}
	if !got.IsRequest() || got.Type != TypeHello || hello.ClientVersion != "v0.0.1" {
		t.Fatalf("request: got %+v / %+v", got, hello)
	}

	got = Envelope{}
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	var hr HelloReply
	if err := got.Decode(&hr); err != nil {
		t.Fatal(err)
	}
	if !got.IsReply() || got.Re != 7 || hr.ServerVersion != "v0.0.2" {
		t.Fatalf("reply: got %+v / %+v", got, hr)
	}

	got = Envelope{}
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsEvent() || got.Type != "something.happened" {
		t.Fatalf("event: got %+v", got)
	}

	got = Envelope{}
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatal(err)
	}
	if !got.IsReply() || got.Error == nil || got.Error.Code != CodeBadRequest {
		t.Fatalf("error reply: got %+v", got)
	}

	if err := ReadFrame(&buf, &got); err != io.EOF {
		t.Fatalf("clean end: got %v, want io.EOF", err)
	}
}

func TestFrameLimits(t *testing.T) {
	big := strings.Repeat("x", MaxFrameSize)
	if err := WriteFrame(io.Discard, big); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized write: got %v", err)
	}

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	var v any
	if err := ReadFrame(bytes.NewReader(hdr[:]), &v); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized read: got %v", err)
	}

	binary.BigEndian.PutUint32(hdr[:], 10)
	truncated := append(hdr[:], `{"a"`...)
	if err := ReadFrame(bytes.NewReader(truncated), &v); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated body: got %v", err)
	}
	if err := ReadFrame(bytes.NewReader(hdr[:2]), &v); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated header: got %v", err)
	}
}

func FuzzReadFrame(f *testing.F) {
	var ok bytes.Buffer
	_ = WriteFrame(&ok, Envelope{ID: 1, Type: TypePing})
	f.Add(ok.Bytes())
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bytes.NewReader(b)
		for {
			var e Envelope
			if err := ReadFrame(r, &e); err != nil {
				return
			}
		}
	})
}

func TestVoiceDatagram(t *testing.T) {
	d := AppendVoice(nil, VoiceHeader{SSRC: 42, Seq: 65535}, []byte("opus"))
	if len(d) != VoiceHeaderSize+4 {
		t.Fatalf("len = %d", len(d))
	}
	h, payload, err := ParseVoice(d)
	if err != nil || h.SSRC != 42 || h.Seq != 65535 || string(payload) != "opus" {
		t.Fatalf("parse: %+v %q %v", h, payload, err)
	}

	if err := StampVoiceSSRC(d, 7); err != nil {
		t.Fatal(err)
	}
	h, payload, _ = ParseVoice(d)
	if h.SSRC != 7 || h.Seq != 65535 || string(payload) != "opus" {
		t.Fatalf("after stamp: %+v %q", h, payload)
	}

	if _, _, err := ParseVoice(d[:VoiceHeaderSize-1]); err == nil {
		t.Fatal("short datagram parsed")
	}
	if _, _, err := ParseVoice(append([]byte{KindVideoPublish}, d[1:]...)); err == nil {
		t.Fatal("wrong kind parsed")
	}
	if err := StampVoiceSSRC([]byte{KindVoice, 1}, 1); err == nil {
		t.Fatal("stamped short datagram")
	}
}

func TestVideoFrames(t *testing.T) {
	var buf bytes.Buffer
	in := VideoFrame{Seq: 9, JPEG: []byte("\xff\xd8jpeg")}
	if err := WritePublishedFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 8+len(in.JPEG) {
		t.Fatalf("published frame is %d bytes", buf.Len())
	}
	out, err := ReadPublishedFrame(&buf)
	if err != nil || out.Seq != 9 || out.SSRC != 0 || !bytes.Equal(out.JPEG, in.JPEG) {
		t.Fatalf("published: %+v %v", out, err)
	}

	in.SSRC = 3
	if err := WriteRelayedFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err = ReadRelayedFrame(&buf)
	if err != nil || out.Seq != 9 || out.SSRC != 3 || !bytes.Equal(out.JPEG, in.JPEG) {
		t.Fatalf("relayed: %+v %v", out, err)
	}

	if err := WritePublishedFrame(io.Discard, VideoFrame{JPEG: make([]byte, MaxVideoFrameSize+1)}); !errors.Is(err, ErrVideoFrameTooLarge) {
		t.Fatalf("oversized write: %v", err)
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:], MaxVideoFrameSize+1)
	if _, err := ReadPublishedFrame(bytes.NewReader(hdr[:])); !errors.Is(err, ErrVideoFrameTooLarge) {
		t.Fatalf("oversized read: %v", err)
	}
	binary.BigEndian.PutUint32(hdr[:], 100)
	if _, err := ReadPublishedFrame(bytes.NewReader(hdr[:])); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated: %v", err)
	}
}

func TestFingerprint(t *testing.T) {
	fp := Fingerprint([]byte("cert"))
	if !strings.HasPrefix(fp, "SHA256:") || len(fp) != len("SHA256:")+43 {
		t.Fatalf("fingerprint %q", fp)
	}
	if fp == Fingerprint([]byte("other")) {
		t.Fatal("different certs share a fingerprint")
	}
}

func TestNormalizeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":      "example.com:7777",
		"example.com:9000": "example.com:9000",
		"10.0.0.2":         "10.0.0.2:7777",
		"::1":              "[::1]:7777",
		"[::1]":            "[::1]:7777",
		"[::1]:9000":       "[::1]:9000",
	} {
		if got := NormalizeAddr(in); got != want {
			t.Errorf("NormalizeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
