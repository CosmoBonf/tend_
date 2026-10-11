package web

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auth-com-br/tend/internal/proto"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeSession is a session with one agent (pane 3) and one shell (pane 4).
type fakeSession struct {
	mu     sync.Mutex
	input  []string
	pasted []string
	subs   [][]uint64
}

func (f *fakeSession) Snapshot() (proto.SessionSnapshot, error) {
	return proto.SessionSnapshot{
		Companies: []proto.CompanyInfo{{ID: 1, Name: "acme", Workspaces: []uint64{10}}},
		Workspaces: []proto.WorkspaceInfo{{ID: 10, Name: "api", Tabs: []proto.TabInfo{
			{ID: 20, Panes: []uint64{3, 4}},
		}}},
		Panes: []proto.PaneInfo{
			{ID: 3, Agent: "claude", State: "blocked", Title: "deploy", Running: true},
			{ID: 4, Running: true},
		},
	}, nil
}

func (f *fakeSession) SubscribePanes(p []uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, p)
	return nil
}

func (f *fakeSession) PaneScreen(pane uint64) (proto.PaneScreenResult, error) {
	return proto.PaneScreenResult{ANSI: "\x1b[HDo you want to proceed? 1. Yes", Cols: 40, Rows: 4}, nil
}

func (f *fakeSession) PastePane(pane uint64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pasted = append(f.pasted, text)
	return nil
}

func (f *fakeSession) SendInput(pane uint64, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, string(data))
	return nil
}

func (f *fakeSession) SleepPane(uint64) (proto.PaneSleepResult, error) {
	return proto.PaneSleepResult{Agent: "claude", Freed: 1 << 20}, nil
}

func (f *fakeSession) WakePane(uint64) error { return nil }

func paired(r *http.Request) *http.Request {
	r.AddCookie(&http.Cookie{Name: cookieName, Value: testToken})
	return r
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestNothingIsServedToADeviceThatIsNotPaired: the page drives agents that
// can run anything on the computer; without the token nothing of it is
// reachable. If it regresses, anyone on the Wi-Fi can message the agents.
func TestNothingIsServedToADeviceThatIsNotPaired(t *testing.T) {
	s := New(&fakeSession{}, testToken)
	for _, path := range []string{"/api/agents", "/api/events", "/app.js"} {
		if w := serve(s, httptest.NewRequest("GET", path, nil)); w.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d without the token", path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/prompt", strings.NewReader(`{"pane":3,"text":"rm -rf"}`))
	r.Header.Set("Content-Type", "application/json")
	if w := serve(s, r); w.Code != http.StatusUnauthorized {
		t.Errorf("a prompt was taken without the token: %d", w.Code)
	}
	wrong := httptest.NewRequest("GET", "/api/agents", nil)
	wrong.AddCookie(&http.Cookie{Name: cookieName, Value: strings.Repeat("0", 64)})
	if w := serve(s, wrong); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong cookie was taken: %d", w.Code)
	}
}

