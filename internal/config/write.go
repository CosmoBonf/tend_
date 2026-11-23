package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Changing one setting from inside tend must not rewrite the settings file.
//
// A file the user wrote is a file they will read again: their comments, the
// order they put things in, the sections they left empty on purpose. Marshalling
// the whole configuration back would lose all of it, so a change is an edit —
// the one line that holds the key, replaced in place, and everything else left
// exactly as it was. herdr does this too (`config/io.rs::upsert_section_raw`),
// and this is that function.

// ErrNoConfig means there is no settings file to edit yet.
var ErrNoConfig = errors.New("config: no settings file")

// Set writes one key in one section of the settings file.
//
// value is TOML as it should appear after the equals sign: Quote for a string,
// "true" for a boolean. A section that is not in the file is added at the end;
// a key that is not in its section is added to it.
func Set(section, key, value string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// No file yet: start from the documented default, so what is written
		// lands in a file that explains itself rather than a bare fragment.
		content = []byte(Example)
	}

	updated := Upsert(string(content), section, key, value)
	if _, err := parse(updated, path); err != nil {
		// The edit produced something tend itself would refuse to start with.
		// Writing it would lock the user out of their own settings.
		return fmt.Errorf("config: refusing to write %s.%s: %w", section, key, err)
	}
	return writeFile(path, updated)
}

// SetTopLevel writes a key outside every table, as Set writes one inside
// one: herdr's upsert_top_level_bool, for `onboarding`.
func SetTopLevel(key, value string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		content = []byte(Example)
	}
	updated := UpsertTopLevel(string(content), key, value)
	if _, err := parse(updated, path); err != nil {
		return fmt.Errorf("config: refusing to write %s: %w", key, err)
	}
	return writeFile(path, updated)
}

// SetDefaultAccount makes an account the one an agent starts on, or the
// agent's own login again when name is empty, kept as the top-level
// default_accounts table.
func SetDefaultAccount(agent, name string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		content = []byte(Example)
	}
	current, err := parse(string(content), path)
	if err != nil {
		return err
	}
	defaults := map[string]string{}
	for k, v := range current.DefaultAccounts {
		defaults[k] = v
	}
	if name == "" {
		delete(defaults, agent)
	} else {
		defaults[agent] = name
	}
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+" = "+strconv.Quote(defaults[k]))
	}
	value := "{ " + strings.Join(parts, ", ") + " }"
	if len(parts) == 0 {
		value = "{}"
	}
	updated := UpsertTopLevel(string(content), "default_accounts", value)
	if _, err := parse(updated, path); err != nil {
		return fmt.Errorf("config: refusing to write default_accounts: %w", err)
	}
	return writeFile(path, updated)
}

// AddAccount writes one more [[accounts]] at the end of the settings file,
// where a table of its own cannot change what any key above it means. A
// name the agent already has an account by is refused rather than doubled.
func AddAccount(a Account) error {
	path, err := Path()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		content = []byte(Example)
	}
	if current, err := parse(string(content), path); err == nil {
		for _, have := range current.AccountsFor(a.Agent) {
			if have.Name == a.Name {
				return fmt.Errorf("%s already has an account named %q", a.Agent, a.Name)
			}
		}
	}
	updated := strings.TrimRight(string(content), "\n") + "\n\n[[accounts]]\n" +
		"name = " + strconv.Quote(a.Name) + "\n" +
		"agent = " + strconv.Quote(a.Agent) + "\n" +
		"home = " + strconv.Quote(a.Home) + "\n"
	if _, err := parse(updated, path); err != nil {
		return fmt.Errorf("config: refusing to write the account: %w", err)
	}
	return writeFile(path, updated)
}

// UpsertTopLevel returns content with key set to value before the first
// table: in place when it is already there, otherwise as the first line.
// A commented-out line is not the key, and is left as it is.
func UpsertTopLevel(content, key, value string) string {
	replacement := key + " = " + value
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			break
		}
		if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") {
			lines[i] = replacement
			return strings.Join(lines, "\n") + "\n"
		}
	}
	if strings.TrimSpace(content) == "" {
		return replacement + "\n"
	}
	return replacement + "\n" + strings.TrimRight(content, "\n") + "\n"
}

// Upsert returns content with section.key set to value, leaving the rest of
// the file — comments, order, spacing — as it was.
func Upsert(content, section, key, value string) string {
	header := "[" + section + "]"
	assignment := key + " = " + value

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := make([]string, 0, len(lines)+3)
	found, inserted := false, false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) != header {
			out = append(out, line)
			continue
		}

		found = true
		out = append(out, line)
		// Walk this section until the next one, replacing the key if it is
		// there and adding it at the end of the section if it is not.
		for i++; i < len(lines); i++ {
			current := strings.TrimSpace(lines[i])
			if strings.HasPrefix(current, "[") && strings.HasSuffix(current, "]") {
				if !inserted {
					out = append(out, assignment)
					inserted = true
				}
				i--
				break
			}
			if isAssignment(current, key) {
				out = append(out, assignment)
				inserted = true
				continue
			}
			out = append(out, lines[i])
		}
	}

	switch {
	case !found:
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, header, assignment)
	case !inserted:
		out = append(out, assignment)
	}
	return strings.Join(out, "\n") + "\n"
}

// RemoveSection deletes one table of the settings file — its header and
// everything up to the next table — and nothing else. A table that is not
// there leaves the file as it is.
func RemoveSection(section string) error {
	path, err := Path()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := DropSection(string(content), section)
	if updated == string(content) {
		return nil
	}
	if _, err := parse(updated, path); err != nil {
		return fmt.Errorf("config: refusing to remove [%s]: %w", section, err)
	}
	return writeFile(path, updated)
}

// DropSection returns content without the table named section, and
// without the blank lines that were left before it.
func DropSection(content, section string) string {
	header := "[" + section + "]"
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != header {
			out = append(out, lines[i])
			continue
		}
		for i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if strings.HasPrefix(next, "[") && strings.HasSuffix(next, "]") {
				break
			}
			i++
		}
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		if i+1 < len(lines) && len(out) > 0 {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// isAssignment reports whether a line assigns key, allowing for the spacing
// people actually write: "key = x", "key=x", "key\t= x".
func isAssignment(line, key string) bool {
	rest, ok := strings.CutPrefix(line, key)
	if !ok {
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	return strings.HasPrefix(rest, "=")
}

// Quote renders a string as TOML.
func Quote(s string) string { return strconv.Quote(s) }

// InlineTable is a TOML inline table of strings, its keys in order so the
// line written does not change while the table does not.
func InlineTable(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, strconv.Quote(k)+" = "+strconv.Quote(m[k]))
	}
	if len(parts) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// Bool renders a boolean as TOML.
func Bool(b bool) string { return strconv.FormatBool(b) }

// writeFile replaces the settings file in one step, so a crash halfway cannot
// leave a settings file that will not parse.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tend-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}
