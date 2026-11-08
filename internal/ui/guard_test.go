package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuard(t *testing.T) {
	srv := httptest.NewServer((&Server{}).Handler())
	defer srv.Close()
	own := srv.URL // http://127.0.0.1:port — what dualnet's own page sends as Origin

	do := func(method, path, body string, hdr map[string]string, host string) int {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	h := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	const act = "/api/cancel?id=999" // harmless: no such download

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		hdr    map[string]string
		host   string
		want   int
	}{
		{"page loads", "GET", "/", "", nil, "", 200},
		{"foreign Host (DNS rebinding)", "POST", act, "", h(CSRFHeader, "1"), "evil.example", 403},
		// the exact request that used to succeed
		{"website POST, no header", "POST", act, "", h("Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site", "Content-Type", "text/plain"), "", 403},
		{"no header, no origin", "POST", act, "", nil, "", 403},
		{"header but foreign Origin", "POST", act, "", h(CSRFHeader, "1", "Origin", "https://evil.example"), "", 403},
		{"header but Origin null (sandboxed frame)", "POST", act, "", h(CSRFHeader, "1", "Origin", "null"), "", 403},
		{"header but cross-site", "POST", act, "", h(CSRFHeader, "1", "Sec-Fetch-Site", "cross-site"), "", 403},
		{"header but same-site (another local port)", "POST", act, "", h(CSRFHeader, "1", "Sec-Fetch-Site", "same-site"), "", 403},
		{"CORS preflight", "OPTIONS", act, "", h("Origin", "https://evil.example", "Access-Control-Request-Method", "POST", "Access-Control-Request-Headers", "x-dualnet"), "", 403},
		{"dualnet's own page", "POST", act, "", h(CSRFHeader, "1", "Origin", own, "Sec-Fetch-Site", "same-origin"), "", 204},
		{"local script with the header", "POST", act, "", h(CSRFHeader, "1"), "", 204},
		{"app window (Sec-Fetch-Site none)", "POST", act, "", h(CSRFHeader, "1", "Sec-Fetch-Site", "none"), "", 204},
		{"download: file:// refused", "POST", "/api/download", `{"url":"file:///C:/Windows/win.ini"}`, h(CSRFHeader, "1"), "", 400},
		{"download: no host refused", "POST", "/api/download", `{"url":"http:///x"}`, h(CSRFHeader, "1"), "", 400},
		{"download: missing url refused", "POST", "/api/download", `{}`, h(CSRFHeader, "1"), "", 400},
	}
	for _, c := range cases {
		if got := do(c.method, c.path, c.body, c.hdr, c.host); got != c.want {
			t.Errorf("%s: HTTP %d, want %d", c.name, got, c.want)
		}
	}
}
