package client

import (
	"encoding/json"
	"net/http"
)

// The cart belongs to the session cookie, not to an account: an anonymous
// visitor has one too, and logging in merges it into the customer's. So every
// cart command works without login as long as the session is kept (it is cached
// in ~/.alcampo/session.json).

const cartAPI = "/api/cart"

// Cart mirrors GET /api/cart/v1/carts/active.
type Cart struct {
	CartID                string     `json:"cartId"`
	RegionID              string     `json:"regionId"`
	DeliveryDestinationID string     `json:"deliveryDestinationId"`
	Items                 []CartItem `json:"items"`
	ItemGroups            []struct {
		Items []CartItem `json:"items"`
	} `json:"itemGroups"`
	Totals               CartTotals `json:"totals"`
	DefaultCheckoutGroup struct {
		ShippingGroupType        string   `json:"shippingGroupType"`
		CanCheckout              bool     `json:"canCheckout"`
		CheckoutRestrictions     []string `json:"checkoutRestrictions"`
		MinimumCheckoutThreshold Money    `json:"minimumCheckoutThreshold"`
	} `json:"defaultCheckoutGroup"`
}

// CartTotals is the money summary of a cart (or of an apply-quantity result).
type CartTotals struct {
	ItemPriceAfterPromos Money `json:"itemPriceAfterPromos"` // what the items cost, promos applied
	ItemsRetailPrice     Money `json:"itemsRetailPrice"`     // before promos
	SavingsPrice         Money `json:"savingsPrice"`
}

// CartItem is one line. Price is the unit price; TotalPrices.FinalPrice the line
// total after promos. The API returns only the productId UUID, so the display
// fields (RetailerProductID, Name) are filled in by DecorateCart.
type CartItem struct {
	ProductID   string  `json:"productId"`
	Quantity    float64 `json:"quantity"`
	Price       Money   `json:"price"`
	TotalPrices struct {
		FinalPrice Money `json:"finalPrice"`
	} `json:"totalPrices"`

	RetailerProductID string `json:"retailerProductId,omitempty"`
	Name              string `json:"name,omitempty"`
}

// Lines returns the cart's items, from the flat list or, failing that, the
// grouped view (some responses only populate one of them).
func (c *Cart) Lines() []CartItem {
	if len(c.Items) > 0 {
		return c.Items
	}
	var out []CartItem
	for _, g := range c.ItemGroups {
		out = append(out, g.Items...)
	}
	return out
}

// GetCart returns the projected active cart plus the full raw JSON.
func (c *Client) GetCart() (*Cart, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodGet, c.BaseURL+cartAPI+"/v1/carts/active", nil, &raw); err != nil {
		return nil, nil, err
	}
	var cart Cart
	_ = json.Unmarshal(raw, &cart) // best-effort projection; raw is the source of truth
	if len(cart.Items) == 0 {
		cart.Items = cart.Lines()
	}
	return &cart, raw, nil
}

// DecorateCart fills in each line's retailer id and name with one product
// lookup. Best-effort: on failure the lines keep their UUIDs.
func (c *Client) DecorateCart(cart *Cart) {
	ids := make([]string, 0, len(cart.Items))
	for _, it := range cart.Items {
		ids = append(ids, it.ProductID)
	}
	ps, err := c.Decorate(ids)
	if err != nil {
		return
	}
	byID := make(map[string]Product, len(ps))
	for _, p := range ps {
		byID[p.ProductID] = p
	}
	for i := range cart.Items {
		if p, ok := byID[cart.Items[i].ProductID]; ok {
			cart.Items[i].RetailerProductID = p.RetailerProductID
			cart.Items[i].Name = p.Name
		}
	}
}

// QuantityDelta is one apply-quantity entry. Quantity is RELATIVE: +2 adds two,
// -1 removes one, and overshooting below zero removes the line.
type QuantityDelta struct {
	ProductID string  `json:"productId"`
	Quantity  float64 `json:"quantity"`
}

// ApplyResult is the projected apply-quantity response.
type ApplyResult struct {
	BasketUpdateResult struct {
		ItemGroups []struct {
			Items []CartItem `json:"items"`
		} `json:"itemGroups"`
		Totals CartTotals `json:"totals"`
	} `json:"basketUpdateResult"`
}

// Items returns the cart lines after the update.
func (r *ApplyResult) Items() []CartItem {
	var out []CartItem
	for _, g := range r.BasketUpdateResult.ItemGroups {
		out = append(out, g.Items...)
	}
	return out
}

// ApplyQuantities applies many relative changes in ONE write.
func (c *Client) ApplyQuantities(deltas []QuantityDelta) (*ApplyResult, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodPost, c.BaseURL+cartAPI+"/v1/carts/active/apply-quantity", deltas, &raw); err != nil {
		return nil, nil, err
	}
	var res ApplyResult
	_ = json.Unmarshal(raw, &res)
	return &res, raw, nil
}

// ClearCart empties the active cart in one call.
func (c *Client) ClearCart() error {
	return c.DoJSON(http.MethodDelete, c.BaseURL+cartAPI+"/v1/carts/active/items", nil, nil)
}

// Change is one desired cart mutation: an additive bump (Add=true) or an
// absolute quantity (Add=false; 0 removes the line).
type Change struct {
	ProductID string // UUID
	Quantity  float64
	Add       bool
}

// PlanDeltas folds desired changes against the current lines into the relative
// deltas apply-quantity wants, plus the resulting absolute quantities. Several
// changes to one product compose in order; zero deltas are dropped.
func PlanDeltas(current []CartItem, changes []Change) (deltas []QuantityDelta, final map[string]float64) {
	final = map[string]float64{}
	for _, it := range current {
		final[it.ProductID] = it.Quantity
	}
	before := make(map[string]float64, len(final))
	for k, v := range final {
		before[k] = v
	}
	var order []string
	seen := map[string]bool{}
	for _, ch := range changes {
		q := ch.Quantity
		if ch.Add {
			q += final[ch.ProductID]
		}
		if q < 0 {
			q = 0
		}
		final[ch.ProductID] = q
		if !seen[ch.ProductID] {
			seen[ch.ProductID] = true
			order = append(order, ch.ProductID)
		}
	}
	for _, id := range order {
		if d := final[id] - before[id]; d != 0 {
			deltas = append(deltas, QuantityDelta{ProductID: id, Quantity: d})
		}
	}
	for id, q := range final {
		if q <= 0 {
			delete(final, id)
		}
	}
	return deltas, final
}

// Invert returns the deltas that undo ds (used to roll back a write that landed
// over budget).
func Invert(ds []QuantityDelta) []QuantityDelta {
	out := make([]QuantityDelta, len(ds))
	for i, d := range ds {
		out[i] = QuantityDelta{ProductID: d.ProductID, Quantity: -d.Quantity}
	}
	return out
}
