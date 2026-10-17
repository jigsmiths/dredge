// Package httpretry holds the retry rules the HTTP clients share.
package httpretry

import (
	"net/http"
	"strconv"
	"time"
)

// MaxWait caps how long a server's Retry-After can hold a request.
const MaxWait = 5 * time.Second

// Transient reports whether a status is worth another try.
func Transient(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// Wait is base, or the server's Retry-After if longer, up to MaxWait.
func Wait(resp *http.Response, base time.Duration) time.Duration {
	if resp != nil {
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			base = max(base, time.Duration(secs)*time.Second)
		}
	}
	return min(base, MaxWait)
}
