//go:build unix

package explorer

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// canEditInPlace: poll says when the terminal can be read, so the preview
// can stop reading it and hand it to an editor.
const canEditInPlace = true

// waitReadable reports whether f has something to read within d.
func waitReadable(f *os.File, d time.Duration) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(d/time.Millisecond))
	if errors.Is(err, unix.EINTR) {
		return false, nil // a resize, most often
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// holdInterrupts keeps ctrl+c and ctrl+\ typed into the editor from ending
// the preview, which is in the same process group and so is sent them too
// by an editor that leaves the terminal's signals on.
func holdInterrupts() (restore func()) {
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGQUIT)
	return func() { signal.Stop(sig) }
}
