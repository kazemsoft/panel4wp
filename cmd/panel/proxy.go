package main

import (
	"crypto/hmac"
	"net"
	"net/http"
)

// Caddy overwrites both headers. A direct caller cannot forge client identity
// without the secret; ordinary X-Forwarded-For is never trusted.
func (a *app) loginClientIP(r *http.Request) string {
	if len(a.sessionKey) >= 32 && hmac.Equal([]byte(r.Header.Get("X-Panel-Proxy-Token")), a.sessionKey) {
		if ip := net.ParseIP(r.Header.Get("X-Panel-Client-IP")); ip != nil {
			return ip.String()
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
