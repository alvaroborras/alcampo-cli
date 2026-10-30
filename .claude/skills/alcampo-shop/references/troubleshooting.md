# Troubleshooting

Common failure modes when driving the `alcampo` CLI, and how to recover.

## "blocked by Alcampo's AWS WAF bot challenge (HTTP 202 …)"

The shop sits behind AWS WAF, which answers bursty clients with a JavaScript challenge (HTTP 202,
empty body, `x-amzn-waf-action: challenge`) instead of data. Observed: roughly 40–50 requests in a
couple of minutes from one IP trips it, and it clears after ~5 minutes.

- **Wait ~5 minutes**, then continue at a gentler pace. Don't retry in a loop — that extends it.
- **Use fewer requests:** one `batch` instead of many `search`es; reuse ids you already resolved;
  avoid re-running `product` for ids whose price you have.
- **Lower parallelism:** `ALCAMPO_CONCURRENCY=1`.
- **Firefox users: `alcampo import-firefox --auto`.** Every run then re-reads the session from
  Firefox, carrying the `aws-waf-token` Firefox keeps fresh (keep a shop tab open). This is the
  durable fix; the options below are one-offs.
- **Import a browser clearance:** a request copied from a browser tab that has the shop open
  carries an `aws-waf-token` cookie, which satisfies the challenge for a while.
  `alcampo import-curl --file s.txt` (this also replaces the session — use the logged-in tab).

## "not logged in" / "the cached session is anonymous (or expired)"

Checkout commands need a logged-in session. Log in at `compraonline.alcampo.es` in the browser,
copy any shop request as cURL (bash) and `alcampo import-curl --file s.txt`, then
`alcampo whoami`. If `whoami` still says anonymous, the copied request was from a logged-out tab,
or it lacked the `global_sid` cookie.

A HAR import fails with "no global_sid session cookie" when it was exported **sanitized** (Chrome's
default "Export HAR" strips cookies) — use "Export HAR (with sensitive data)".

## "My browser doesn't show the cart the CLI built"

The cart belongs to the session cookie. An anonymous CLI session has its own cart that no browser
sees. Import the logged-in browser session **first**, then fill the cart — or, if you already built
it anonymously, note the lines (`alcampo cart get`), import the session, and re-apply them with
`cart set-many`. (`alcampo logout` discards the CLI's session and its anonymous cart.)

## "the cart is on the shop's temporary default destination"

`checkout slots`/`reserve` need a real saved delivery address. A session that never picked one
sits on the shop's browse-only default destination, which the slot service rejects. Have the user
add or select their address at `compraonline.alcampo.es` (in the same browser session), then
`alcampo checkout addresses` and pass `--address <id>`.

## A write fails with HTTP 403

Writes carry the session's CSRF token (scraped from the shop's HTML). The CLI refreshes it once
automatically when a write is rejected for CSRF; a persistent 403 usually means the session expired
— re-import it.

## Search gives no / odd results

- Try a simpler or more specific term; the shop caps search terms at 50 characters.
- `--fresh` filters client-side; if every hit was frozen/canned it can return nothing — drop it.
- `--category` with a name that matches several categories is an error that lists them; pass the
  `OC…` code instead.

## `total` differs from the cart total

`total` multiplies **list prices**. Multi-buy promotions (2ª unidad al 50%, 3x2…) are applied only
by the cart, so `cart get` can be lower. Quote the cart figure once items are in.

## "product … not found"

The id doesn't exist (or isn't sold online). Ids are the numbers from the product URL
(`…/products/<slug>/54180`) or the UUIDs from `--json` output. Re-search to get the current id.

## Budget errors

`BUDGET EXCEEDED` is the guard working: stop and report it. Raise `--max` only when the user raises
the budget. Remember the checkout cap must also cover the delivery fee.
