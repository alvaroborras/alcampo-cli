package client

import "testing"

func TestExtractTotal(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want float64
		ok   bool
	}{
		{"cart (live shape)", `{"cartId":"x","totals":{"itemPriceAfterPromos":{"currency":"EUR","amount":"15.84"},"itemsRetailPrice":{"currency":"EUR","amount":"15.84"}}}`, 15.84, true},
		{"apply-quantity wraps the cart", `{"basketUpdateResult":{"totals":{"itemPriceAfterPromos":{"currency":"EUR","amount":"10.56"}}}}`, 10.56, true},
		{"checkout totalPrice wins over items", `{"totals":{"totalPrice":{"amount":"76.84"},"itemPriceAfterPromos":{"amount":"68.64"}}}`, 76.84, true},
		{"bare string amount", `{"totals":{"total":"12.50"}}`, 12.5, true},
		{"empty cart is not a total", `{"totals":{"itemPriceAfterPromos":{"amount":"0.00"}}}`, 0, false},
		{"no totals", `{"cartId":"x"}`, 0, false},
		{"not json", `nope`, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ExtractTotal([]byte(c.raw))
			if ok != c.ok || got != c.want {
				t.Fatalf("ExtractTotal = %v,%v want %v,%v", got, ok, c.want, c.ok)
			}
		})
	}
}
