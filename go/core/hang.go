package core

import (
	"os"
	"runtime"
	"runtime/debug"

	"golang.org/x/sys/unix"
)

// Hang and crash reports. A hang: the app's watchdog asks for every goroutine's stack (who waits on what).
// A crash: Go prints a panic's or fatal error's trace to fd 2 and the process dies — on Android fd 2 goes
// nowhere, so it is pointed at a file the app reads and sends on the next start.

// GoStacks: the stacks of all goroutines.
func GoStacks() string {
	buf := make([]byte, 4<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// RedirectStderr points fd 2 at [path] (appending) and makes a crash dump every goroutine.
func RedirectStderr(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	debug.SetTraceback("all")
	return unix.Dup3(int(f.Fd()), 2, 0)
}
