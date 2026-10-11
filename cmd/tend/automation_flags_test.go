//go:build unix

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestFlagsAfterTheTextAreStillFlags: Go's flag package stops at the first
// argument that is not a flag, which here is the target — so `tend agent
// prompt claude do it -wait` read "-wait" as part of the prompt and returned
// without waiting. A silent loss of a flag is worse than an error.
func TestFlagsAfterTheTextAreStillFlags(t *testing.T) {
	valued := map[string]bool{"wait": false, "timeout": true, "s": true}
	got := strings.Join(hoistFlags(
		[]string{"claude", "run", "the", "tests", "-wait", "-timeout", "30s"}, valued), " ")
	if want := "-wait -timeout 30s claude run the tests"; got != want {
		t.Errorf("hoistFlags = %q, want %q", got, want)
	}

	// A prompt may contain a word starting with a dash, as long as it is not
	// one of this command's own flags.
	got = strings.Join(hoistFlags([]string{"claude", "fix", "the", "-v", "flag"}, valued), " ")
	if want := "claude fix the -v flag"; got != want {
		t.Errorf("an unrelated dash argument was moved: %q", got)
	}

	// And "--" is how a prompt keeps a word that is one of them.
	got = strings.Join(hoistFlags([]string{"-wait", "claude", "--", "explain", "-timeout"}, valued), " ")
	if want := "-wait claude explain -timeout"; got != want {
		t.Errorf("hoistFlags after -- = %q, want %q", got, want)
	}
}

// TestAPISchemaPrintsTheSchema: `tend api schema -json` is herdr's
// `herdr api schema --json`, and needs no session. If it regresses, a
// plugin author has only the source to learn the socket from.
func TestAPISchemaPrintsTheSchema(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "api", "schema", "-json").Output()
	if err != nil {
		t.Fatalf("api schema -json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil || doc["title"] != "tend API" {
		t.Fatalf("not the schema: %v\n%.200s", err, out)
	}
	summary, err := exec.Command(bin, "api", "schema").Output()
	if err != nil || !strings.Contains(string(summary), "methods: ") || !strings.Contains(string(summary), "schema_version: 1") {
		t.Errorf("summary: %v\n%s", err, summary)
	}
}
