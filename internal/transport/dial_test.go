package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDialTrustOnFirstUse(t *testing.T) {
	cert, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen("127.0.0.1:0", cert)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.CloseWithError(0, "") })
		}
	}()

	addr := ln.Addr().String()
	want := Fingerprint(cert)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, peer, err := Dial(ctx, addr, "")
	if err != nil {
		t.Fatal(err)
	}
	conn.CloseWithError(0, "")
	if peer.Trust != TrustNew || peer.Fingerprint != want {
		t.Fatalf("first contact: got %+v, want TrustNew with %s", peer, want)
	}

	conn, peer, err = Dial(ctx, addr, want)
	if err != nil {
		t.Fatal(err)
	}
	conn.CloseWithError(0, "")
	if peer.Trust != TrustPinned {
		t.Fatalf("pinned: got trust %v", peer.Trust)
	}

	_, _, err = Dial(ctx, addr, "SHA256:somebody-else")
	var mismatch *MismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("wrong pin: got %v, want *MismatchError", err)
	}
	if mismatch.Presented != want || mismatch.Pinned != "SHA256:somebody-else" {
		t.Fatalf("mismatch details: %+v", mismatch)
	}
}
