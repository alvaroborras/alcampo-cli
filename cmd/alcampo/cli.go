package main

import (
	"encoding/json"
	"flag"
	"os"
	"strconv"
	"strings"

	"github.com/alvaroborras/alcampo-cli/internal/client"
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
// rotations are written back as they happen. ALCAMPO_COOKIE, when set, replaces
// the cached session for this run (and is then cached like any other).
func newClient(_ *common) *client.Client {
	cl := client.New()
	cl.LoadSession()
	if ck := os.Getenv("ALCAMPO_COOKIE"); ck != "" {
		cl.SetCookieHeader(ck)
	}
	return cl
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
