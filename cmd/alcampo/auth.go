package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/alvaroborras/alcampo-cli/internal/client"
	"github.com/alvaroborras/alcampo-cli/internal/config"
	"github.com/alvaroborras/alcampo-cli/internal/firefox"
)

// ---- session commands ----
//
// Alcampo's shop authenticates with a cookie session, not a token: the
// global_sid cookie identifies the visitor (and owns the cart), and logging in
// in a browser upgrades that same session. Login itself sits behind an AWS WAF
// captcha, so the CLI never logs in — it imports the browser's cookies instead.

// authedClient builds a client and confirms its session is logged in, for the
// commands (whoami, checkout) that need a customer account.
func authedClient(cf *common) (*client.Client, error) {
	cl := newClient(cf)
	if !cl.HasSession() {
		return nil, fmt.Errorf("not logged in: log in at %s in a browser, then `alcampo import-curl` or `alcampo import-har`", client.BaseURL)
	}
	ok, err := cl.LoggedIn()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("the cached session is anonymous (or expired): log in at %s in a browser, then re-import it with `alcampo import-curl` / `alcampo import-har`", client.BaseURL)
	}
	return cl, nil
}

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	cf := addCommon(fs)
	_ = fs.Parse(reorderArgs(fs, args))
	cl := newClient(cf)
	if !cl.HasSession() {
		fmt.Println("no session yet — anonymous (a session is created on first use; import a logged-in one with `alcampo import-curl`)")
		return nil
	}
	cu, raw, err := cl.Me()
	if err != nil {
		if ok, lerr := cl.LoggedIn(); lerr == nil && !ok {
			if cf.jsonOut {
				return emitJSON(map[string]any{"logged_in": false})
			}
			fmt.Println("anonymous session (not logged in) — cart commands work; checkout needs a logged-in session")
			return nil
		}
		return err
	}
	if cf.jsonOut {
		return emitRaw(raw)
	}
	fmt.Printf("ok — logged in as %s %s (customer id=%s)\n", orDefault(strings.TrimSpace(cu.Details.FullName), "?"), angle(cu.Details.Email), cu.ID)
	return nil
}

func angle(s string) string {
	if s == "" {
		return ""
	}
	return "<" + s + ">"
}

// import-curl extraction patterns (DevTools → Copy as cURL (bash)).
var (
	reCookieB = regexp.MustCompile(`(?:^|\s)(?:-b|--cookie)\s+'([^']*)'`)
	reCookieH = regexp.MustCompile(`(?i)(?:-H|--header)\s+'cookie:\s*([^']*)'`)
)

func cmdImportCurl(args []string) error {
	fs := flag.NewFlagSet("import-curl", flag.ExitOnError)
	cf := addCommon(fs)
	file := fs.String("file", "-", "file with a DevTools 'Copy as cURL' command ('-' = stdin)")
	_ = fs.Parse(reorderArgs(fs, args))
	data, err := readInput(*file)
	if err != nil {
		return err
	}
	s := string(data)
	cookie := firstSubmatch(reCookieB, s)
	if cookie == "" {
		cookie = strings.TrimSpace(firstSubmatch(reCookieH, s))
	}
	if cookie == "" {
		return fmt.Errorf("no cookie found in the input — copy a request to %s as cURL (bash)", client.BaseURL)
	}
	return importCookies(cf, client.ParseCookieHeader(cookie), "cURL")
}

// import-har extracts the session from a browser HAR export (DevTools → Network
// → "Export HAR (with sensitive data)…").
func cmdImportHar(args []string) error {
	fs := flag.NewFlagSet("import-har", flag.ExitOnError)
	cf := addCommon(fs)
	file := fs.String("file", "", "path to a .har file ('-' or omitted = stdin; may also be passed positionally)")
	_ = fs.Parse(reorderArgs(fs, args))
	src := *file
	if src == "" && fs.NArg() == 1 {
		src = fs.Arg(0)
	}
	if src == "" {
		src = "-"
	}
	data, err := readInput(src)
	if err != nil {
		return err
	}
	sess, err := client.ParseHAR(data)
	if err != nil {
		return err
	}
	return importCookies(cf, sess.Cookies, "HAR")
}

