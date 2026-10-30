package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/alvaroborras/alcampo-cli/internal/client"
)

// fmtQty renders a quantity without a trailing ".0" (1.0 → "1", 0.5 → "0.5").
func fmtQty(q float64) string {
	return strconv.FormatFloat(q, 'f', -1, 64)
}

func cmdCart(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: alcampo cart <get|add|set|set-many|clear> [flags] [args]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("cart", flag.ExitOnError)
	cf := addCommon(fs)
	maxFlag := fs.Float64("max", 0, "refuse if the resulting cart total exceeds this many € (0 = env/config)")
	file := fs.String("f", "", "set-many: file with one '<id> <qty>' per line ('-' for stdin; 0 removes)")
	_ = fs.Parse(reorderArgs(fs, rest))
	// The cart lives in the session cookie, so no login is needed: an anonymous
	// session has a cart too, and logging in later merges it into the account's.
	cl := newClient(cf)
	switch sub {
	case "get":
		return cartGet(cl, cf)
	case "clear":
		return cartClear(cl, cf)
	case "add", "set":
		a := fs.Args()
		if len(a) != 2 {
			return fmt.Errorf("usage: alcampo cart %s <product_id> <qty>", sub)
		}
		qty, perr := strconv.ParseFloat(a[1], 64)
		if perr != nil || qty < 0 || (sub == "add" && qty == 0) {
			return fmt.Errorf("invalid qty %q", a[1])
		}
		return cartApply(cl, cf, []cartChange{{id: a[0], qty: qty, add: sub == "add"}}, *maxFlag, sub)
	case "set-many":
		changes, cerr := collectSetMany(*file, fs.Args())
		if cerr != nil {
			return cerr
		}
		if len(changes) == 0 {
			return fmt.Errorf("no changes (use -f file/stdin with '<id> <qty>' lines, or '<id> <qty> ...' args)")
		}
		return cartApply(cl, cf, changes, *maxFlag, "set-many")
	default:
		return fmt.Errorf("unknown cart subcommand %q (get|add|set|set-many|clear)", sub)
	}
}

// ---- read ----

func cartGet(cl *client.Client, cf *common) error {
	cart, raw, err := cl.GetCart()
	if err != nil {
		return err
	}
	if cf.jsonOut {
		return emitRaw(raw)
	}
	cl.DecorateCart(cart)
	fmt.Printf("carrito: %d productos, total %s€\n", len(cart.Items), orDefault(cart.Totals.ItemPriceAfterPromos.Amount, "0.00"))
	for _, it := range cart.Items {
		printCartLine(it)
	}
	if s := cart.Totals.SavingsPrice.Amount; s != "" && s != "0.00" {
		fmt.Printf("  ahorro en promociones: %s€\n", s)
	}
	printRestrictions(cart)
	return nil
}

// printCartLine renders one line as '[id] name — qty × unit = subtotal'.
func printCartLine(it client.CartItem) {
	id := orDefault(it.RetailerProductID, it.ProductID)
	name := orDefault(it.Name, "producto")
	fmt.Printf("  [%s] %s — %s × %s€ = %s€\n", id, name, fmtQty(it.Quantity), orDefault(it.Price.Amount, "?"), orDefault(it.TotalPrices.FinalPrice.Amount, "?"))
}

// printRestrictions explains why the cart can't be checked out yet, translating
// the platform's restriction codes. Advisory only.
func printRestrictions(cart *client.Cart) {
	if len(cart.Items) == 0 {
		return
	}
	for _, r := range cart.DefaultCheckoutGroup.CheckoutRestrictions {
		switch r {
		case "EMPTY_BASKET":
			// obvious from the empty list
		case "NOT_REACHED_THRESHOLD":
			fmt.Printf("  ⚠ no alcanza el pedido mínimo (%s€)\n", cart.DefaultCheckoutGroup.MinimumCheckoutThreshold.Amount)
		case "MISSING_SLOT":
			fmt.Println("  ⚠ falta reservar franja de entrega (`alcampo checkout slots`)")
		default:
			fmt.Printf("  ⚠ restricción de checkout: %s\n", r)
		}
	}
}

// ---- write ----

func cartClear(cl *client.Client, cf *common) error {
	cart, _, err := cl.GetCart()
	if err != nil {
		return err
	}
	n := len(cart.Items)
	if n == 0 {
		fmt.Println("✓ el carrito ya está vacío")
		return nil
	}
	if err := cl.ClearCart(); err != nil {
		return err
	}
	if cf.jsonOut {
		return emitJSON(map[string]any{"cleared": n})
	}
	fmt.Printf("✓ carrito vaciado (%d productos eliminados)\n", n)
	return nil
}

// cartChange is one desired mutation: an additive bump (add=true) or an absolute
// quantity set (add=false; qty 0 removes the line). id is as the user typed it
// (retailer id or UUID).
type cartChange struct {
	id  string
	qty float64
	add bool
}

