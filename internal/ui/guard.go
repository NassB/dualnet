package ui

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CSRFHeader must be present on every request that changes something.
//
// dualnet runs as administrator and its API can change Windows settings, start
// downloads and turn networking on or off. Any web page you visit can send
// "simple" cross-origin POSTs to 127.0.0.1 (it cannot read the reply, but the
// request still arrives). A custom header turns the request into one that needs
// a CORS preflight, which dualnet never grants, so only dualnet's own page (and
// local scripts that set it on purpose) can use the API.
const CSRFHeader = "X-Dualnet"

func isLocalHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// guard protects the API:
//   - the Host header must be a loopback name (DNS rebinding);
//   - requests that change something must carry CSRFHeader, must not be marked
//     cross-site by the browser (Sec-Fetch-Site) and, if they carry an Origin,
//     it must be dualnet's own.
func guard(h http.Handler) http.Handler {
	deny := func(w http.ResponseWriter) { http.Error(w, "forbidden", http.StatusForbidden) }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalHost(r.Host) {
			deny(w)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(CSRFHeader) == "" {
				deny(w)
				return
			}
			// "none" = typed in the address bar / app window; same-site (another
			// port on localhost) and cross-site are refused.
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				deny(w)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					deny(w) // also catches "null" (sandboxed frames)
					return
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}