// TestTheAddressWithTheTokenPairsTheDevice: it sets the cookie and takes
// the token out of the address bar; a wrong token is refused, and ten of
// them in a minute stop the guessing.
func TestTheAddressWithTheTokenPairsTheDevice(t *testing.T) {
	s := New(&fakeSession{}, testToken)
	w := serve(s, httptest.NewRequest("GET", "/?t="+testToken, nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("pairing answered %d → %q", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Value != testToken || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", c)
	}
	for i := 0; i < maxFailures; i++ {
		if w := serve(s, httptest.NewRequest("GET", "/?t=nope", nil)); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong token %d answered %d", i, w.Code)
		}
	}
	if w := serve(s, httptest.NewRequest("GET", "/?t="+testToken, nil)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d wrong tokens, %d", maxFailures, w.Code)
	}
}

func post(path, body string) *http.Request {
	r := paired(httptest.NewRequest("POST", path, strings.NewReader(body)))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// TestAPhoneMessagesAgentsAndNothingElse: a message reaches an agent as a
// paste and an Enter; a shell, a key outside the list, a form post and a
// post from another site are refused. If it regresses, the phone is a
// remote shell.
func TestAPhoneMessagesAgentsAndNothingElse(t *testing.T) {
	f := &fakeSession{}
	s := New(f, testToken)
	if w := serve(s, post("/api/prompt", `{"pane":3,"text":"ship it"}`)); w.Code != http.StatusOK {
		t.Fatalf("prompt: %d %s", w.Code, w.Body)
	}
	if len(f.pasted) != 1 || f.pasted[0] != "ship it" || len(f.input) != 1 || f.input[0] != "\r" {
		t.Fatalf("pasted %q, input %q", f.pasted, f.input)
	}
	if w := serve(s, post("/api/key", `{"pane":3,"key":"1"}`)); w.Code != http.StatusOK || f.input[1] != "1" {
		t.Fatalf("key 1: %d", w.Code)
	}
	for _, c := range []struct{ path, body string }{
		{"/api/prompt", `{"pane":4,"text":"rm -rf ~"}`}, // a shell
		{"/api/key", `{"pane":3,"key":"ctrl-c"}`},       // not a key the page sends
		{"/api/key", `{"pane":3,"key":"rm -rf ~\r"}`},
	} {
		if w := serve(s, post(c.path, c.body)); w.Code == http.StatusOK {
			t.Errorf("%s %s was taken", c.path, c.body)
		}
	}
	form := paired(httptest.NewRequest("POST", "/api/prompt", strings.NewReader("pane=3&text=x")))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := serve(s, form); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a form post answered %d", w.Code)
	}
	other := post("/api/prompt", `{"pane":3,"text":"x"}`)
	other.Header.Set("Origin", "https://evil.example")
	if w := serve(s, other); w.Code != http.StatusForbidden {
		t.Errorf("another site's post answered %d", w.Code)
	}
	if len(f.pasted) != 1 {
		t.Fatalf("a refused request reached the session: %q", f.pasted)
	}
}

// TestTheStreamCarriesTheAgentsAndTheShownScreen: the page's one stream
// starts with the list and, for the pane it shows, its screen as HTML.
func TestTheStreamCarriesTheAgentsAndTheShownScreen(t *testing.T) {
	f := &fakeSession{}
	s := New(f, testToken)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events?pane=3", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: testToken})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var got []string
	for sc.Scan() && len(got) < 4 {
		if line := sc.Text(); line != "" {
			got = append(got, line)
		}
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"event: agents", `"agent":"claude"`, `"company":"acme"`, "event: screen", "Do you want to proceed? 1. Yes"} {
		if !strings.Contains(all, want) {
			t.Fatalf("%q missing from:\n%s", want, all)
		}
	}
	if strings.Contains(all, `"pane":4`) {
		t.Fatalf("a shell was listed as an agent:\n%s", all)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.subs) == 0 || len(f.subs[0]) != 1 || f.subs[0][0] != 3 {
		t.Fatalf("subscriptions %v", f.subs)
	}
	// A pane that is not an agent's is not streamed either.
	if w := serve(s, paired(httptest.NewRequest("GET", "/api/events?pane=4", nil))); w.Code != http.StatusForbidden {
		t.Fatalf("a shell's screen answered %d", w.Code)
	}
}

// TestAgentsAreListedWhereTheyAreWhatNeedsYouFirst: asleep, exited and
// done read as such, and within a company the one asking comes first.
func TestAgentsAreListedWhereTheyAreWhatNeedsYouFirst(t *testing.T) {
	snap := proto.SessionSnapshot{
		Workspaces: []proto.WorkspaceInfo{{ID: 1, Tabs: []proto.TabInfo{{Panes: []uint64{1, 2, 3, 4}}}}},
		Panes: []proto.PaneInfo{
			{ID: 1, Agent: "claude", State: "idle", Dormant: true, Running: true},
			{ID: 2, Agent: "codex", State: "idle", Done: true, Running: true},
			{ID: 3, Agent: "claude", State: "blocked", Running: true},
			{ID: 4, Agent: "dsh", State: "idle"},
		},
	}
	got := Agents(snap)
	order := []uint64{3, 2, 1, 4}
	for i, a := range got {
		if a.Pane != order[i] {
			t.Fatalf("order %+v", got)
		}
	}
	if got[2].State != "asleep" || got[3].State != "exited" || got[0].Space != "space 1" {
		t.Fatalf("states %+v", got)
	}
}
