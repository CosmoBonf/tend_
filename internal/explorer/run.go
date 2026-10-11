package explorer

import (
	"context"
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/auth-com-br/tend/internal/vt"
)

// refreshEvery is how often the panel reads the disk and git again. Often
// enough that a file an agent just wrote shows up while you look; seldom
// enough that `git status` on a large repository is not running constantly.
const refreshEvery = 2 * time.Second

// Terminal modes the panel sets and puts back: the alternate screen, so the
// pane's history is left as it was; mouse clicks, the wheel and drags (a
// drag over the tree copies paths, so clicks alone are not enough), in SGR's
// encoding, which has no column limit; the cursor hidden.
const (
	enterScreen = "\x1b[?1049h\x1b[?1002h\x1b[?1006h\x1b[?25l"
	leaveScreen = "\x1b[?1006l\x1b[?1002l\x1b[?25h\x1b[?1049l"
)

// Run runs the explorer on the terminal until it is told to quit.
func Run(m *Model, in *os.File, out io.Writer) error {
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, state)
	_, _ = io.WriteString(out, enterScreen)
	defer io.WriteString(out, leaveScreen)

	input := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				input <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				close(input)
				return
			}
		}
	}()
	resized := make(chan struct{}, 1)
	stop := notifyResize(resized)
	defer stop()
	tick := time.NewTicker(refreshEvery)
	defer tick.Stop()

	painter := vt.NewPainter()
	var grid *vt.Grid
	var pending []byte
	draw := func() {
		cols, rows, err := term.GetSize(fd)
		if err != nil || cols <= 0 || rows <= 0 {
			cols, rows = 30, 20
		}
		if grid == nil || grid.Cols() != cols || grid.Rows() != rows {
			grid = vt.NewGrid(cols, rows, 0)
			painter.Invalidate()
		}
		x, y, visible := m.Draw(grid)
		_, _ = out.Write(painter.Paint(grid, x, y, visible))
	}

	// A search runs away from this loop, so typing is never held up by
	// git grep; a new one cancels the last, whose answer is stale anyway.
	results := make(chan SearchResult, 1)
	// A push or a pull the same way, so a slow remote holds nothing up.
	jobs := make(chan func(*Model), 1)
	cancel := func() {}
	defer func() { cancel() }()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()

	draw()
	for !m.Quit() {
		select {
		case <-poll.C:
			if job, ok := m.JobDue(); ok {
				go func() { jobs <- job() }()
			}
			req, ok := m.SearchDue(time.Now())
			if !ok {
				if m.jobRunning {
					break // the spinner is drawn; nothing else changed
				}
				continue
			}
			cancel()
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			go func() {
				r := RunSearch(ctx, req)
				if ctx.Err() == nil {
					results <- r
				}
			}()
		case r := <-results:
			m.ApplySearch(r)
		case apply := <-jobs:
			m.ApplyJob(apply)
		case data, ok := <-input:
			if !ok {
				return nil
			}
			events, rest := Parse(append(pending, data...))
			pending = rest
			for _, ev := range events {
				switch e := ev.(type) {
				case Key:
					m.Key(e)
				case Mouse:
					m.Mouse(e)
				}
			}
		case <-resized:
		case <-tick.C:
			// A new build installed: hand over to it (upgrade.go). The
			// deferred restores put the terminal back before the caller
			// starts it.
			if m.upgradeDue() {
				return ErrUpgrade
			}
			// Not while a diff or a file is up: reading it again would move
			// what is being read.
			if m.mode == modeList {
				m.Tick()
			}
		}
		draw()
	}
	return nil
}
