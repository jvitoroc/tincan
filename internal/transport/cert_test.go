package transport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateCertPersists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")

	first, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(first) != Fingerprint(second) {
		t.Fatal("fingerprint changed across restarts")
	}

	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits; Go always reports 0666 there.
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Fatalf("key file mode %v, want 0600", perm)
	}
}

func TestLoadOrCreateCertRefusesHalfPair(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateCert(dir)
	if err == nil || !strings.Contains(err.Error(), "only one of") {
		t.Fatalf("got %v, want a half-pair error", err)
	}
}
