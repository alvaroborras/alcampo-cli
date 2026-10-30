package client

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HARSession is the Alcampo session extracted from a browser HAR export
// (DevTools → Network → "Export HAR (with sensitive data)…"). The session is
// just cookies — global_sid (visitor + cart + login), aws-waf-token (the WAF
// clearance) and friends — so only request Cookie headers and response
// Set-Cookie headers are read; request bodies (which may hold a password) are
// never touched.
type HARSession struct {
	Cookies map[string]string
}

// ParseHAR folds, in capture order, every cookie the browser sent to or received
// from the shop host, so the result is the browser's final cookie state.
func ParseHAR(data []byte) (HARSession, error) {
	var har struct {
		Log struct {
			Entries []struct {
				Request struct {
					URL     string      `json:"url"`
					Headers []harHeader `json:"headers"`
				} `json:"request"`
				Response struct {
					Headers []harHeader `json:"headers"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &har); err != nil {
		return HARSession{}, fmt.Errorf("parse HAR JSON: %w", err)
	}

	s := HARSession{Cookies: map[string]string{}}
	for _, e := range har.Log.Entries {
		if !strings.Contains(e.Request.URL, "compraonline.alcampo.es") {
			continue
		}
		for _, h := range e.Request.Headers {
			if strings.EqualFold(h.Name, "cookie") {
				for k, v := range ParseCookieHeader(h.Value) {
					s.Cookies[k] = v
				}
			}
		}
		for _, h := range e.Response.Headers {
			if !strings.EqualFold(h.Name, "set-cookie") {
				continue
			}
			// HAR may fold several Set-Cookie values into one, newline-separated.
			for _, line := range strings.Split(h.Value, "\n") {
				first, _, _ := strings.Cut(line, ";")
				name, val, ok := strings.Cut(strings.TrimSpace(first), "=")
				if !ok || name == "" {
					continue
				}
				if val == "" {
					delete(s.Cookies, name)
				} else {
					s.Cookies[name] = val
				}
			}
		}
	}
	if s.Cookies[sessionCookie] == "" {
		return s, fmt.Errorf("no %s session cookie for compraonline.alcampo.es in the HAR — "+
			"export it from a tab where you're on the shop, with \"Export HAR (with sensitive data)\" "+
			"(the default sanitized export strips cookies)", sessionCookie)
	}
	return s, nil
}

type harHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// UseCookies replaces the client's session with the given cookies.
func (c *Client) UseCookies(cookies map[string]string) {
	c.mu.Lock()
	c.cookies = make(map[string]string, len(cookies))
	for k, v := range cookies {
		c.cookies[k] = v
	}
	c.csrf, c.dirty = "", true
	c.mu.Unlock()
}

// SyncCookies adopts a browser's current cookies as the session. Unlike
// UseCookies it keeps the CSRF token when the session cookie is unchanged, so
// syncing on every run doesn't cost an extra page fetch. Reports whether any
// cookie value changed.
func (c *Client) SyncCookies(cookies map[string]string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := len(cookies) != len(c.cookies)
	for k, v := range cookies {
		if c.cookies[k] != v {
			changed = true
		}
	}
	if !changed {
		return false
	}
	if cookies[sessionCookie] != c.cookies[sessionCookie] {
		c.csrf = ""
	}
	c.cookies = make(map[string]string, len(cookies))
	for k, v := range cookies {
		c.cookies[k] = v
	}
	c.dirty = true
	return true
}

// CookieNames lists the session's cookie names (never values) for reporting.
func (c *Client) CookieNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.cookies))
	for n := range c.cookies {
		names = append(names, n)
	}
	return names
}
