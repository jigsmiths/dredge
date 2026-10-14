package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/jobs"
	"github.com/jigsmiths/dredge/internal/library"
)

const plink = "https://open.spotify.com/playlist/37i9dQZF1DWXRqgorJj26U"

// The manager is never started, so submitted jobs stay queued.
func newServer(t *testing.T, key string) http.Handler {
	t.Helper()
	conn, err := db.Connect(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	m := jobs.NewManager(jobs.NewStore(conn), jobs.Deps{})
	return New(Options{
		Jobs: m, APIKey: key, Search: fakeSearch{},
		Scan: func(context.Context) (library.ScanStats, error) { return library.ScanStats{Files: 7, Indexed: 2}, nil },
	})
}

func call(t *testing.T, h http.Handler, method, path, body string, hdr ...string) (int, map[string]any, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var m map[string]any
	_ = json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&m)
	return rec.Code, m, rec.Body.String()
}

func TestSubmitThenFetchAJob(t *testing.T) {
	h := newServer(t, "")
	code, j, _ := call(t, h, "POST", "/api/jobs", `{"url":"`+plink+`"}`)
	if code != http.StatusAccepted || j["status"] != "queued" || j["url"] != plink {
		t.Fatalf("submit: %d %v", code, j)
	}
	code, again, _ := call(t, h, "POST", "/api/jobs", `{"url":"`+plink+`"}`)
	if code != http.StatusOK || again["id"] != j["id"] {
		t.Errorf("resubmit: %d %v, want 200 and the same job", code, again)
	}
	code, got, _ := call(t, h, "GET", "/api/jobs/1", "")
	if code != http.StatusOK || got["id"] != j["id"] || got["tracks"] == nil {
		t.Errorf("get: %d %v", code, got)
	}
}

func TestListJobs(t *testing.T) {
	h := newServer(t, "")
	call(t, h, "POST", "/api/jobs", `{"url":"`+plink+`"}`)
	code, _, raw := call(t, h, "GET", "/api/jobs", "")
	if code != http.StatusOK || !strings.HasPrefix(raw, "[") || !strings.Contains(raw, `"queued"`) {
		t.Errorf("list: %d %s", code, raw)
	}
	_, _, raw = call(t, h, "GET", "/api/jobs?status=done", "")
	if strings.TrimSpace(raw) != "[]" {
		t.Errorf("filtered list = %s, want []", raw)
	}
}

func TestBadRequests(t *testing.T) {
	h := newServer(t, "")
	for name, body := range map[string]string{
		"not json": `nope`, "no url": `{}`, "not spotify": `{"url":"https://example.com/x"}`,
	} {
		if code, m, _ := call(t, h, "POST", "/api/jobs", body); code != http.StatusBadRequest || m["error"] == nil {
			t.Errorf("%s: %d %v", name, code, m)
		}
	}
}

func TestUnknownJobsAre404AndWrongStatesAre409(t *testing.T) {
	h := newServer(t, "")
	for _, c := range [][2]string{{"GET", "/api/jobs/99"}, {"GET", "/api/jobs/abc"}, {"POST", "/api/jobs/99/retry"}, {"POST", "/api/jobs/99/cancel"}, {"DELETE", "/api/jobs/99"}} {
		if code, _, _ := call(t, h, c[0], c[1], ""); code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c[0], c[1], code)
		}
	}
	call(t, h, "POST", "/api/jobs", `{"url":"`+plink+`"}`)
	if code, _, _ := call(t, h, "POST", "/api/jobs/1/retry", ""); code != http.StatusConflict {
		t.Errorf("retry of a queued job = %d, want 409", code)
	}
	if code, _, _ := call(t, h, "DELETE", "/api/jobs/1", ""); code != http.StatusConflict {
		t.Errorf("delete of a queued job = %d, want 409", code)
	}
}

func TestCancelThenDelete(t *testing.T) {
	h := newServer(t, "")
	call(t, h, "POST", "/api/jobs", `{"url":"`+plink+`"}`)
	if code, j, _ := call(t, h, "POST", "/api/jobs/1/cancel", ""); code != http.StatusAccepted || j["status"] != "cancelled" {
		t.Errorf("cancel: %d %v", code, j)
	}
	if code, _, _ := call(t, h, "DELETE", "/api/jobs/1", ""); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if code, _, _ := call(t, h, "GET", "/api/jobs/1", ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d", code)
	}
}

func TestScan(t *testing.T) {
	h := newServer(t, "")
	code, m, _ := call(t, h, "POST", "/api/scan", "")
	if code != http.StatusOK || m["files"] != float64(7) || m["indexed"] != float64(2) {
		t.Errorf("scan: %d %v", code, m)
	}
}

func TestAPIKey(t *testing.T) {
	h := newServer(t, "s3cret")
	if code, _, _ := call(t, h, "GET", "/api/jobs", ""); code != http.StatusUnauthorized {
		t.Errorf("no key = %d, want 401", code)
	}
	if code, _, _ := call(t, h, "GET", "/api/jobs", "", "X-Api-Key", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong key = %d, want 401", code)
	}
	if code, _, _ := call(t, h, "GET", "/api/jobs", "", "X-Api-Key", "s3cret"); code != http.StatusOK {
		t.Errorf("right key = %d, want 200", code)
	}
}

func TestHealthAndTheUIStayOpenWhateverTheKey(t *testing.T) {
	h := newServer(t, "s3cret")
	if code, _, _ := call(t, h, "GET", "/health", ""); code != http.StatusOK {
		t.Errorf("health = %d", code)
	}
	if code, _, _ := call(t, h, "GET", "/", ""); code != http.StatusOK {
		t.Errorf("ui = %d", code)
	}
}

func TestNoKeyMeansOpen(t *testing.T) {
	h := newServer(t, "")
	if code, _, _ := call(t, h, "GET", "/api/jobs", ""); code != http.StatusOK {
		t.Errorf("open api = %d", code)
	}
}
