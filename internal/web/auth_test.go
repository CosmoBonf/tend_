package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAPasswordIsKeptAsAHashAndChecked: the file holds no password, only
// what checks one; a wrong login or password does not pass.
func TestAPasswordIsKeptAsAHashAndChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-default.login")
	c, err := NewCredentials("akira", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(path, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "correct horse") {
		t.Fatal("the password itself was written")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	back, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Check("akira", "correct horse") {
		t.Fatal("the right password was refused")
	}
	for _, wrong := range [][2]string{{"akira", "correct hors"}, {"bob", "correct horse"}, {"", ""}} {
		if back.Check(wrong[0], wrong[1]) {
			t.Fatalf("%q passed", wrong)
		}
	}
	if _, err := NewCredentials("akira", "short"); err == nil {
		t.Fatal("a seven-character password was taken")
	}
	if _, err := LoadCredentials(filepath.Join(t.TempDir(), "none")); err != ErrNoLogin {
		t.Fatalf("no file: %v", err)
	}
}

func signIn(s *Server, user, password string) *httptest.ResponseRecorder {
	form := url.Values{"user": {user}, "password": {password}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serve(s, r)
}

// TestAPhoneSignsInWithTheLogin: an unpaired phone gets the login page; the
// right login pairs it, a wrong one does not, and sign out unpairs it. If it
// regresses, the login lets anyone in, or lets nobody.
func TestAPhoneSignsInWithTheLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-default.login")
	s := New(&fakeSession{}, testToken).WithLogin(path)
	w := serve(s, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), "tend web -set-password") || strings.Contains(w.Body.String(), "/app.js") {
		t.Fatalf("no login set: %d\n%s", w.Code, w.Body)
	}
	c, _ := NewCredentials("akira", "correct horse")
	if err := SaveCredentials(path, c); err != nil {
		t.Fatal(err)
	}
	w = serve(s, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), `action="/login"`) {
		t.Fatalf("the login page:\n%s", w.Body)
	}
	w = signIn(s, "akira", "wrong password")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?e=1" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("a wrong password: %d %q %v", w.Code, w.Header().Get("Location"), w.Result().Cookies())
	}
	// As a browser sends it: the page's own origin.
	form := url.Values{"user": {"akira"}, "password": {"correct horse"}}
	r := httptest.NewRequest("POST", "http://192.168.0.10:7777/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://192.168.0.10:7777")
	w = serve(s, r)
	if w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("referrer policy %q: with no-referrer a browser sends Origin: null", w.Header().Get("Referrer-Policy"))
	}
	cookies := w.Result().Cookies()
	if w.Header().Get("Location") != "/" || len(cookies) != 1 || cookies[0].Value != testToken {
		t.Fatalf("the right password: %q %v", w.Header().Get("Location"), cookies)
	}
	out := paired(httptest.NewRequest("POST", "/logout", nil))
	if w := serve(s, out); w.Code != http.StatusOK || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatalf("sign out: %d %v", w.Code, w.Result().Cookies())
	}
	foreign := httptest.NewRequest("POST", "/login", strings.NewReader("user=akira&password=correct+horse"))
	foreign.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	foreign.Header.Set("Origin", "https://evil.example")
	if w := serve(s, foreign); w.Code != http.StatusForbidden {
		t.Fatalf("a sign-in from another site: %d", w.Code)
	}
}
