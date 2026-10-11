package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A login and password are a second way to pair a phone, besides the
// address with the token: typed on the phone, where a 64-character address
// is not. The password is not kept — only a PBKDF2-SHA256 hash of it with a
// random salt, readable by its owner alone — and a right one sets the same
// cookie the token's address sets.

// pbkdf2Iterations is OWASP's figure for PBKDF2-HMAC-SHA256 (2023): slow
// enough to make guessing from a stolen file costly, quick enough not to be
// felt at a login.
const pbkdf2Iterations = 600_000

// Credentials are a login and its password's hash.
type Credentials struct {
	User string `json:"user"`
	Salt []byte `json:"salt"`
	Hash []byte `json:"hash"`
	Iter int    `json:"iter"`
}

// PasswordPath is where a session's login is kept.
func PasswordPath(stateDir, session string) string {
	return filepath.Join(stateDir, "web-"+session+".login")
}

// ErrNoLogin is a session with no login set: `tend web -set-password`.
var ErrNoLogin = errors.New("no login is set; `tend web -set-password` sets one")

// NewCredentials hashes a password for a login.
func NewCredentials(user, password string) (Credentials, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return Credentials{}, errors.New("the login is empty")
	}
	if len(password) < 8 {
		return Credentials{}, errors.New("the password needs at least 8 characters")
	}
	c := Credentials{User: user, Salt: make([]byte, 16), Iter: pbkdf2Iterations}
	if _, err := rand.Read(c.Salt); err != nil {
		return Credentials{}, err
	}
	hash, err := pbkdf2.Key(sha256.New, password, c.Salt, c.Iter, 32)
	if err != nil {
		return Credentials{}, err
	}
	c.Hash = hash
	return c, nil
}

// Check says whether a login and password are these, in time that does not
// tell how much of either was right.
func (c Credentials) Check(user, password string) bool {
	if c.Iter <= 0 || len(c.Hash) == 0 {
		return false
	}
	hash, err := pbkdf2.Key(sha256.New, password, c.Salt, c.Iter, len(c.Hash))
	if err != nil {
		return false
	}
	okUser := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(user)), []byte(c.User))
	okHash := subtle.ConstantTimeCompare(hash, c.Hash)
	return okUser&okHash == 1
}

// SaveCredentials writes a login, 0600, in one rename.
func SaveCredentials(path string, c Credentials) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".web-login-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadCredentials reads a login; ErrNoLogin when none is set.
func LoadCredentials(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Credentials{}, ErrNoLogin
	}
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, err
	}
	return c, nil
}
