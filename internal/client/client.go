// Package client is a thin, dependency-free Go client for the (unofficial)
// www.compraonline.alcampo.es API. Alcampo's web shop runs on the Ocado Smart
// Platform: a cookie session (global_sid) carries the visitor — anonymous or
// logged in — and its cart, and every write needs the per-session CSRF token
// embedded in the HTML shell. It speaks the same HTTP shapes the web app does.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BaseURL is the web shop host; the API lives under /api/<service>/.
	BaseURL = "https://www.compraonline.alcampo.es"
	// DefaultUA mirrors a current desktop Chrome so requests look like the web app.
	DefaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
)

// Client is the API entrypoint. The zero value is not usable — use New.
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string

	mu      sync.Mutex
	cookies map[string]string // session cookie jar (global_sid, VISITORID, aws-waf-token, …)
	csrf    string            // X-CSRF-TOKEN for writes; bound to the session
	dirty   bool              // cookies/csrf changed since the last save

	// Persist, when set, is called after a response changes the session so the
	// rotated cookies survive across CLI runs (see LoadSession/SaveSession).
	Persist func()

	// MinInterval spaces request starts at least this far apart. The shop's AWS
	// WAF challenges bursty clients, so even concurrent lookups are paced.
	MinInterval time.Duration
	paceMu      sync.Mutex
	nextSlot    time.Time

	// OnChallenge, when set, is called once when the WAF challenges a request.
	// It may refresh the session from the browser (which keeps its clearance
	// token fresh) and reports whether anything changed; if so the request is
	// retried once.
	OnChallenge func() bool
}

// New returns a Client with web-app-like defaults and an empty (anonymous)
// session. Its HTTP client presents Chrome's TLS (JA3) fingerprint via uTLS; if
// that fails to initialize it falls back to the stdlib transport.
func New() *Client {
	hc := &http.Client{Timeout: 30 * time.Second}
	if tr, err := newChromeTransport(); err == nil {
		hc.Transport = tr
	}
	return &Client{
		HTTP:        hc,
		BaseURL:     BaseURL,
		UserAgent:   DefaultUA,
		cookies:     map[string]string{},
		MinInterval: 250 * time.Millisecond,
	}
}

// pace blocks until this request may start, reserving the next slot.
func (c *Client) pace() {
	if c.MinInterval <= 0 {
		return
	}
	c.paceMu.Lock()
	now := time.Now()
	wait := c.nextSlot.Sub(now)
	if wait < 0 {
		wait = 0
	}
	c.nextSlot = now.Add(wait + c.MinInterval)
	c.paceMu.Unlock()
	time.Sleep(wait)
}

// APIError carries a non-2xx response so callers (and agents) can branch on it.
// RetryAfter is the parsed Retry-After header on a 429/503 (>=0 when the server
// sent one, -1 when absent), used to pace automatic backoff.
type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("alcampo api: HTTP %d: %s", e.Status, truncate(e.Body, 300))
}

// ErrWAFChallenge is returned when AWS WAF (in front of the shop) answers with a
// JavaScript challenge instead of data: HTTP 202, empty body, and an
// x-amzn-waf-action: challenge header. It needs a browser to solve, so the fix is
// importing a browser session that carries a fresh aws-waf-token cookie.
var ErrWAFChallenge = errors.New("blocked by Alcampo's AWS WAF bot challenge (HTTP 202 x-amzn-waf-action: challenge; " +
	"usually too many requests in a few minutes) — wait ~5 minutes, or open www.compraonline.alcampo.es in a browser " +
	"and import its cookies (incl. aws-waf-token) with `alcampo import-curl` / `alcampo import-har`")

func (c *Client) newReq(method, url string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json; charset=utf-8")
	req.Header.Set("accept-language", "es-ES,es;q=0.9,en;q=0.8")
	req.Header.Set("user-agent", c.UserAgent)
	req.Header.Set("referer", c.BaseURL+"/")
	req.Header.Set("origin", c.BaseURL)
	req.Header.Set("ecom-request-source", "web")
	if body != nil {
		req.Header.Set("content-type", "application/json; charset=utf-8")
	}
	c.mu.Lock()
	if method != http.MethodGet && c.csrf != "" {
		req.Header.Set("x-csrf-token", c.csrf)
	}
	if ck := c.cookieHeaderLocked(); ck != "" {
		req.Header.Set("cookie", ck)
	}
	c.mu.Unlock()
	return req, nil
}

// Automatic backoff on throttling (HTTP 429/503): retry up to maxRetries times,
// honouring Retry-After, else exponential backoff with jitter, each wait capped.
const (
	maxRetries       = 3
	defaultRetryBase = 500 * time.Millisecond
	maxRetryWait     = 10 * time.Second
)

// isRateLimited reports whether err is a throttling response worth backing off on.
func isRateLimited(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusTooManyRequests || ae.Status == http.StatusServiceUnavailable)
}

// parseRetryAfter reads the delta-seconds form of Retry-After; -1 when absent or in
// the (rare here) HTTP-date form, so the caller falls back to its own backoff.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return -1
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return -1
}

// retryWait is how long to wait before the next attempt: the server's Retry-After
// if it sent one, else exponential backoff plus jitter — both capped.
func retryWait(err error, backoff time.Duration) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter >= 0 {
		return capWait(ae.RetryAfter)
	}
	return capWait(backoff + time.Duration(rand.Int63n(int64(250*time.Millisecond))))
}

func capWait(d time.Duration) time.Duration {
	if d > maxRetryWait {
		return maxRetryWait
	}
	if d < 0 {
		return 0
	}
	return d
}

