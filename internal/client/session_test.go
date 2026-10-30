package client

import (
	"net/http"
	"testing"
)

func TestParseCookieHeader(t *testing.T) {
	got := ParseCookieHeader(" global_sid=abc; VISITORID=v=1 ;bad; =x; aws-waf-token=t ")
	want := map[string]string{"global_sid": "abc", "VISITORID": "v=1", "aws-waf-token": "t"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// Set-Cookie rotations must land in the jar (and a new session must drop the
// CSRF token bound to the old one), and the session must round-trip to disk.
func TestSessionAbsorbAndPersist(t *testing.T) {
	t.Setenv("ALCAMPO_CONFIG_DIR", t.TempDir())
	c := New()
	c.LoadSession()
	c.UseCookies(map[string]string{"global_sid": "old", "AWSALB": "x"})
	c.csrf = "tok"
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "global_sid=new; Path=/")
	resp.Header.Add("Set-Cookie", "AWSALB=; Max-Age=0")
	c.absorbCookies(resp)
	if c.cookies["global_sid"] != "new" || c.cookies["AWSALB"] != "" || c.csrf != "" {
		t.Fatalf("after absorb: cookies=%v csrf=%q", c.cookies, c.csrf)
	}

	c2 := New()
	if !c2.LoadSession() || c2.cookies["global_sid"] != "new" {
		t.Fatalf("session did not persist: %v", c2.cookies)
	}
	if !c2.HasSession() {
		t.Fatal("HasSession should be true with a global_sid")
	}
}

// Re-sending the same global_sid (an expiry refresh) must keep the CSRF token;
// only a changed session drops it.
func TestSameSessionCookieKeepsCSRF(t *testing.T) {
	t.Setenv("ALCAMPO_CONFIG_DIR", t.TempDir())
	c := New()
	c.UseCookies(map[string]string{"global_sid": "s1"})
	c.csrf = "tok"
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "global_sid=s1; Path=/")
	c.absorbCookies(resp)
	if c.csrf != "tok" {
		t.Fatalf("csrf dropped on an unchanged session cookie")
	}
}

// Browser sync runs every command: an unchanged session must keep its CSRF
// token (no extra page fetch), a new session must drop it.
func TestSyncCookies(t *testing.T) {
	t.Setenv("ALCAMPO_CONFIG_DIR", t.TempDir())
	c := New()
	c.UseCookies(map[string]string{"global_sid": "s1", "aws-waf-token": "t1"})
	c.csrf = "tok"
	if c.SyncCookies(map[string]string{"global_sid": "s1", "aws-waf-token": "t1"}) {
		t.Fatal("identical cookies must report no change")
	}
	if !c.SyncCookies(map[string]string{"global_sid": "s1", "aws-waf-token": "t2"}) || c.csrf != "tok" {
		t.Fatalf("a refreshed WAF token must sync and keep csrf (csrf=%q)", c.csrf)
	}
	if !c.SyncCookies(map[string]string{"global_sid": "s2"}) || c.csrf != "" || c.cookies["aws-waf-token"] != "" {
		t.Fatalf("a new session must replace the jar and drop csrf: %v csrf=%q", c.cookies, c.csrf)
	}
}
