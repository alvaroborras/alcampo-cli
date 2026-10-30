# `alcampo` CLI reference

Every command accepts `--json` (raw data → stdout, logs → stderr; exit 1 on error). Flags may go
anywhere after the (sub)command. Product ids are the shop's numeric ids (`54180`); the platform
UUIDs (`176cfd5e-…`) are accepted anywhere too. Requests are paced (≥250 ms apart) and parallel
lookups are capped at 2 (`ALCAMPO_CONCURRENCY=1..16`) to stay under the shop's AWS WAF rate limit.

## Read (anonymous)

| Command | What it does |
|---|---|
| `alcampo search <term...> [--limit N] [--category <id\|name>] [--fresh]` | full-text product search |
| `alcampo batch [-f file] [--hits N] [--category <id\|name>] [--fresh]` | top hit(s) for many terms (one search per term, run in parallel) |
| `alcampo total [-f file]` | deterministic basket total from `<id> [qty]` lines (list prices, summed in cents) |
| `alcampo product <id>` | detail: price, unit price, promos, brand, pack size, category, nutrition |
| `alcampo categories [--depth N]` | category tree (`OC…` code + name) |
| `alcampo categories --id <id\|name> [--limit N]` | one category's products |

`--fresh` drops hits whose category path contains *Congelados* or *Conservas* (client-side, so it
over-fetches 4×). `--category` takes a UUID, an `OC…` code, or a name matched against the top
three tree levels (exact beats substring; ambiguity is an error listing candidates).

### Search hit shape (`search --json` → `.hits[]`, `batch --json` → `[].hits[]`)

```json
{
  "productId": "176cfd5e-afa9-4aa1-8b6f-fe3d757c535e",
  "retailerProductId": "54180",
  "name": "AUCHAN Leche semidesnatada de vaca 6 x 1l Producto Alcampo.",
  "brand": "PRODUCTO ALCAMPO",
  "packSizeDescription": "6000ml",
  "price": {"amount": "5.28", "currency": "EUR"},
  "unitPrice": {"price": {"amount": "0.88", "currency": "EUR"}, "unit": "fop.price.per.litre", "unitName": "PER_LITRE"},
  "available": true,
  "categoryPath": ["Leche, Huevos, Lácteos, Yogures y Bebidas vegetales", "Leche", "Leche semidesnatada"],
  "promotions": [{"description": "…"}]
}
```

`product --json` returns the raw product page: `{product: <hit shape + more>, bopData:
{detailedDescription, fields: [{title, content}], breadcrumbs}, bopPromotions, …}`. Nutrition is the
HTML table in the `nutritionalData` field (the human output parses it).

`total --json` → `{lines: [{id, name, qty, unit_price, subtotal, promo?, error?}], total, count, complete}`.

## Session

| Command | What it does |
|---|---|
| `alcampo import-curl [--file f]` | import cookies from a DevTools "Copy as cURL (bash)" (`-` = stdin) |
| `alcampo import-har [--file f]` | import cookies from "Export HAR (with sensitive data)" |
| `alcampo import-firefox [--auto] [--off] [--profile dir]` | import from the Firefox profile (needs `sqlite3`); `--auto` re-syncs every run |
| `alcampo set-cookie [--stdin]` | import a raw `name=value; …` Cookie header |
| `alcampo whoami` | logged in (name, email, customer id) or anonymous |
| `alcampo logout` | delete the cached session (next run starts a new anonymous cart) |

The session is the cookie jar (`global_sid` owns visitor + cart; `aws-waf-token` is the WAF
clearance; `VISITORID`, `AWSALB*`) plus the CSRF token scraped from the HTML shell and sent as
`X-CSRF-TOKEN` on writes. Rotated cookies are written back after every response.
`ALCAMPO_COOKIE='<header>'` replaces the session for one run.

## Cart (works anonymously)

| Command | What it does |
|---|---|
| `alcampo cart get` | lines (`[id] name — qty × price = subtotal`), total, savings, checkout restrictions |
| `alcampo cart add <id> <qty> [--max EUR]` | add qty to the current quantity |
| `alcampo cart set <id> <qty> [--max EUR]` | absolute qty (`0` removes) |
| `alcampo cart set-many [-f file] [--max EUR]` | many absolute `<id> <qty>` lines, ONE write (`#` comments ok) |
| `alcampo cart clear` | empty the cart |

Under the hood: `GET /api/cart/v1/carts/active`, then `POST …/carts/active/apply-quantity` with
**relative** deltas `[{productId, quantity}]` (the CLI computes them from your absolute targets),
and `DELETE …/carts/active/items` for clear.

`cart get --json` (raw):

```json
{
  "cartId": "…", "regionId": "…", "deliveryDestinationId": "…",
  "items": [{"productId": "…", "quantity": 3, "price": {"amount": "5.28"},
             "totalPrices": {"finalPrice": {"amount": "15.84"}}}],
  "totals": {"itemPriceAfterPromos": {"amount": "15.84"}, "itemsRetailPrice": {"amount": "15.84"},
             "savingsPrice": {"amount": "0.00"}},
  "defaultCheckoutGroup": {"shippingGroupType": "…", "canCheckout": false,
                           "checkoutRestrictions": ["MISSING_SLOT"],
                           "minimumCheckoutThreshold": {"amount": "0.01"}}
}
```

Raw lines carry only the UUID; the human view decorates them with name + numeric id.
Write `--json` output is the raw apply-quantity response (`basketUpdateResult.{itemGroups, totals}`).

## Checkout (logged-in session)

| Command | What it does |
|---|---|
| `alcampo checkout addresses` | saved home-delivery addresses (raw) |
| `alcampo checkout slots [--address <id>]` | delivery slots (default: the cart's `deliveryDestinationId`) |
| `alcampo checkout reserve --slot <id> [--address <id>]` | reserve a slot |
| `alcampo checkout summary [--max EUR]` | checkout summary (raw), budget-checked |
| `alcampo checkout finish [--max EUR]` | fail-closed budget check, then print the browser checkout URL |

Payment (card + 3-D Secure) is completed in the browser; the CLI never places or pays an order.

## Spending guard

`--max <eur>` on cart writes and `checkout summary/finish`; also `ALCAMPO_MAX_EUR` or
`[limits] max_eur` in config; precedence flag > env > config. Cart writes refuse before writing on
a complete local estimate, and roll back if the server total lands over the cap. `finish` fails
closed when the total can't be read.

## State

`~/.alcampo/` (override with `ALCAMPO_CONFIG_DIR`), all files 0600:

- `session.json` — cookies + CSRF token (secret: it is your login)
- `config.toml` — `[limits] max_eur`
