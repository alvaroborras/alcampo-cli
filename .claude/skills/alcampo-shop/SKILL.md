---
name: alcampo-shop
description: >-
  Do the grocery shop at Alcampo (compraonline.alcampo.es) by driving the local `alcampo`
  CLI: turn a shopping list into real priced products, fill the cart, prepare delivery
  checkout (address + slot), and hand off to the browser for payment only on explicit
  go-ahead. Use this whenever the user wants to shop, price a grocery list, build a cart, or
  check out at Alcampo — including Spanish phrasings like "hazme la compra en Alcampo",
  "compra en Alcampo", "añade X al carrito", "pídeme estos productos", "¿cuánto cuesta esta
  lista en Alcampo?", and English ones like "do my Alcampo shop", "price this grocery list at
  Alcampo", "fill my Alcampo cart". Trigger even when the user just pastes a list of groceries
  and mentions Alcampo without saying "skill" or "CLI". Also use when the user wants to cook a
  dish or recipe and buy the ingredients at Alcampo ("quiero hacer una paella", "ingredientes
  para una cena para 6"): it asks how many people and any allergies before it prices, so it
  never guesses the headcount. Always confirm the resolved products before touching the cart,
  and never proceed to payment without explicit consent. Do NOT use for other supermarkets
  (Mercadona, Carrefour, Lidl, Amazon) — this is Alcampo-only.
---

# Alcampo shop

Turn a shopping list into a real Alcampo order by driving the `alcampo` CLI — search prices,
fill the cart, reserve a delivery slot, and hand the reviewed order to the browser to pay. The
CLI talks to the same HTTP endpoints `compraonline.alcampo.es` uses; this skill is the playbook
for using it safely on a real account.

## The two safety gates (read first)

This leads to spending real money on a real account, so two points are non-negotiable:

1. **Confirm before touching the cart.** Reading and pricing are free and side-effect-free —
   do them freely. But before the *first* `cart add`/`cart set`/`cart set-many`, show the user
   the resolved plan (each list item → the exact product you picked, with id, name, size and
   price) and wait for an explicit OK. Product matching is fuzzy (see below), so this is where
   the user catches a wrong brand or size.
2. **Never move to payment without explicit consent.** `checkout finish` is the last CLI step:
   it checks the payable total against `--max` and prints the checkout URL where the user pays
   (card + 3-D Secure, in the browser). Run it only when the user has clearly said "yes, let's
   pay" for *this* order, after you've shown them the full total including delivery. The CLI
   never pays by itself.

Everything else (search, batch pricing, `cart get`, `checkout addresses`, `checkout slots`,
`checkout reserve`, `checkout summary`) is reversible and fine to run as you work toward those
two checkpoints. (Reserving a slot holds it for the session; it isn't an order.)

### Spending cap (defense in depth)

On top of the two gates, put a hard euro ceiling on every cart/checkout command with `--max <eur>`,
so a wrong product match or a fat-fingered quantity can't quietly run up a huge order. Choose the cap:

- the user's stated budget if they gave one ("máximo 80€", "no más de 100"); otherwise
- the agreed plan total from Gate 1, rounded up with a small margin.

Any `cart add/set/set-many` or `checkout summary/finish` whose total would exceed `--max` fails with
`error: BUDGET EXCEEDED …` and a non-zero exit. Cart writes price the basket **before** writing, and
if the server's total still lands over the cap they **roll the change back**. `checkout finish`
fails **closed**: with a cap set, if it can't read the total it refuses. Treat a budget error as
**stop-and-report** — only raise the cap if the user explicitly raises the budget. (Also settable
once: `ALCAMPO_MAX_EUR`, or `[limits].max_eur` in `~/.alcampo/config.toml`; flag > env > config.)

**Different euro figures — don't conflate them:**

- **Cart `--max`** — a ceiling on the product subtotal (promos applied). Leave a little headroom:
  real prices are discrete, so a "≈60€" target often lands at 60.10€.
- **Delivery fee** — the slot's price, added at checkout on top of the products. So the
  **checkout `--max` must cover products + delivery**. Using the cart cap on `checkout finish`
  will refuse a perfectly fine order.
- **Minimum order** — `cart get` warns ("no alcanza el pedido mínimo") when the shop's threshold
  isn't met; add items rather than fighting it.

## Locate the binary

Run `alcampo` from `PATH`. If it isn't installed, install it (npm `@alvaroborras/alcampo`, the
`curl | sh` script, or a GitHub release — see the project README) and confirm with
`alcampo version`. Never hardcode a local path or a build-from-source step.

## Sessions and login (read before the cart)

Alcampo runs on the Ocado platform: a **session cookie** (`global_sid`) identifies the visitor —
anonymous or logged in — and **owns the cart**. The CLI caches its session in
`~/.alcampo/session.json`, so:

- **Search, prices and `total` need nothing.**
- **The cart works anonymously** — but an anonymous CLI cart lives only in the CLI's own session.
  The user's browser won't see it, and checkout needs a logged-in account.
- **To check out, the CLI must share the user's logged-in browser session.** Login itself sits
  behind a captcha, so it always happens in the browser; the CLI imports the cookies:
  1. The user logs in at `compraonline.alcampo.es` in their own browser.
  2. DevTools → Network → right-click any request to the shop → **Copy → Copy as cURL (bash)**,
     saved to a file → `alcampo import-curl --file session.txt`
     (or **Export HAR (with sensitive data)…** → `alcampo import-har --file shop.har`; the default
     sanitized HAR export strips cookies).
  3. `alcampo whoami` → `ok — logged in as …`.

  CLI and browser then share **one cart**: what the CLI adds shows up in the browser, which is
  what makes the browser hand-off at payment work.

**So if the user intends to order (not just price), import the logged-in session *before* you
fill the cart.** Otherwise you build the cart in an anonymous session the browser can't see.
If `whoami` says the session is anonymous or expired, ask the user to log in and re-import.

**Never echo secrets.** Feed session material via files or stdin, never as argv; don't print
cookie values back to the user (the CLI reports only cookie names).

## The shopping workflow

**Pre-flight — check the existing cart.** Before you build or price anything, run `alcampo cart
get`. A real account often already holds items. If the cart is non-empty and unrelated to this
request, tell the user and offer to start clean with `alcampo cart clear` — otherwise your
`set-many` lands *on top of* whatever was there.

### 0. Scope & clarify before pricing (detect the gaps, ask only what matters)

A grocery request usually arrives underspecified, and the gaps change the basket. Before pricing,
scan for those gaps and resolve each the cheapest way that won't surprise the user:

- **They already told you** → use it, don't ask.
- **There's an obviously-safe default** → take it, but **mark it** in the plan (step 2).
- **It's consequential *and* you can't safely guess** → stop and ask.

The trap to avoid is silently inventing a consequential value — defaulting "una paella" to 4
people, or a seafood version for someone allergic to shellfish. The opposite trap is ten questions
before you lift a finger. Ask the *few* things that genuinely move the basket, **batched into one
round**, with a default so a one-word answer is enough.

**First, read what kind of request it is:**

- **A dish/recipe** ("hazme una paella", "ingredientes para lasaña") → turn it into ingredients
  and *scale them to a headcount*. Most ambiguity lives here.
- **A list of items** ("arroz, pollo, 6 huevos") → resolve each to a product.
- **Specific products** ("Arroz SOS 1 kg, leche Auchan") → near-direct; match and confirm.
- **Vague / a photo** ("lo de siempre") → ask for the list (or read the photo and confirm).

**Then scan these axes:**

- **Serving size / how many meals** — for any recipe or open-ended shop, always ask.
- **Diet & allergens** — for any recipe shop, ask once. An allergen miss is a health risk.
- **Recipe variant** — when a dish has genuinely different versions, ask; minor variants: pick the
  common one and mark it.
- **Brand / price tier** — "el más barato" or a named brand → honor it. Otherwise default to the
  own brand (**Auchan / "Producto Alcampo"**) and show it; don't ask.
- **Format** (fresh vs. frozen, size, eco, entera vs. desnatada) → default the common option and
  show it.
- **Quantity & units** — scale to headcount and show it. Reconcile pack-vs-unit (step 1).
  **Sanity-check absurd amounts** ("200 huevos" is almost certainly "20").
- **Budget** — given → that's your `--max`; not given → the agreed plan total becomes it.

**How to ask:** prefer the **AskUserQuestion** tool — one axis per question, 2-4 concrete options,
the sensible default first. Ask in the user's language.

**For a dish, show the ingredient math** (rice ~100 g/person, protein, sofrito, stock…) and let the
user adjust before pricing. For pantry staples either ask "¿algo que ya tengas en casa?" or include
them clearly so they can drop what they have.

### 1. Resolve the list to real products (free, no login)

```bash
printf 'leche entera\nhuevos\nplátano\naceite de oliva\n' | alcampo batch -f -
```

`batch` returns the **top hit per term** — a rough match. Ids are the shop's numeric ids
(`[54180]`), the same as in its product URLs.

- For anything the user was specific about (brand, size, "el barato", "sin lactosa") or where
  the top hit looks off, run `alcampo search "<term>" --limit 5` and pick deliberately.
- When genuinely ambiguous, show 2-3 options and let the user choose.
- Use `--json` when you need to parse ids/prices reliably.
- Hits show active promotions (🏷 …) — mention them, but price with the list price (see step 2).

**The fresh-vs-frozen (and canned) trap.** A bare term can top-rank the frozen or canned version.
`--fresh` drops anything under a *Congelados* or *Conservas* category (it over-fetches, then
filters):

```bash
printf 'gambas\nmejillón\nmerluza\n' | alcampo batch -f - --fresh
alcampo search gambas --fresh --limit 5
```

To pin a term to an aisle use `--category <id|name>` (a name like `"Pescadería"`, or an `OC…` code
from `alcampo categories`).

**Pace yourself.** The shop's AWS WAF challenges bursty clients (HTTP 202 → "blocked by Alcampo's
AWS WAF bot challenge"). Prefer one `batch` over many `search` calls, don't re-run lookups you
already have, and if you get blocked, wait ~5 minutes (see troubleshooting).

**Watch for, as you match:**

- **Pack vs. unit.** "6 huevos" on a dozen-pack product is 1 pack, not 6. Read the name and
  `packSizeDescription` before you set a quantity.
- **"El más barato por kilo/litro."** Compare the unit price (`(0.88€/l)`), not the pack price.
- **Unavailable / no decent match.** Hits marked `[no disponible]` can't be bought; say so and offer
  the nearest alternative.

### 2. Present the plan and get the OK (Gate 1)

Show a compact table: each list item → chosen product (`[id] name — size — price`) and a quantity.
**Compute the total with code, never by hand:**

```bash
printf '54180 1\n99211 2\n' | alcampo total -f -   # → per-line subtotals + total
```

`total` sums list prices; multi-buy promotions (2ª unidad, 3x2…) are applied only by the cart, so
the cart total can come out *lower*. Surface every assumption you marked in step 0, then wait for
the go-ahead.

### 3. Fill the cart (after the OK)

Fill the whole basket in **one write** with `set-many`, reusing the annotated lines from `total`:

```bash
alcampo cart set-many -f - --max 61 <<'EOF'
54180   1   # Leche semidesnatada Auchan 6x1l
99211   2   # Leche semidesnatada L.R. 6x1l
EOF
alcampo cart get                                # verify: names, qty × price, total
```

`set-many` sets **absolute** quantities (`0` removes). It reads the cart, plans the change, prices
the result, refuses before writing if over `--max`, then applies everything in a single call. For
small tweaks `cart add <id> <qty>` (additive) and `cart set <id> <qty>` (absolute) exist too. After
writing, run `cart get` and reconcile against the plan.

### 4. Prepare delivery (logged-in session; reversible)

```bash
alcampo checkout addresses                        # saved addresses (deliveryDestinationId)
alcampo checkout slots                            # slots for the cart's address (or --address <id>)
alcampo checkout reserve --slot <slotId>          # hold a slot
alcampo checkout summary --max 80                 # payable total: products + delivery
```

Pick a slot the user wants (each has a time window and a price), reserve it, then read the
summary for the authoritative total.

### 5. Pay (Gate 2 — explicit consent only)

Show the final order: line items, subtotal, delivery fee, total, and the slot. Only if the user
explicitly confirms *this* order:

```bash
alcampo checkout finish --max 80     # verifies the total ≤ 80€, prints the checkout URL
```

Give the user the URL; they review and pay in their browser (same account, same cart). If you
have any doubt about consent, stop and ask — a prepared cart with a reserved slot is harmless.

## Reference material

- `references/cli-reference.md` — every command, its flags, and the JSON shapes.
- `references/troubleshooting.md` — WAF challenges, expired/anonymous sessions, empty results,
  promos vs. `total`, and other failure modes.

## Why no all-in-one script

This skill drives the CLI step by step on purpose: the two safety gates need a human in the loop,
and product matching needs judgment. Keep the control flow in the conversation.
