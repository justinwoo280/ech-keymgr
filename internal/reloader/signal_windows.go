//go:build windows

package reloader

import "syscall"

// defaultReloadSignal on Windows is SIGTERM. Windows has no real
// equivalent of SIGHUP — sending SIGTERM via os.Process.Signal
// effectively terminates the target. Operators on Windows should
// generally prefer the `exec` reload strategy:
//
//	reload:
//	  strategy: exec
//	  command: C:\nginx\nginx.exe -s reload
//
// The signal strategy still compiles on Windows so configs are
// portable across hosts; calling it just routes through the very
// limited Windows signal model.
const defaultReloadSignal = syscall.SIGTERM

// signalTable on Windows only lists signals that Go's syscall
// package actually defines on the platform. SIGHUP/SIGUSR1/SIGUSR2/
// SIGQUIT do not exist; trying to use them yields a friendlier
// error pointing operators at the exec strategy.
var signalTable = map[string]syscall.Signal{
	"SIGTERM": syscall.SIGTERM,
	"SIGINT":  syscall.SIGINT,
	"SIGKILL": syscall.SIGKILL,
}
