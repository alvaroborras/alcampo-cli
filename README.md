# alcampo

Unofficial, agent-friendly CLI for `compraonline.alcampo.es` — search the catalog, read
prices and promos, build a cart, and prepare delivery checkout. Single static Go binary, no
runtime deps, structured `--json` output for programmatic/agent use.

> Unofficial. Alcampo has no public API. Bring your own account; use at a sane request rate.
> This talks to the same HTTP endpoints the website does.
>
> Forked from [ivorpad/mercadona-cli](https://github.com/ivorpad/mercadona-cli) and ported
> from Mercadona's API to Alcampo's (which runs on the Ocado Smart Platform).

## Install

**npm** — downloads the prebuilt binary for your platform on first run:

```bash
npm install -g @alvaroborras/alcampo      # puts `alcampo` on your PATH
npx @alvaroborras/alcampo search queso    # …or run without installing
```

**curl** (macOS / Linux):

```bash
curl -fsSL https://raw.githubusercontent.com/alvaroborras/alcampo-cli/main/install.sh | sh
```

Override with `ALCAMPO_VERSION=v0.1.0` (pin a tag) or `ALCAMPO_INSTALL_DIR=/path`
(install location; defaults to `/usr/local/bin`, else `~/.local/bin`).

**Manual** — download a tarball for your OS/arch from the
[releases page](https://github.com/alvaroborras/alcampo-cli/releases), extract, and put
`alcampo` on your PATH.

**From source** (Go 1.26+):

```bash
go install github.com/alvaroborras/alcampo-cli/cmd/alcampo@latest
# or, in a clone:
go build -o alcampo ./cmd/alcampo
```

## Commands

### Read (anonymous — no login)

```bash
alcampo search leche                         # full-text product search
alcampo search --limit 5 --json mayonesa     # structured output for agents
alcampo search gambas --fresh                # drop frozen (Congelados) + canned (Conservas)
alcampo search atún --category Conservas     # scope to a category (name, OC… code or UUID)
alcampo batch -f lista.txt                   # top hit for many terms
printf 'queso\nmayonesa\n' | alcampo batch -f -
alcampo product 54180                        # detail, price, promos, nutrition
alcampo total -f cesta.txt                   # exact basket total from '<id> [qty]' lines
alcampo categories                           # category tree (OC… codes)
alcampo categories --id "Leche semidesnatada" --limit 10   # one category's products
```

Product ids are the shop's numeric ids — the number at the end of a product URL
(`…/products/<slug>/54180`). The platform's UUIDs (`176cfd5e-…`, in `--json` output) are
accepted anywhere too. Data goes to **stdout**, logs/errors to **stderr**, exit code `1` on
error — friendly to scripts and agents. Flags can go anywhere after the (sub)command.

```console
$ alcampo search --limit 3 leche
query="leche"  hits=3+
  [54180] AUCHAN Leche semidesnatada de vaca 6 x 1l Producto Alcampo. — 5.28€ (0.88€/l) (Leche semidesnatada)
  [54178] AUCHAN Leche entera de vaca 6 x 1 l Producto Alcampo. — 5.76€ (0.96€/l) (Leche entera)
  [99193] L.R. Leche entera 6 x 1 l. — 5.70€ (0.95€/l) (Leche entera)

$ alcampo product 54180
[54180] AUCHAN Leche semidesnatada de vaca 6 x 1l Producto Alcampo.
  precio: 5.28€  (0.88€/l)
  marca: PRODUCTO ALCAMPO
  formato: 6000ml
  categoría: Leche, Huevos, Lácteos, Yogures y Bebidas vegetales › Leche › Leche semidesnatada
  uuid: 176cfd5e-afa9-4aa1-8b6f-fe3d757c535e
  nutrición (por 100g):
    Valor energético (Kj): 193 Kj
    Valor energético (Kcal): 46 Kcal
    Grasas: 1.6 g
    ...
```

### Cart — works anonymously

Alcampo's shop (Ocado platform) keys the cart to a **session cookie**, not an account, so the
cart works without logging in. The CLI caches its session in `~/.alcampo/session.json`, so the
cart carries over between runs.

```bash
alcampo cart get                             # lines, qty × price, total, savings, restrictions
alcampo cart add 54180 2 --max 80            # add 2 (refuses if the cart would exceed 80 €)
alcampo cart set 54180 1                     # absolute quantity (0 removes)
alcampo cart set-many -f - --max 80 <<'EOF'  # many absolute '<id> <qty>' lines in ONE write
54180 1   # Leche semidesnatada Auchan
99211 3   # Leche semidesnatada L.R.
EOF
alcampo cart clear                           # empty it
```

```console
$ alcampo cart get
carrito: 2 productos, total 20.22€
  [54180] AUCHAN Leche semidesnatada de vaca 6 x 1l Producto Alcampo. — 1 × 5.28€ = 5.28€
  [99211] L.R. Leche semidesnatada 6 x 1 l. — 3 × 4.98€ = 14.94€
  ⚠ falta reservar franja de entrega (`alcampo checkout slots`)
```

An anonymous CLI cart lives only in the CLI's session, though — your browser won't see it. To
check out, use your logged-in session (below) *before* filling the cart, so the CLI and the
browser share one cart.

### Session: log in in the browser, import the cookies

The shop authenticates with cookies (`global_sid` + friends), and its login form sits behind a
captcha, so the CLI never logs in itself. You log in once in your browser, and the CLI imports
the session:

```bash
# 1. log in at https://www.compraonline.alcampo.es
# 2. DevTools → Network → right-click any request to the shop → Copy → Copy as cURL (bash)
alcampo import-curl --file session.txt       # or: pbpaste | alcampo import-curl
alcampo whoami                               # → ok — logged in as Nombre Apellido <you@…>
```

Alternatives: `alcampo import-har --file shop.har` (DevTools → **Export HAR (with sensitive
data)…** — the default sanitized export strips cookies), or `alcampo set-cookie --stdin` with a
raw `name=value; …` Cookie header. `ALCAMPO_COOKIE` overrides the session for a single run.
`alcampo logout` forgets the cached session.

Imports report cookie **names** only; values are never printed. Cookie rotations are written
back to `session.json` (0600) after every response.

### Checkout (logged-in session)

```bash
alcampo checkout addresses                   # saved delivery addresses
alcampo checkout slots                       # slots for the cart's address (or --address <id>)
alcampo checkout reserve --slot <slotId>     # hold a delivery slot
alcampo checkout summary --max 90            # payable total incl. delivery (raw JSON)
alcampo checkout finish --max 90             # budget gate, then prints the checkout URL
```

Payment goes through the shop's card processor with 3-D Secure, which needs the browser, so the
CLI's last step is `checkout finish`: it checks the payable total against your cap (failing
**closed** if it can't read it) and prints the checkout URL. You review and pay there, with the
same account and cart. **The CLI never places or pays for an order.**

> Status: the checkout calls (`addresses`, `slots`, `reserve`, `summary`) use the endpoints and
> request bodies the web app sends, but they haven't yet been run against a logged-in account.
> Everything else in this README has been verified live.

### Spending guard (agent safety)

When an agent drives the CLI, cap how much it can ever spend. Any cart write or checkout step
over the cap is refused with a non-zero exit and an `error:` line:

```console
$ alcampo cart add 53004 1 --max 15
error: BUDGET EXCEEDED (estimated ≈33.06€ > 15.00€ limit): not writing add — raise the cap with --max, ALCAMPO_MAX_EUR, or [limits].max_eur
```

Cart writes price the resulting basket *before* writing, and if the server's total still lands
over the cap (rounding, promos), the change is rolled back. Set the cap once with
`ALCAMPO_MAX_EUR=100`, or in config:

```toml
# ~/.alcampo/config.toml
[limits]
max_eur = 100        # refuse any cart/checkout whose total exceeds 100 €
```

Precedence is **flag > env > config**; `0`/unset = no limit.

## Design / reliability

The CLI calls the same JSON endpoints the Alcampo site calls (`/api/webproductpagews/…` for the
catalog, `/api/cart/…`, `/api/ecomslots/…`, `/api/ecomdeliverydestinations/…`). It doesn't
scrape rendered pages. The one exception is the CSRF token, which it reads from the HTML shell.

- **Session.** A cookie jar (`global_sid`, `VISITORID`, `AWSALB*`, `aws-waf-token`) is persisted
  across runs. Writes carry the session's `X-CSRF-TOKEN`. If a write is rejected for CSRF, the
  token is re-fetched once and the write retried.
- **Cart writes** use `apply-quantity`, which takes *relative* deltas. The CLI computes them from
  your absolute targets against the current cart, so `set-many` is one request whatever its size.
- **TLS.** The client hello mimics Chrome's (uTLS) over HTTP/1.1. If that setup fails, the CLI
  falls back to Go's standard transport.
- **AWS WAF.** The shop's WAF answers bursty clients with a JavaScript challenge (HTTP 202,
  `x-amzn-waf-action: challenge`). In testing, roughly 40–50 requests in a couple of minutes from
  one IP tripped it, and it cleared after about 5 minutes. To avoid it, the CLI spaces requests
  ≥250 ms apart and runs at most 2 lookups in parallel (`ALCAMPO_CONCURRENCY`, 1–16). If it's
  challenged anyway, it stops with a clear error instead of hammering. A browser-imported
  session carrying `aws-waf-token` satisfies the challenge for a while. The CLI doesn't try to
  solve challenges itself.
- **Rate limits.** On HTTP 429/503 it retries with `Retry-After` or exponential backoff plus jitter.
- **Region.** Catalog and delivery follow the session's region: the shop's default region for an
  anonymous session, your address's region for a logged-in one.

## Config

State lives in `~/.alcampo/` (override with `ALCAMPO_CONFIG_DIR`), all `0600`:

- `session.json` — cookies + CSRF token (machine-managed; **secret** — it *is* your login).
- `config.toml` — user-authored: `[limits] max_eur`.

## Claude skill

This repo bundles a Claude Code skill, **`alcampo-shop`** (`.claude/skills/alcampo-shop/`), that
drives this CLI to do the grocery shop. It turns a list into priced products, confirms them with
you, fills the cart, and prepares delivery. Payment is handed to the browser only on your explicit
go-ahead. Install it where your Claude reads skills (symlink or copy
`.claude/skills/alcampo-shop` into `~/.claude/skills/`).

## Releasing

Push a semver tag — GitHub Actions
([`.github/workflows/release.yml`](.github/workflows/release.yml)) cross-compiles with
[GoReleaser](https://goreleaser.com), publishes a GitHub Release (per-OS/arch archives +
`checksums.txt`), then publishes the npm wrapper that downloads from it.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

Actions are pinned to commit SHAs, permissions are least-privilege per job, and npm publishes via
OIDC Trusted Publishing (no long-lived token) with provenance. One-time setup: configure a Trusted
Publisher for `@alvaroborras/alcampo` on npmjs.com (GitHub Actions: user `alvaroborras`, repo
`alcampo-cli`, workflow `release.yml`).

Dry-run the build locally (no publish; artifacts land in `./dist`):

```bash
goreleaser release --snapshot --clean --skip=publish
```
