package httpretry

import (
	"net/http"
	"testing"
	"time"
)

func TestTransient(t *testing.T) {
	for status, want := range map[int]bool{200: false, 404: false, 500: false, 429: true, 502: true, 503: true, 504: true} {
		if got := Transient(status); got != want {
			t.Errorf("Transient(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestWait(t *testing.T) {
	with := func(v string) *http.Response { return &http.Response{Header: http.Header{"Retry-After": {v}}} }
	cases := []struct {
		name string
		resp *http.Response
		want time.Duration
	}{
		{"no response", nil, 200 * time.Millisecond},
		{"no header", with(""), 200 * time.Millisecond},
		{"seconds", with("2"), 2 * time.Second},
		{"shorter than the base", with("0"), 200 * time.Millisecond},
		{"capped", with("3600"), MaxWait},
		{"a date is ignored", with("Wed, 21 Oct 2026 07:28:00 GMT"), 200 * time.Millisecond},
	}
	for _, c := range cases {
		if got := Wait(c.resp, 200*time.Millisecond); got != c.want {
			t.Errorf("%s: Wait = %v, want %v", c.name, got, c.want)
		}
	}
}
