package explorer

import (
	"os"
	"time"
)

// keyReader reads the terminal for the preview, and stops reading while
// another program has it. A goroutine left in a read would take the first
// keys typed into the editor the preview runs, so a read is only begun when
// the terminal has something to read (waitReadable), and a pause is answered
// between reads.
type keyReader struct {
	in  *os.File
	out chan []byte
	// hold asks the reader to stop; it closes the channel it is sent once
	// it has, and waits on unhold to go on.
	hold   chan chan struct{}
	unhold chan struct{}
}

// pollEvery bounds how long a pause waits for the reader to notice it.
const pollEvery = 50 * time.Millisecond

func newKeyReader(in *os.File) *keyReader {
	return &keyReader{in: in, out: make(chan []byte, 16), hold: make(chan chan struct{}), unhold: make(chan struct{})}
}

func (r *keyReader) run() {
	buf := make([]byte, 4096)
	for {
		select {
		case stopped := <-r.hold:
			close(stopped)
			<-r.unhold
			continue
		default:
		}
		ready, err := waitReadable(r.in, pollEvery)
		if err != nil {
			close(r.out)
			return
		}
		if !ready {
			continue
		}
		n, err := r.in.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			select {
			case r.out <- data:
			case stopped := <-r.hold:
				// Paused with keys unread by the preview: they were typed
				// for it, before the editor, and are dropped with it.
				close(stopped)
				<-r.unhold
			}
		}
		if err != nil {
			close(r.out)
			return
		}
	}
}

// pause returns once the reader is not reading, dropping what it had read
// and not yet handed over.
func (r *keyReader) pause() {
	stopped := make(chan struct{})
	for sent := false; !sent; {
		select {
		case r.hold <- stopped:
			sent = true
		case <-r.out:
		}
	}
	<-stopped
	for {
		select {
		case <-r.out:
		default:
			return
		}
	}
}

// resume lets the reader read again.
func (r *keyReader) resume() { r.unhold <- struct{}{} }
