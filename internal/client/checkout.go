package client

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Checkout on the Ocado platform hangs off the active session rather than a
// checkout object: pick a delivery address (a "delivery destination"), reserve
// a slot for it, and the active cart's checkout summary then carries the
// payable total including the delivery charge. These calls need a logged-in
// session. Payment itself goes through the shop's card processor (3-D Secure),
// so placing the order is handed off to the browser — see CheckoutURL.

// CheckoutURL is the web page where the reserved order is reviewed and paid.
func (c *Client) CheckoutURL() string { return c.BaseURL + "/checkout" }

// Addresses lists the customer's saved home-delivery addresses.
func (c *Client) Addresses() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.DoJSON(http.MethodGet, c.BaseURL+"/api/ecomdeliverydestinations/v4/delivery-addresses?deliveryMethod=HOME_DELIVERY", nil, &raw)
}

// Slots lists delivery slots for a delivery destination (address id). The
// region and shipping group come from the active cart.
func (c *Client) Slots(deliveryDestinationID, regionID, shippingGroupType string) (json.RawMessage, error) {
	// displayConfiguration and numberOfDays are what the desktop web app sends;
	// the API rejects the request (400) without them.
	body := map[string]any{
		"deliveryDestinationId": deliveryDestinationID,
		"regionId":              regionID,
		"shippingGroupType":     shippingGroupType,
		"displayConfiguration":  "DELIVERY_METHOD",
		"numberOfDays":          7,
	}
	var raw json.RawMessage
	return raw, c.DoJSON(http.MethodPost, c.BaseURL+"/api/ecomslots/v2/slots", body, &raw)
}

// ReserveSlot reserves a delivery slot for the session's order.
func (c *Client) ReserveSlot(slotID, deliveryDestinationID, regionID string) (json.RawMessage, error) {
	body := map[string]any{
		"slotId":                slotID,
		"deliveryDestinationId": deliveryDestinationID,
		"regionId":              regionID,
	}
	var raw json.RawMessage
	return raw, c.DoJSON(http.MethodPost, c.BaseURL+"/api/ecomslots/v1/slots/reservation", body, &raw)
}

// CheckoutSummary reads the active cart's checkout summary — the authoritative
// payable total (items + delivery) once a slot is reserved.
func (c *Client) CheckoutSummary() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.DoJSON(http.MethodGet, c.BaseURL+cartAPI+"/v1/carts/active/checkout-summary", nil, &raw)
}

// ExtractTotal pulls the payable total (in €) out of a cart, apply-quantity or
// checkout-summary response. Candidates are tried most-authoritative first —
// totals.totalPrice (items + delivery, on a checkout summary), then the items
// total after promos — each parsed on its own so one odd field can't hide the
// others. Returns false when no positive total is found.
func ExtractTotal(raw json.RawMessage) (float64, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return 0, false
	}
	if inner, ok := top["basketUpdateResult"]; ok { // apply-quantity wraps the cart
		if t, ok := ExtractTotal(inner); ok {
			return t, true
		}
	}
	var totals map[string]json.RawMessage
	if json.Unmarshal(top["totals"], &totals) != nil {
		return 0, false
	}
	for _, k := range []string{"totalPrice", "total", "itemPriceAfterPromos"} {
		if t, ok := moneyAmount(totals[k]); ok {
			return t, true
		}
	}
	return 0, false
}

// moneyAmount reads a positive euro amount from a Money object ({"amount":"5.28"})
// or a bare string/number.
func moneyAmount(b json.RawMessage) (float64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var m Money
	if json.Unmarshal(b, &m) == nil && m.Amount != "" {
		return parseMoney(m.Amount)
	}
	return parseMoney(strings.Trim(string(b), `"`))
}

// parseMoney reads a decimal euro string; false for empty/non-numeric/non-positive.
func parseMoney(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return f, true
}
