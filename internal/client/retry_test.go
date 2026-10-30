package client

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// seqRT returns the next status code from codes on each call (200 once exhausted).
// Throttle codes carry Retry-After: 0 so the backoff loop doesn't actually sleep.
type seqRT struct {
	codes []int
	calls int
}

func (s *seqRT) RoundTrip(*http.Request) (*http.Response, error) {
	code := 200
	if s.calls < len(s.codes) {
		code = s.codes[s.calls]
	}
	s.calls++
	h := http.Header{}
	if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
		h.Set("Retry-After", "0")
	}
	body := `{"ok":true}`
	if code == http.StatusAccepted { // what the AWS WAF challenge looks like
		h.Set("x-amzn-waf-action", "challenge")
		body = ""
	}
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     h,
	}, nil
}

func testClient(rt http.RoundTripper) *Client {
	c := New()
	c.HTTP = &http.Client{Transport: rt}
	c.MinInterval = 0
	return c
}

func TestDoJSONRetriesOnRateLimit(t *testing.T) {
	rt := &seqRT{codes: []int{429, 503, 200}}
	c := testClient(rt)
	var out map[string]any
	if err := c.DoJSON("GET", "https://x/api/", nil, &out); err != nil {
		t.Fatalf("expected success after backoff, got %v", err)
	}
	if rt.calls != 3 {
		t.Fatalf("expected 3 attempts (429,503,200), got %d", rt.calls)
	}
}

func TestDoJSONGivesUpAfterMaxRetries(t *testing.T) {
	rt := &seqRT{codes: []int{429, 429, 429, 429, 429}}
	c := testClient(rt)
	err := c.DoJSON("GET", "https://x/api/", nil, nil)
	if !isRateLimited(err) {
		t.Fatalf("expected a rate-limit error after exhausting retries, got %v", err)
	}
	if rt.calls != maxRetries+1 {
		t.Fatalf("expected %d attempts, got %d", maxRetries+1, rt.calls)
	}
}

func TestDoJSONDoesNotRetryOn4xx(t *testing.T) {
	rt := &seqRT{codes: []int{400, 200}}
	c := testClient(rt)
	err := c.DoJSON("GET", "https://x/api/", nil, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 400 {
		t.Fatalf("expected an immediate 400, got %v", err)
	}
	if rt.calls != 1 {
		t.Fatalf("a 400 must not be retried; got %d calls", rt.calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("2"); d != 2*time.Second {
		t.Fatalf("'2' → %v, want 2s", d)
	}
	if d := parseRetryAfter("0"); d != 0 {
		t.Fatalf("'0' → %v, want 0", d)
	}
	if d := parseRetryAfter(""); d != -1 {
		t.Fatalf("absent → %v, want -1 (sentinel)", d)
	}
	if d := parseRetryAfter("Wed, 21 Oct 2026 07:28:00 GMT"); d != -1 {
		t.Fatalf("HTTP-date → %v, want -1 (fall back to backoff)", d)
	}
}

func TestIsRateLimited(t *testing.T) {
	if !isRateLimited(&APIError{Status: 429}) || !isRateLimited(&APIError{Status: 503}) {
		t.Fatal("429/503 should be rate-limited")
	}
	if isRateLimited(&APIError{Status: 500}) || isRateLimited(&APIError{Status: 403}) || isRateLimited(nil) {
		t.Fatal("500/403/nil should not be rate-limited")
	}
}

func TestDoJSONMapsWAFChallenge(t *testing.T) {
	rt := &seqRT{codes: []int{202}}
	c := testClient(rt)
	err := c.DoJSON("GET", "https://x/api/", nil, nil)
	if !errors.Is(err, ErrWAFChallenge) {
		t.Fatalf("expected ErrWAFChallenge for a 202 challenge, got %v", err)
	}
	if rt.calls != 1 {
		t.Fatalf("a WAF challenge must not be retried; got %d calls", rt.calls)
	}
}

func TestIsWAFChallenge(t *testing.T) {
	h := http.Header{}
	if !isWAFChallenge(&http.Response{StatusCode: 202, Header: h}, nil) {
		t.Fatal("empty 202 is the WAF challenge")
	}
	if isWAFChallenge(&http.Response{StatusCode: 202, Header: h}, []byte(`{"ok":true}`)) {
		t.Fatal("a 202 with a body is a real API response")
	}
	h2 := http.Header{}
	h2.Set("x-amzn-waf-action", "challenge")
	if !isWAFChallenge(&http.Response{StatusCode: 405, Header: h2}, []byte("x")) {
		t.Fatal("the challenge header alone must be recognised")
	}
}

func TestOnChallengeRetriesOnceWhenSessionChanged(t *testing.T) {
	rt := &seqRT{codes: []int{202, 200}}
	c := testClient(rt)
	calls := 0
	c.OnChallenge = func() bool { calls++; return true }
	if err := c.DoJSON("GET", "https://x/api/", nil, nil); err != nil {
		t.Fatalf("expected success after re-sync, got %v", err)
	}
	if calls != 1 || rt.calls != 2 {
		t.Fatalf("hook calls=%d http calls=%d, want 1 and 2", calls, rt.calls)
	}

	rt2 := &seqRT{codes: []int{202, 202, 202}}
	c2 := testClient(rt2)
	c2.OnChallenge = func() bool { return false } // nothing new in the browser
	if err := c2.DoJSON("GET", "https://x/api/", nil, nil); !errors.Is(err, ErrWAFChallenge) || rt2.calls != 1 {
		t.Fatalf("no change → no retry; got err=%v calls=%d", err, rt2.calls)
	}
}
