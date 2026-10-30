package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/alvaroborras/alcampo-cli/internal/config"
)

const sessionCacheFile = "session.json"

// sessionCookie is the Ocado platform's session cookie. It identifies the
// visitor (anonymous or logged in) and therefore owns the active cart.
const sessionCookie = "global_sid"

// Session is the on-disk session cache: the cookie jar plus the CSRF token that
// goes with it. Rotated cookies are written back after every response, so an
// anonymous cart built in one run is still there in the next.
type Session struct {
	Cookies map[string]string `json:"cookies"`
	CSRF    string            `json:"csrf,omitempty"`
}

// LoadSession hydrates the client from the cached session and arranges for
// later cookie rotations to be saved back. Returns false if none is cached.
func (c *Client) LoadSession() bool {
	var s Session
	err := config.Load(sessionCacheFile, &s)
	c.Persist = func() { _ = c.SaveSession() }
	if err != nil || len(s.Cookies) == 0 {
		return false
	}
	c.mu.Lock()
	c.cookies, c.csrf = s.Cookies, s.CSRF
	c.mu.Unlock()
	return true
}

// SaveSession writes the cookie jar + CSRF token (0600) if they changed.
func (c *Client) SaveSession() error {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	s := Session{Cookies: make(map[string]string, len(c.cookies)), CSRF: c.csrf}
	for k, v := range c.cookies {
		s.Cookies[k] = v
	}
	c.dirty = false
	c.mu.Unlock()
	return config.Save(sessionCacheFile, s)
}

// Customer is the projected /api/customer/v3/customers/current response.
type Customer struct {
	ID        string `json:"customerId"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// Me fetches the logged-in customer. An anonymous session gets a 401/403, which
// is also the cheapest way to tell whether an imported session authenticates.
func (c *Client) Me() (*Customer, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.DoJSON(http.MethodGet, c.BaseURL+"/api/customer/v3/customers/current", nil, &raw); err != nil {
		return nil, nil, err
	}
	var cu Customer
	_ = json.Unmarshal(raw, &cu)
	return &cu, raw, nil
}

// LoggedIn reports whether the session belongs to a logged-in customer.
func (c *Client) LoggedIn() (bool, error) {
	if !c.HasSession() {
		return false, nil
	}
	_, _, err := c.Me()
	if err == nil {
		return true, nil
	}
	var ae *APIError
	if errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
		return false, nil
	}
	return false, fmt.Errorf("check login: %w", err)
}
