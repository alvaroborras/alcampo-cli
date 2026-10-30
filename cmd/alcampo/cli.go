package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alvaroborras/alcampo-cli/internal/client"
	"github.com/alvaroborras/alcampo-cli/internal/config"
	"github.com/alvaroborras/alcampo-cli/internal/firefox"
)

// common flags shared by every subcommand.
type common struct {
	jsonOut bool
}

func addCommon(fs *flag.FlagSet) *common {
	c := &common{}
	fs.BoolVar(&c.jsonOut, "json", false, "emit raw JSON to stdout")
	return c
}

// newClient builds a client on the cached session (~/.alcampo/session.json), so
// the anonymous cart and any imported login carry over between runs. Cookie
// rotations are written back as they happen. With browser sync on
// ([session] browser = "firefox", or ALCAMPO_BROWSER=firefox) the session is
// first refreshed from the Firefox profile — and again once if the WAF
// challenges a request — so the CLI carries the browser's live clearance.
// ALCAMPO_COOKIE, when set, replaces the session for this run.
func newClient(_ *common) *client.Client {
	cl := client.New()
	cl.LoadSession()
	cfg, _ := config.LoadConfig()
	if strings.EqualFold(firstNonEmpty(os.Getenv("ALCAMPO_BROWSER"), cfg.Session.Browser), "firefox") {
		var warned bool
		sync := func() bool {
			cookies, err := firefoxCookies(cfg.Session.FirefoxProfile)
			if err == nil && cookies["global_sid"] == "" {
				err = fmt.Errorf("no Alcampo session in Firefox (open %s there)", client.BaseURL)
			}
			if err != nil {
				if !warned {
					warned = true
					fmt.Fprintf(os.Stderr, "⚠ Firefox session sync failed, using the cached session: %v\n", err)
				}
				return false
			}
			changed := cl.SyncCookies(cookies)
			if changed {
				_ = cl.SaveSession()
			}
			return changed
		}
		sync()
		cl.OnChallenge = sync
	}
	if ck := os.Getenv("ALCAMPO_COOKIE"); ck != "" {
		cl.SetCookieHeader(ck)
	}
	return cl
}

// firefoxCookies reads the shop's cookies from the given (or default) profile.
func firefoxCookies(profile string) (map[string]string, error) {
	if profile == "" {
		p, err := firefox.DefaultProfile()
		if err != nil {
			return nil, err
		}
		profile = p
	}
	return firefox.Cookies(profile, "compraonline.alcampo.es")
}

// lookupConcurrency bounds concurrent product GETs (pricing a basket, resolving
// ids, batch searches). Kept deliberately low: the shop's AWS WAF challenges
// bursty clients. Override with ALCAMPO_CONCURRENCY (1–16).
func lookupConcurrency() int {
	if s := os.Getenv("ALCAMPO_CONCURRENCY"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 1 && n <= 16 {
			return n
		}
	}
	return 2
}

// reorderArgs lets flags appear anywhere among positional args. The stdlib flag
// parser stops at the first positional, so `cart add 123 5 --max 50` would
// silently drop --max (dangerous for a safety flag); this hoists flags (and their
// values) ahead of a `--` terminator so a normal fs.Parse sees them all. Honours
// bool flags (no value), `--flag=value`, and an explicit `--`.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.IndexByte(name, '=') >= 0 {
				continue // --flag=value: value is in the same token
			}
			if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
				flags = append(flags, args[i+1]) // consume this flag's value
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	out := make([]string, 0, len(flags)+1+len(positional))
	out = append(out, flags...)
	out = append(out, "--") // keep positionals positional even if they start with '-'
	return append(out, positional...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func emitRaw(raw json.RawMessage) error {
	var buf any
	if err := json.Unmarshal(raw, &buf); err != nil {
		_, err = os.Stdout.Write(raw)
		return err
	}
	return emitJSON(buf)
}
