// Package firefox reads a site's cookies from the user's own Firefox profile, so
// the CLI can ride on the browser's live session — including the aws-waf-token
// clearance that Firefox keeps fresh while a shop tab is open — instead of a
// one-off import that goes stale. It shells out to the sqlite3 binary (no cgo,
// no bundled SQLite) and reads a copy of the database, since Firefox holds a
// lock on the original while it runs.
package firefox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNoSQLite is returned when the sqlite3 binary isn't on PATH.
var ErrNoSQLite = errors.New("the sqlite3 command is required to read Firefox cookies (install it, e.g. `sudo apt install sqlite3`)")

// rootDirs lists where Firefox keeps profiles.ini, per install flavour.
func rootDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{filepath.Join(home, "Library", "Application Support", "Firefox")}
	case "windows":
		return []string{filepath.Join(os.Getenv("APPDATA"), "Mozilla", "Firefox")}
	default:
		return []string{
			filepath.Join(home, ".mozilla", "firefox"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		}
	}
}

// DefaultProfile finds the profile Firefox starts with: the [Install…] section's
// Default= (what modern Firefox actually uses), else the [Profile…] marked
// Default=1, else the only profile. Directories without cookies.sqlite are skipped.
func DefaultProfile() (string, error) {
	var tried []string
	for _, root := range rootDirs() {
		ini, err := os.ReadFile(filepath.Join(root, "profiles.ini"))
		if err != nil {
			continue
		}
		tried = append(tried, root)
		for _, p := range profileCandidates(string(ini)) {
			dir := p
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(root, filepath.FromSlash(p))
			}
			if _, err := os.Stat(filepath.Join(dir, "cookies.sqlite")); err == nil {
				return dir, nil
			}
		}
	}
	if len(tried) == 0 {
		return "", fmt.Errorf("no Firefox profiles.ini found (looked in %s) — pass --profile <dir>", strings.Join(rootDirs(), ", "))
	}
	return "", fmt.Errorf("no Firefox profile with cookies.sqlite under %s — pass --profile <dir>", strings.Join(tried, ", "))
}

// profileCandidates returns profile paths from profiles.ini, best first.
func profileCandidates(ini string) []string {
	type section struct {
		name string
		kv   map[string]string
	}
	var secs []section
	for _, line := range strings.Split(ini, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			secs = append(secs, section{name: line[1 : len(line)-1], kv: map[string]string{}})
		case len(secs) > 0:
			if k, v, ok := strings.Cut(line, "="); ok {
				secs[len(secs)-1].kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	var install, marked, rest []string
	for _, s := range secs {
		switch {
		case strings.HasPrefix(s.name, "Install") && s.kv["Default"] != "":
			install = append(install, s.kv["Default"])
		case strings.HasPrefix(s.name, "Profile") && s.kv["Path"] != "":
			if s.kv["Default"] == "1" {
				marked = append(marked, s.kv["Path"])
			} else {
				rest = append(rest, s.kv["Path"])
			}
		}
	}
	return append(append(install, marked...), rest...)
}

// Cookies returns the unexpired cookies Firefox holds for hosts ending in
// hostSuffix (e.g. "compraonline.alcampo.es"), name → value.
func Cookies(profileDir, hostSuffix string) (map[string]string, error) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil, ErrNoSQLite
	}
	tmp, err := os.MkdirTemp("", "alcampo-ff-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// Copy the DB with its write-ahead log: recent cookie updates (a refreshed
	// aws-waf-token) often live only in the -wal until Firefox checkpoints.
	src := filepath.Join(profileDir, "cookies.sqlite")
	db := filepath.Join(tmp, "cookies.sqlite")
	if err := copyFile(src, db); err != nil {
		return nil, fmt.Errorf("read Firefox cookies: %w", err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		_ = copyFile(src+ext, db+ext) // absent when Firefox isn't running
	}
	return query(db, hostSuffix, time.Now())
}

// query runs the cookie SELECT against a (copied) cookies.sqlite. Firefox has
// stored expiry in seconds and, in newer versions, milliseconds; both are
// handled, and session cookies (expiry 0) are kept.
func query(db, hostSuffix string, now time.Time) (map[string]string, error) {
	host := strings.ReplaceAll(strings.TrimPrefix(hostSuffix, "."), "'", "")
	sql := fmt.Sprintf(`SELECT name, value, expiry FROM moz_cookies
		WHERE host = '%[1]s' OR host = '.%[1]s' OR host LIKE '%%.%[1]s'
		ORDER BY lastAccessed;`, host)
	out, err := exec.Command("sqlite3", "-readonly", "-json", db, sql).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("sqlite3: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var rows []struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Expiry int64  `json:"expiry"`
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("parse sqlite3 output: %w", err)
		}
	}
	cookies := map[string]string{}
	for _, r := range rows {
		exp := r.Expiry
		if exp > 1e11 { // milliseconds
			exp /= 1000
		}
		if exp != 0 && time.Unix(exp, 0).Before(now) {
			continue
		}
		cookies[r.Name] = r.Value // ordered by lastAccessed: freshest wins
	}
	return cookies, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
