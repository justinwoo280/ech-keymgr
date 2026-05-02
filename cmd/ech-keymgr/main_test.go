package main

import (
	"strings"
	"testing"
)

// TestVersionDefaulted ensures the link-time -X main.Version override
// has a sane fallback, so `ech-keymgr --version` is never empty.
func TestVersionDefaulted(t *testing.T) {
	if strings.TrimSpace(Version) == "" {
		t.Errorf("Version must not be empty; got %q", Version)
	}
}

// TestRootCmdConstructs is a smoke test: newRootCmd() must produce a
// usable cobra command tree with our six subcommands attached. We
// don't run it (cobra's Execute hits os.Exit), only verify the shape.
func TestRootCmdConstructs(t *testing.T) {
	root := newRootCmd()
	if root == nil {
		t.Fatal("newRootCmd() returned nil")
	}
	if root.Use == "" {
		t.Errorf("root cmd has no Use string")
	}

	want := map[string]bool{
		"rotate": true,
		"verify": true,
		"status": true,
		"init":   true,
		"daemon": true,
		"keygen": true,
	}
	for _, sub := range root.Commands() {
		// strip optional argument signature; we only need the verb.
		name := strings.SplitN(sub.Use, " ", 2)[0]
		delete(want, name)
	}
	if len(want) != 0 {
		missing := make([]string, 0, len(want))
		for k := range want {
			missing = append(missing, k)
		}
		t.Errorf("root cmd is missing subcommands: %v", missing)
	}
}