// doWithBackoff issues a request, retrying transient throttling (429/503) so a burst
// of calls — e.g. pricing a fresh basket, or many searches during a shop — degrades
// gracefully instead of failing. Non-throttle errors return immediately.
func (c *Client) doWithBackoff(method, url string, body, out any) error {
	backoff := defaultRetryBase
	challenged := false
	for attempt := 0; ; attempt++ {
		err := c.doOnce(method, url, body, out)
		if errors.Is(err, ErrWAFChallenge) && !challenged && c.OnChallenge != nil {
			challenged = true
			if c.OnChallenge() {
				err = c.doOnce(method, url, body, out)
			}
		}
		if !isRateLimited(err) || attempt >= maxRetries {
			return err
		}
		time.Sleep(retryWait(err, backoff))
		backoff *= 2
	}
}

// DoJSON performs an API request with automatic throttle backoff. Writes first
// make sure a CSRF token is in hand; if one is rejected (it is bound to the
// session, which may have rotated) the token is refreshed once and retried.
func (c *Client) DoJSON(method, url string, body, out any) error {
	if method != http.MethodGet {
		if err := c.EnsureCSRF(); err != nil {
			return err
		}
	}
	err := c.doWithBackoff(method, url, body, out)
	if method != http.MethodGet && isCSRFFailure(err) {
		if rerr := c.RefreshCSRF(); rerr == nil {
			return c.doWithBackoff(method, url, body, out)
		}
	}
	return err
}

// isCSRFFailure reports a write rejected for its CSRF token (403 mentioning csrf).
func isCSRFFailure(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden && strings.Contains(strings.ToLower(ae.Body), "csrf")
}

// doOnce performs a single API request, decoding a 2xx JSON body into out (if
// non-nil). Non-2xx responses become *APIError; a WAF challenge is ErrWAFChallenge.
func (c *Client) doOnce(method, url string, body, out any) error {
	data, _, err := c.send(method, url, body)
	if err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s: %w", url, err)
		}
	}
	return nil
}

// send issues one request and returns the body, absorbing any Set-Cookie into
// the session. It maps the WAF challenge and non-2xx statuses to errors.
func (c *Client) send(method, url string, body any) ([]byte, *http.Response, error) {
	req, err := c.newReq(method, url, body)
	if err != nil {
		return nil, nil, err
	}
	c.pace()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	c.absorbCookies(resp)
	if isWAFChallenge(resp, data) {
		return nil, resp, ErrWAFChallenge
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, &APIError{Status: resp.StatusCode, Body: string(data), RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	return data, resp, nil
}

// isWAFChallenge spots the WAF's challenge: its explicit header, or the bare
// empty-bodied 202 it answers with (a real API 202 carries a body).
func isWAFChallenge(resp *http.Response, body []byte) bool {
	if strings.EqualFold(resp.Header.Get("x-amzn-waf-action"), "challenge") {
		return true
	}
	return resp.StatusCode == http.StatusAccepted && len(bytes.TrimSpace(body)) == 0
}

// ---- session: cookies + CSRF ----

// reCSRF finds the session's CSRF token in the server-rendered __INITIAL_STATE__.
var reCSRF = regexp.MustCompile(`"csrf":\{"token":"([^"]+)"`)

// EnsureCSRF fetches a CSRF token if the session doesn't have one yet.
func (c *Client) EnsureCSRF() error {
	c.mu.Lock()
	have := c.csrf != ""
	c.mu.Unlock()
	if have {
		return nil
	}
	return c.RefreshCSRF()
}

// RefreshCSRF loads the HTML shell (which also establishes the session cookies
// on a fresh client) and pulls the CSRF token out of its initial state.
func (c *Client) RefreshCSRF() error {
	data, _, err := c.send(http.MethodGet, c.BaseURL+"/", nil)
	if err != nil {
		return fmt.Errorf("fetch csrf token: %w", err)
	}
	m := reCSRF.FindSubmatch(data)
	if m == nil {
		return fmt.Errorf("csrf token not found in %s/ page", c.BaseURL)
	}
	c.mu.Lock()
	c.csrf, c.dirty = string(m[1]), true
	c.mu.Unlock()
	c.persist()
	return nil
}

// absorbCookies folds a response's Set-Cookie headers into the session jar
// (expired/empty values delete), then persists if anything changed.
func (c *Client) absorbCookies(resp *http.Response) {
	cks := resp.Cookies()
	if len(cks) == 0 {
		return
	}
	c.mu.Lock()
	for _, ck := range cks {
		prev := c.cookies[ck.Name]
		if ck.MaxAge < 0 || ck.Value == "" || (!ck.Expires.IsZero() && ck.Expires.Before(time.Now())) {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
		if ck.Name == sessionCookie && c.cookies[ck.Name] != prev {
			c.csrf = "" // a new session invalidates the old token
		}
	}
	c.dirty = true
	c.mu.Unlock()
	c.persist()
}

func (c *Client) persist() {
	if c.Persist != nil {
		c.Persist()
	}
}

// cookieHeaderLocked renders the jar as a Cookie header (sorted for stability).
func (c *Client) cookieHeaderLocked() string {
	if len(c.cookies) == 0 {
		return ""
	}
	names := make([]string, 0, len(c.cookies))
	for n := range c.cookies {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + "=" + c.cookies[n]
	}
	return strings.Join(parts, "; ")
}

// SetCookieHeader replaces the session with a raw Cookie header (e.g. from a
// browser's "Copy as cURL"). The CSRF token is re-derived on the next write.
func (c *Client) SetCookieHeader(raw string) {
	c.UseCookies(ParseCookieHeader(raw))
}

// HasSession reports whether a session cookie is present.
func (c *Client) HasSession() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cookies[sessionCookie] != ""
}

// ParseCookieHeader splits "a=1; b=2" into a map (last value wins).
func ParseCookieHeader(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		name, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(val)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
