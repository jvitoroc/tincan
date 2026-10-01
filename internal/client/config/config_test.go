package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tincan")

	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Fingerprint("example.com:7777") != "" {
		t.Fatal("empty config has a pin")
	}

	c.PinFingerprint("example.com:7777", "SHA256:abc")
	c.LastServer = "example.com:7777"
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits; Go always reports 0666 there.
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Fatalf("config mode %v, want 0600", perm)
	}

	c, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Fingerprint("example.com:7777"); got != "SHA256:abc" {
		t.Fatalf("pin after reload = %q", got)
	}

	if !c.Forget("example.com:7777") || c.Forget("example.com:7777") {
		t.Fatal("Forget should report true once, then false")
	}
	if c.LastServer != "" || c.Fingerprint("example.com:7777") != "" {
		t.Fatalf("forgotten server still present: %+v", c)
	}
}

func TestLoadRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("loaded a corrupt config")
	}
}

func TestDefaultDirHonorsEnv(t *testing.T) {
	t.Setenv("TINCAN_CONFIG_DIR", "/tmp/somewhere")
	if dir, _ := DefaultDir(); dir != "/tmp/somewhere" {
		t.Fatalf("DefaultDir = %q", dir)
	}
}
