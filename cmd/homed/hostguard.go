package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// hostGuard lets through only requests whose Host is the relay domain (any port) or this hub's own loopback address,
// and answers anything else with 421. It is the DNS-rebinding guard of the public listener: the gateway turns off
// the go-sdk's own guard, because relayed requests reach 127.0.0.1 with the public Host. domain is read on every
// request, so a relay connected while homed runs is accepted at once; "" means no relay.
func hostGuard(domain func() string, port int, next http.Handler) http.Handler {
	p := strconv.Itoa(port)
	local := map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true, "[::1]:" + p: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if d := strings.ToLower(domain()); local[host] || (d != "" && hostname(host) == d) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte(`{"error":"misdirected request"}` + "\n"))
	})
}

// hostname drops the port and a trailing dot from a Host header value.
func hostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}
