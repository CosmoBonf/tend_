//go:build !unix

package explorer

import (
	"os"
	"time"
)

// canEditInPlace is false off unix: with no poll, a read cannot be kept
// from taking the editor's keys, so o opens the editor in a tab instead.
const canEditInPlace = false

// waitReadable cannot wait here; the read that follows blocks instead.
func waitReadable(*os.File, time.Duration) (bool, error) { return true, nil }

func holdInterrupts() (restore func()) { return func() {} }
