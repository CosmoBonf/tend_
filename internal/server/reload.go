package server

import (
	"time"

	"github.com/auth-com-br/tend/internal/config"
	"github.com/auth-com-br/tend/internal/proto"
	"github.com/auth-com-br/tend/internal/pty"
)

// Settings that are the server's — how often panes are examined, how much
// scrollback a pane keeps, where new panes start — are read once when it
// starts. A change to them used to mean restarting the server, which closes
// the panes: a heavy price for a number in a file. herdr reloads
// (`server.reload_config`), and so does this.
//
// What a reload can and cannot do is worth stating. The detection tick is a
// ticker, and it is replaced. Scrollback is how big a terminal's history is,
// and it applies to panes opened from now on: changing it under a running
// terminal would mean rebuilding its ring and throwing away the part that no
// longer fits, which is not what somebody editing a number expects.

// ReloadFromFile is what the reload method calls: the server re-reads the
// settings itself, because it is the process that has to live with them and a
// client on another machine may not even have the file.
func (s *Server) ReloadFromFile() proto.ReloadResult {
	path, err := config.Path()
	result := proto.ReloadResult{Path: path}
	if err != nil {
		result.Err = err.Error()
		return result
	}
	cfg, err := config.Load()
	if err != nil {
		// The file is there and wrong. Saying so is the whole value of a
		// reload: the alternative is a setting that silently never applied.
		result.Err = err.Error()
		return result
	}
	interval, _ := cfg.DetectInterval()
	sleepAfter, _ := cfg.SleepAfter()
	result.Changed = s.Reload(Reloadable{
		DetectInterval: interval,
		Scrollback:     cfg.Scrollback(),
		Sleep:          &SleepSettings{Restored: cfg.SleepRestored(), After: sleepAfter},
	})
	if s.tabBarDiffers(cfg.UI.TabBarRight, cfg.UI.TabBarSeparator) {
		// Reconfigured only when it changed: doing it anyway would restart
		// every command and blank the bar for a reload about something else.
		s.ConfigureTabBar(cfg.UI.TabBarRight, cfg.UI.TabBarSeparator)
		result.Changed = append(result.Changed, "tab bar")
	}
	return result
}

// Reloadable is what a running server will take from a re-read settings file.
type Reloadable struct {
	DetectInterval time.Duration
	Scrollback     int
	DefaultSize    pty.Size
	// Sleep is the sleeping agents' settings; nil leaves them. A pointer,
	// since "never" is a zero that has to be told from "not said".
	Sleep *SleepSettings
}

// SleepSettings are [agents]' sleep_restored and sleep_after.
type SleepSettings struct {
	Restored bool
	After    time.Duration
}

// Reload applies settings to a running server and says what changed.
func (s *Server) Reload(next Reloadable) []string {
	var changed []string

	s.mu.Lock()
	if next.DetectInterval > 0 && next.DetectInterval != s.cfg.DetectInterval {
		s.cfg.DetectInterval = next.DetectInterval
		changed = append(changed, "detection interval")
	}
	if next.Scrollback > 0 && next.Scrollback != s.cfg.Scrollback {
		s.cfg.Scrollback = next.Scrollback
		changed = append(changed, "scrollback (panes opened from now on)")
	}
	if next.DefaultSize.Valid() && next.DefaultSize != s.cfg.DefaultSize {
		s.cfg.DefaultSize = next.DefaultSize
		changed = append(changed, "default pane size")
	}
	if next.Sleep != nil {
		if next.Sleep.Restored != s.cfg.SleepRestored {
			s.cfg.SleepRestored = next.Sleep.Restored
			changed = append(changed, "agents asleep after a restart")
		}
		if next.Sleep.After != s.cfg.SleepAfter {
			s.cfg.SleepAfter = next.Sleep.After
			changed = append(changed, "agents' sleep after idling")
		}
	}
	interval := s.cfg.DetectInterval
	s.mu.Unlock()

	select {
	case s.retick <- interval:
	default:
		// The loop is busy with a round of detection and will read the
		// interval when it comes back. One pending change is enough: the
		// newest is the one that counts.
	}
	return changed
}
