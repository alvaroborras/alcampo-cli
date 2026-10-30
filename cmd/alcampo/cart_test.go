package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alvaroborras/alcampo-cli/internal/client"
)

func TestCollectSetManyPositional(t *testing.T) {
	ch, err := collectSetMany("", []string{"10", "2", "20", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 2 {
		t.Fatalf("want 2 changes, got %d", len(ch))
	}
	if ch[0].id != "10" || ch[0].qty != 2 || ch[0].add {
		t.Fatalf("change 0 wrong: %+v", ch[0])
	}
	if ch[1].id != "20" || ch[1].qty != 0 { // qty 0 allowed (= remove)
		t.Fatalf("change 1 wrong (qty 0 must be allowed): %+v", ch[1])
	}
}

func TestCollectSetManyErrors(t *testing.T) {
	cases := map[string][]string{
		"odd token count": {"10"},
		"non-numeric qty": {"10", "x"},
		"negative qty":    {"10", "-1"},
	}
	for name, args := range cases {
		if _, err := collectSetMany("", args); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}

func TestReadChangesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "changes.txt")
	// whole-line comment, a trailing '# name' annotation, a blank line, and a plain line.
	body := "# a comment\n10 2  # Leche entera\n20 0\n\n30 1.5\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := readChangesFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []cartChange{{id: "10", qty: 2}, {id: "20", qty: 0}, {id: "30", qty: 1.5}}
	if len(ch) != len(want) {
		t.Fatalf("want %d changes, got %d (%+v)", len(want), len(ch), ch)
	}
	for i, w := range want {
		if ch[i] != w {
			t.Errorf("change %d = %+v, want %+v", i, ch[i], w)
		}
	}
}

// priceBasket prices the final quantities from the current cart lines and the
// looked-up products (lookups win, being fresher) — no network involved.
func TestPriceBasket(t *testing.T) {
	current := []client.CartItem{
		{ProductID: "u1", Quantity: 1, Price: client.Money{Amount: "1.20"}},
		{ProductID: "u2", Quantity: 1, Price: client.Money{Amount: "6.00"}},
	}
	looked := map[string]client.Product{
		"54180": {ProductID: "u3", Price: client.Money{Amount: "5.28"}},
	}
	final := map[string]float64{"u1": 2, "u2": 1, "u3": 1} // 2.40 + 6.00 + 5.28
	bp := priceBasket(final, current, looked)
	if !bp.complete || bp.totalCents != 1368 {
		t.Fatalf("got complete=%v cents=%d, want true/1368", bp.complete, bp.totalCents)
	}
}

func TestPriceBasketFractionalQty(t *testing.T) {
	current := []client.CartItem{{ProductID: "u9", Price: client.Money{Amount: "10.00"}}}
	bp := priceBasket(map[string]float64{"u9": 0.35}, current, nil) // 0.35 × 10.00 = 3.50
	if !bp.complete || bp.totalCents != 350 {
		t.Fatalf("got complete=%v cents=%d, want true/350", bp.complete, bp.totalCents)
	}
}

func TestPriceBasketIncompleteWhenUnpriced(t *testing.T) {
	bp := priceBasket(map[string]float64{"unknown": 1}, nil, nil)
	if bp.complete {
		t.Fatal("a line with no known price must mark the estimate incomplete")
	}
}
