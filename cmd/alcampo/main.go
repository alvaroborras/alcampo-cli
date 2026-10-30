// Command alcampo is an unofficial, agent-friendly CLI for compraonline.alcampo.es.
//
// It covers the anonymous read core (product search single + batch, catalog/detail
// reads), the session cart (works anonymously, cached between runs) and the
// logged-in checkout leg (import-curl/import-har, slots, summary) with a
// configurable spending cap. Every command supports --json (data to stdout,
// logs to stderr) for programmatic use by agents.
package main

import (
	"fmt"
	"os"
)

// Build metadata, injected at release time via -ldflags (GoReleaser).
// Defaults are for a local `go build`.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "search":
		err = cmdSearch(os.Args[2:])
	case "batch":
		err = cmdBatch(os.Args[2:])
	case "total":
		err = cmdTotal(os.Args[2:])
	case "product":
		err = cmdProduct(os.Args[2:])
	case "categories":
		err = cmdCategories(os.Args[2:])
	case "import-curl":
		err = cmdImportCurl(os.Args[2:])
	case "import-har":
		err = cmdImportHar(os.Args[2:])
	case "set-cookie":
		err = cmdSetCookie(os.Args[2:])
	case "logout":
		err = cmdLogout(os.Args[2:])
	case "whoami":
		err = cmdWhoami(os.Args[2:])
	case "cart":
		err = cmdCart(os.Args[2:])
	case "checkout":
		err = cmdCheckout(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// versionString renders the build version, appending the commit/date when they
// were injected at release time (a local `go build` prints just "dev").
func versionString() string {
	if commit == "" {
		return version
	}
	if date != "" {
		return fmt.Sprintf("%s (%s, %s)", version, commit, date)
	}
	return fmt.Sprintf("%s (%s)", version, commit)
}

func usage() {
	fmt.Fprint(os.Stderr, `alcampo — unofficial CLI for compraonline.alcampo.es

USAGE:
  alcampo <command> [flags]

Product ids are the shop's numeric ids (e.g. 54180, as in its product URLs);
the platform UUIDs are accepted anywhere too.

READ COMMANDS (anonymous, no login):
  search <term...>        full-text product search
                          --limit N             max results (default 24)
                          --category <id|name>  filter to a category (see 'categories')
                          --fresh               drop frozen (Congelados) + canned (Conservas)
  batch [-f file]         search many terms, top hit each (--hits N, --category, --fresh)
  total [-f file]         deterministic basket total from '<id> [qty]' lines (summed in code)
  product <id>            product detail, price, promos, nutrition (when available)
  categories              category tree (--depth N), or --id <id|name> for its products

CART (works anonymously — the cart belongs to the cached session cookie):
  cart get                show current cart (names, qty × price, total; --json for raw)
  cart add <id> <qty>     add qty of a product
  cart set <id> <qty>     set a product's absolute qty (0 removes)
  cart set-many [-f file] apply many '<id> <qty>' lines in ONE write (0 removes);
                          prices the basket first so --max refuses before writing
  cart clear              empty the cart

SESSION (login happens in the browser; the CLI imports its cookies):
  import-curl [--file f]  import a session from a DevTools 'Copy as cURL (bash)' of any
                          request to the shop ('-' = stdin)
  import-har [--file f]   import a session from a DevTools 'Export HAR (with sensitive data)'
  set-cookie [--stdin]    import a raw 'name=value; …' Cookie header
  whoami                  show whether the session is logged in, and as whom
  logout                  forget the cached session (starts a fresh anonymous cart)

CHECKOUT (needs a logged-in session):
  checkout addresses      list saved delivery addresses
  checkout slots          delivery slots (--address <id>; default: the cart's address)
  checkout reserve        --slot <id> [--address <id>]   reserve a delivery slot
  checkout summary        payable total incl. delivery (raw JSON; --max guard)
  checkout finish         verify the total against --max, then print the checkout URL:
                          payment (3-D Secure) is completed in the browser

SPENDING GUARD (agent safety — caps how much can be spent):
  --max <eur>             refuse any cart/checkout whose total exceeds <eur>.
                          Also ALCAMPO_MAX_EUR env or [limits] max_eur in config.toml.
                          'checkout finish' fails CLOSED: with a cap set, if the total
                          can't be read it refuses.

COMMON FLAGS (may go anywhere after the (sub)command):
  --json                  emit raw JSON (data→stdout, logs→stderr)

ENVIRONMENT:
  ALCAMPO_CONFIG_DIR      state dir (default ~/.alcampo)
  ALCAMPO_COOKIE          use this Cookie header as the session for this run
  ALCAMPO_CONCURRENCY     parallel product lookups/searches (1–16, default 2)

  version | help
`)
}
