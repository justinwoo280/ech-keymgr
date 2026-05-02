package reloader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ----------------------------------------------------------------
// Factory + Noop
// ----------------------------------------------------------------

func TestNew_RejectsUnknownStrategy(t *testing.T) {
	if _, err := New(Config{Strategy: "bogus"}); err == nil {
		t.Errorf("expected error on unknown strategy")
	}
}

func TestNew_RequiresFieldsPerStrategy(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"signal w/o pid_file", Config{Strategy: StrategySignal}},
		{"exec w/o command", Config{Strategy: StrategyExec}},
		{"systemd w/o unit", Config{Strategy: StrategySystemd}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.cfg); err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}

func TestNoop_NeverErrors(t *testing.T) {
	n := Noop{}
	if n.Name() != "noop" {
		t.Errorf("Name")
	}
	if err := n.Reload(context.Background()); err != nil {
		t.Errorf("Reload: %v", err)
	}
}

// ----------------------------------------------------------------
// signal strategy
// ----------------------------------------------------------------

// TestSignal_SendsSIGUSR1ToOurselves and TestSignal_DefaultIsSIGHUP
// (the latter follows immediately) live in reloader_unix_test.go
// because they reference syscall.SIGUSR1 / syscall.SIGHUP which
// don't exist on Windows. They moved out wholesale; do not re-add
// them here.

func TestSignal_ReadsPID_RejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"non-numeric":  "abc\n",
		"negative":     "-1",
		"zero":         "0",
		"only-newline": "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			f := filepath.Join(dir, "pid")
			_ = os.WriteFile(f, []byte(body), 0o600)
			if _, err := readPID(f); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestSignal_PIDFileMissing(t *testing.T) {
	r, err := New(Config{Strategy: StrategySignal, PIDFile: "/nonexistent/path/pid", Signal: "SIGUSR2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background()); err == nil {
		t.Errorf("expected error on missing pid_file")
	}
}

func TestParseSignal_RejectsUnknown(t *testing.T) {
	if _, err := parseSignal("SIGFAKE"); err == nil {
		t.Errorf("expected error on SIGFAKE")
	}
}

// TestParseSignal_AcceptsBareName for SIGUSR1 lives in
// reloader_unix_test.go (Windows has no SIGUSR1). The Windows
// equivalent is in reloader_windows_test.go and asserts SIGTERM.

// ----------------------------------------------------------------
// exec strategy
// ----------------------------------------------------------------

// findTrueCommand returns a path to a binary that exits 0 with no
// output, suitable for the StrategyExec smoke test. The location
// differs across platforms: Linux ships /bin/true, macOS only ships
// /usr/bin/true, and on Windows we fall back to cmd /c exit 0 (and
// special-case it in callers because the path may contain spaces).
func findTrueCommand(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"/usr/bin/true", "/bin/true"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if path, err := exec.LookPath("true"); err == nil {
		return path
	}
	t.Skip("no `true` binary found on this platform")
	return ""
}

func TestExec_RunsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("StrategyExec uses POSIX-style command parsing; covered by reloader_windows_test.go")
	}
	r, err := New(Config{Strategy: StrategyExec, Command: findTrueCommand(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background()); err != nil {
		t.Errorf("Reload: %v", err)
	}
}

func TestExec_PropagatesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	r, _ := New(Config{Strategy: StrategyExec, Command: "/bin/false"})
	if err := r.Reload(context.Background()); err == nil {
		t.Errorf("expected error from /bin/false")
	}
}

func TestExec_RespectsContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	r, _ := New(Config{Strategy: StrategyExec, Command: findTrueCommand(t)})
	if err := r.Reload(ctx); err == nil {
		t.Errorf("expected ctx error")
	}
}

func TestExec_IncludesOutputInError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	r, _ := New(Config{
		Strategy: StrategyExec,
		Command:  "/bin/sh",
		Args:     []string{"-c", "echo MAGIC_TOKEN >&2; exit 7"},
	})
	err := r.Reload(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "MAGIC_TOKEN") {
		t.Errorf("error should include stderr, got: %v", err)
	}
}

// ----------------------------------------------------------------
// systemd strategy (factory only — we won't actually invoke systemctl)
// ----------------------------------------------------------------

func TestSystemd_FactoryOK(t *testing.T) {
	r, err := New(Config{Strategy: StrategySystemd, Unit: "nginx.service"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != "systemd" {
		t.Errorf("Name = %q", r.Name())
	}
}