// cartApply is the shared GET → resolve ids → plan deltas → (preventive) budget
// guard → single write flow behind add/set/set-many. Pricing the resulting basket
// locally lets --max refuse BEFORE the write, and a final authoritative check
// rolls the change back if the API total still breaches the cap.
func cartApply(cl *client.Client, cf *common, changes []cartChange, maxFlag float64, action string) error {
	cart, _, err := cl.GetCart()
	if err != nil {
		return err
	}

	// Map every id the user typed to its product (and so its UUID + price).
	ids := make([]string, len(changes))
	for i, ch := range changes {
		ids[i] = ch.id
	}
	products, errs := cl.Lookup(ids, lookupConcurrency())
	for _, ch := range changes {
		if e := errs[ch.id]; e != nil {
			return fmt.Errorf("resolve product %s: %w", ch.id, e)
		}
	}
	planned := make([]client.Change, len(changes))
	for i, ch := range changes {
		planned[i] = client.Change{ProductID: products[ch.id].ProductID, Quantity: ch.qty, Add: ch.add}
	}
	deltas, final := client.PlanDeltas(cart.Items, planned)
	if len(deltas) == 0 {
		fmt.Println("✓ sin cambios (el carrito ya tiene esas cantidades)")
		return nil
	}

	maxEUR, err := resolveMax(maxFlag)
	if err != nil {
		return err
	}
	est := priceBasket(final, cart.Items, products)
	if maxEUR > 0 && est.complete && est.eur() > maxEUR {
		return fmt.Errorf("BUDGET EXCEEDED (estimated ≈%.2f€ > %.2f€ limit): not writing %s — raise the cap with --max, ALCAMPO_MAX_EUR, or [limits].max_eur", est.eur(), maxEUR, action)
	}

	res, raw, err := cl.ApplyQuantities(deltas)
	if err != nil {
		return err
	}

	// Authoritative backstop: if the real total breaches the cap, undo the
	// change so we never leave the cart over budget.
	if maxEUR > 0 {
		if total, ok := client.ExtractTotal(raw); ok && total > maxEUR {
			_, _, _ = cl.ApplyQuantities(client.Invert(deltas))
			return fmt.Errorf("BUDGET EXCEEDED: %s landed at %.2f€, over the %.2f€ limit — reverted the cart (raise --max to proceed)", action, total, maxEUR)
		}
	}

	if cf.jsonOut {
		return emitRaw(raw)
	}
	printApplyResult(res, changes, products, action)
	return nil
}

// basketPrice is a local estimate of a basket's total (summed in integer cents).
// complete is false when any line couldn't be priced (then the estimate is a
// lower bound and the guard defers to the authoritative post-write total).
type basketPrice struct {
	totalCents int64
	complete   bool
}

func (b *basketPrice) eur() float64 { return float64(b.totalCents) / 100 }

// priceBasket sums unit price × qty over the final quantities (keyed by UUID),
// taking prices from the current cart lines and the freshly looked-up products.
func priceBasket(final map[string]float64, current []client.CartItem, looked map[string]client.Product) *basketPrice {
	unit := map[string]string{}
	for _, it := range current {
		unit[it.ProductID] = it.Price.Amount
	}
	for _, p := range looked {
		unit[p.ProductID] = p.Price.Amount
	}
	bp := &basketPrice{complete: true}
	for id, q := range final {
		cents, err := priceCents(unit[id])
		if err != nil {
			bp.complete = false
			continue
		}
		bp.totalCents += int64(math.Round(float64(cents) * q))
	}
	return bp
}

// printApplyResult prints the concise human summary after a write: the new cart
// total/count plus the touched line(s), from the authoritative write response.
func printApplyResult(res *client.ApplyResult, changes []cartChange, products map[string]client.Product, action string) {
	items := res.Items()
	qty := map[string]float64{}
	for _, it := range items {
		qty[it.ProductID] = it.Quantity
	}
	total := orDefault(res.BasketUpdateResult.Totals.ItemPriceAfterPromos.Amount, "?")
	lineFor := func(ch cartChange) string {
		p := products[ch.id]
		if q, ok := qty[p.ProductID]; ok {
			return fmt.Sprintf("[%s] %s → x%s", p.ID(), p.Name, fmtQty(q))
		}
		return fmt.Sprintf("[%s] %s → (eliminado)", p.ID(), p.Name)
	}
	if action == "set-many" {
		fmt.Printf("✓ set-many: %d cambios  |  carrito: %d productos, total %s€\n", len(changes), len(items), total)
		for _, ch := range changes {
			fmt.Printf("    %s\n", lineFor(ch))
		}
		return
	}
	fmt.Printf("✓ %s %s  |  carrito: %d productos, total %s€\n", action, lineFor(changes[0]), len(items), total)
}

// ---- set-many input parsing ----

// collectSetMany reads desired '<id> <qty>' changes from a file/stdin (-f) or,
// with no file, from positional '<id> <qty>' pairs. Unlike the `total` basket
// reader, qty 0 is allowed and means "remove that line".
func collectSetMany(file string, posArgs []string) ([]cartChange, error) {
	if file != "" {
		return readChangesFile(file)
	}
	if len(posArgs)%2 != 0 {
		return nil, fmt.Errorf("set-many positional args must be '<id> <qty>' pairs (got %d tokens) — or use -f file", len(posArgs))
	}
	changes := make([]cartChange, 0, len(posArgs)/2)
	for i := 0; i < len(posArgs); i += 2 {
		q, err := strconv.ParseFloat(posArgs[i+1], 64)
		if err != nil || q < 0 {
			return nil, fmt.Errorf("invalid qty %q for id %q (want a number ≥ 0; 0 removes)", posArgs[i+1], posArgs[i])
		}
		changes = append(changes, cartChange{id: posArgs[i], qty: q})
	}
	return changes, nil
}

func readChangesFile(file string) ([]cartChange, error) {
	var r io.Reader
	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var changes []cartChange
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	ln := 0
	for sc.Scan() {
		ln++
		t := stripComment(sc.Text())
		if t == "" {
			continue
		}
		f := strings.Fields(t)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: expected '<id> <qty>', got %q", ln, t)
		}
		q, err := strconv.ParseFloat(f[1], 64)
		if err != nil || q < 0 {
			return nil, fmt.Errorf("line %d: invalid qty %q (want a number ≥ 0; 0 removes)", ln, f[1])
		}
		changes = append(changes, cartChange{id: f[0], qty: q})
	}
	return changes, sc.Err()
}
