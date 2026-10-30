package client

import "testing"

func TestParseHARFoldsCookiesInOrder(t *testing.T) {
	har := `{"log":{"entries":[
	 {"request":{"url":"https://www.compraonline.alcampo.es/","headers":[{"name":"Cookie","value":"global_sid=old; VISITORID=v1"}]},
	  "response":{"headers":[{"name":"set-cookie","value":"global_sid=new; Path=/; HttpOnly\naws-waf-token=tok; Path=/"}]}},
	 {"request":{"url":"https://other.example/","headers":[{"name":"cookie","value":"global_sid=ignored"}]},"response":{"headers":[]}},
	 {"request":{"url":"https://www.compraonline.alcampo.es/api/cart/v1/carts/active","headers":[{"name":"cookie","value":"global_sid=new; VISITORID=v2; aws-waf-token=tok"}]},
	  "response":{"headers":[{"name":"Set-Cookie","value":"VISITORID=; Max-Age=0"}]}}
	]}}`
	s, err := ParseHAR([]byte(har))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"global_sid": "new", "aws-waf-token": "tok"}
	if len(s.Cookies) != len(want) {
		t.Fatalf("cookies = %v, want %v", s.Cookies, want)
	}
	for k, v := range want {
		if s.Cookies[k] != v {
			t.Errorf("%s = %q, want %q", k, s.Cookies[k], v)
		}
	}
}

func TestParseHARNeedsSession(t *testing.T) {
	har := `{"log":{"entries":[{"request":{"url":"https://www.compraonline.alcampo.es/","headers":[]},"response":{"headers":[]}}]}}`
	if _, err := ParseHAR([]byte(har)); err == nil {
		t.Fatal("a HAR without global_sid (e.g. a sanitized export) must be an error")
	}
	if _, err := ParseHAR([]byte("not json")); err == nil {
		t.Fatal("invalid JSON must be an error")
	}
}
