//go:build unix

package reloader

import "syscall"

// defaultReloadSignal is what an empty signal: name in YAML resolves
// to. SIGHUP is the right answer for nginx, apache, lighttpd, etc.
const defaultReloadSignal = syscall.SIGHUP

// signalTable lists every named signal the user is allowed to ask
// for. The keys are uppercase + SIG-prefixed because parseSignal
// normalises input that way before lookup.
var signalTable = map[string]syscall.Signal{
	"SIGHUP":  syscall.SIGHUP,
	"SIGUSR1": syscall.SIGUSR1,
	"SIGUSR2": syscall.SIGUSR2,
	"SIGTERM": syscall.SIGTERM,
	"SIGQUIT": syscall.SIGQUIT,
	"SIGINT":  syscall.SIGINT,
}