// import-firefox reads the session straight from the user's Firefox profile
// (the sqlite3 binary does the reading; Firefox may stay open). With --auto it
// also turns on per-run sync, so the CLI keeps carrying the browser's fresh
// aws-waf-token instead of a copy that expires within minutes.
func cmdImportFirefox(args []string) error {
	fs := flag.NewFlagSet("import-firefox", flag.ExitOnError)
	cf := addCommon(fs)
	profile := fs.String("profile", "", "Firefox profile directory (default: Firefox's default profile)")
	auto := fs.Bool("auto", false, "also sync from Firefox automatically on every run (saved to config.toml)")
	off := fs.Bool("off", false, "turn automatic Firefox sync off again")
	_ = fs.Parse(reorderArgs(fs, args))

	cfg, _ := config.LoadConfig()
	if *off {
		cfg.Session.Browser, cfg.Session.FirefoxProfile = "", ""
		if err := config.SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "automatic Firefox sync turned off (the cached session stays as it is)")
		return nil
	}
	dir := *profile
	if dir == "" {
		dir = cfg.Session.FirefoxProfile
	}
	if dir == "" {
		p, err := firefox.DefaultProfile()
		if err != nil {
			return err
		}
		dir = p
	}
	cookies, err := firefox.Cookies(dir, "compraonline.alcampo.es")
	if err != nil {
		return err
	}
	if cookies["global_sid"] == "" {
		return fmt.Errorf("no Alcampo session cookie (global_sid) in the Firefox profile %s — open %s in Firefox "+
			"(Firefox writes session cookies to disk every ~15 s, so retry shortly after), or pass --profile", dir, client.BaseURL)
	}
	if *auto {
		cfg.Session.Browser = "firefox"
		cfg.Session.FirefoxProfile = *profile // empty keeps following Firefox's default
		if err := config.SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "automatic Firefox sync on: every run re-reads the session from Firefox (keep a shop tab open so its WAF token stays fresh; `alcampo import-firefox --off` to stop)")
	}
	fmt.Fprintf(os.Stderr, "Firefox profile: %s\n", dir)
	return importCookies(cf, cookies, "Firefox profile")
}

// set-cookie seeds the session from a raw Cookie header (DevTools → Application
// → Cookies, or a request's Cookie header). Prefer --stdin to keep it out of argv.
func cmdSetCookie(args []string) error {
	fs := flag.NewFlagSet("set-cookie", flag.ExitOnError)
	cf := addCommon(fs)
	stdin := fs.Bool("stdin", false, "read the Cookie header from stdin (keeps it out of argv)")
	_ = fs.Parse(reorderArgs(fs, args))
	var raw string
	switch {
	case *stdin:
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		raw = strings.TrimSpace(string(b))
	case fs.NArg() == 1:
		raw = strings.TrimSpace(fs.Arg(0))
	default:
		return fmt.Errorf("usage: alcampo set-cookie '<name=value; ...>'   (or: ... set-cookie --stdin)")
	}
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "cookie:"), "Cookie:"))
	cookies := client.ParseCookieHeader(raw)
	if len(cookies) == 0 {
		return fmt.Errorf("no cookies found in the input (expected 'name=value; name2=value2')")
	}
	return importCookies(cf, cookies, "Cookie header")
}

// importCookies replaces the cached session, then reports what it holds — cookie
// names and login state only, never values.
func importCookies(cf *common, cookies map[string]string, from string) error {
	if cookies["global_sid"] == "" {
		return fmt.Errorf("the %s has no global_sid session cookie — copy it from a request to %s", from, client.BaseURL)
	}
	cl := newClient(cf)
	cl.UseCookies(cookies)
	if err := cl.SaveSession(); err != nil {
		return err
	}
	names := cl.CookieNames()
	sort.Strings(names)
	fmt.Fprintf(os.Stderr, "imported session from %s: %d cookies (%s) → %s/session.json (0600)\n",
		from, len(names), strings.Join(names, ", "), config.Dir())
	if cookies["aws-waf-token"] == "" {
		fmt.Fprintln(os.Stderr, "⚠ no aws-waf-token cookie: if requests get WAF-challenged (HTTP 202), re-import from a request that carries it.")
	}
	switch ok, err := cl.LoggedIn(); {
	case err != nil:
		fmt.Fprintf(os.Stderr, "⚠ couldn't verify the session: %v\n", err)
	case ok:
		fmt.Fprintln(os.Stderr, "→ logged in. verify with: alcampo whoami")
	default:
		fmt.Fprintln(os.Stderr, "→ session is anonymous (not logged in): cart works, checkout needs you to log in in the browser first and re-import.")
	}
	return nil
}

// logout forgets the cached session (cookies + CSRF). The next command starts a
// fresh anonymous session with an empty cart; the browser session is untouched.
func cmdLogout(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	_ = fs.Parse(reorderArgs(fs, args))
	p := config.Dir() + "/session.json"
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintf(os.Stderr, "session cache removed (%s)\n", p)
	return nil
}

func readInput(file string) ([]byte, error) {
	if file == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(file)
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
