// Package version reports which tincan build is running and which wire protocol it speaks.
package version

import (
	"fmt"
	"runtime/debug"
)

// Protocol is the wire protocol version. Bump it only for breaking changes to the
// control stream or media formats; clients and servers must speak the same one.
const Protocol = 1

// Module is the Go module path, used in update instructions.
const Module = "github.com/jvitoroc/tincan"

// Version returns the module version stamped into the binary: the release tag
// ("v0.1.0") for release builds and go install, a pseudo-version for untagged
// commits, or "dev" when there is no version information.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

// String formats a binary's name with its version and protocol, e.g.
// "tincan v0.1.0 (protocol 1)".
func String(name string) string {
	return fmt.Sprintf("%s %s (protocol %d)", name, Version(), Protocol)
}

// UpdateHint returns the command that updates this binary, matching how it was
// installed: go install records a module checksum, release builds don't.
func UpdateHint(cmd string) string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Sum != "" {
		return fmt.Sprintf("go install %s/cmd/%s@latest", Module, cmd)
	}
	script := "curl -fsSL https://raw.githubusercontent.com/jvitoroc/tincan/main/install.sh | sh"
	if cmd == "tincan-server" {
		return script + " -s -- --server"
	}
	return script
}
