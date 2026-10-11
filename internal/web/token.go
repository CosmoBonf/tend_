package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The token is what pairs a phone: the URL `tend web` prints carries it
// once, and the page keeps it as a cookie. It is the whole of the
// authentication, so it is long, random, and kept readable by its owner
// alone (0600), beside the session's state.

// TokenPath is where a session's token is kept.
func TokenPath(stateDir, session string) string {
	return filepath.Join(stateDir, "web-"+session+".token")
}

// LoadToken reads the token at path, making one when there is none or when
// fresh is set — which unpairs every phone paired with the old one.
func LoadToken(path string, fresh bool) (string, error) {
	if !fresh {
		data, err := os.ReadFile(path)
		if err == nil {
			if t := strings.TrimSpace(string(data)); len(t) >= 32 {
				return t, nil
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".web-token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return token, os.Rename(tmp.Name(), path)
}
