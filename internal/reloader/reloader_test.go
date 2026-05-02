package reloader

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
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

func TestSignal_SendsSIGUSR1ToOurselves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals not portable on Windows")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Strategy: StrategySignal, PIDFile: pidFile, Signal: "SIGUSR1"})
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	defer signal.Stop(ch)

	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		// got it
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive SIGUSR1 within 2s")
	}
}

func TestSignal_DefaultIsSIGHUP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals not portable on Windows")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600)
	r, err := New(Config{Strategy: StrategySignal, PIDFile: pidFile, Signal: ""})
	if err != nil {
		t.Fatal(err)
	}
	sr := r.(*signalReloader)
	if sr.sig != syscall.SIGHUP {
		t.Errorf("default sig = %v, want SIGHUP", sr.sig)
	}
}

func TestSignal_ReadsPID_RejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"non-numeric":   "abc\n",
		"negative":      "-1",
		"zero":          "0",
		"only-newline":  "\n",
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

func TestParseSignal_AcceptsBareName(t *testing.T) {
	if got, err := parseSignal("usr1"); err != nil || got != syscall.SIGUSR1 {
		t.Errorf("parse usr1 = %v, %v", got, err)
	}
}

// ----------------------------------------------------------------
// exec strategy
// ----------------------------------------------------------------

func TestExec_RunsCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/bin/true unavailable on Windows")
	}
	r, err := New(Config{Strategy: StrategyExec, Command: "/bin/true"})
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
	r, _ := New(Config{Strategy: StrategyExec, Command: "/bin/true"})
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
