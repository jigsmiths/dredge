package slskd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is slskd. Each user behaves as users[name] says: "ok" sends the file,
// "stuck" never starts, "errors" fails the transfer, "offline" cannot be asked.
type fake struct {
	t     *testing.T
	dir   string
	users map[string]string
	resps []Response

	mu         sync.Mutex
	queries    []string
	key        string
	polls      map[string]int // transfer id -> GETs so far
	dest       map[string]string
	removed    []string
	searchGone bool
}

func newFake(t *testing.T, users map[string]string, resps []Response) (*fake, *Client) {
	t.Helper()
	f := &fake{t: t, dir: t.TempDir(), users: users, resps: resps, polls: map[string]int{}, dest: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v0/searches", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SearchText string `json:"searchText"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.queries, f.key = append(f.queries, body.SearchText), r.Header.Get("X-API-Key")
		f.mu.Unlock()
		_, _ = fmt.Fprint(w, `{"id":"s1","isComplete":false}`)
	})
	mux.HandleFunc("GET /api/v0/searches/s1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"s1","isComplete":true}`)
	})
	mux.HandleFunc("GET /api/v0/searches/s1/responses", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(f.resps)
	})
	mux.HandleFunc("DELETE /api/v0/searches/s1", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.searchGone = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v0/transfers/downloads/batches", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Files    []File `json:"files"`
			Options  struct {
				Destination string `json:"destination"`
			} `json:"options"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.users[body.Username] == "offline" {
			http.Error(w, "User "+body.Username+" appears to be offline", http.StatusNotFound)
			return
		}
		id := "t-" + body.Username
		f.mu.Lock()
		f.dest[id] = body.Options.Destination + "|" + body.Files[0].Filename
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"batch":{"transfers":[{"id":%q,"state":"Requested"}]},"failures":[]}`, id)
	})
	mux.HandleFunc("GET /api/v0/transfers/downloads/{user}/{id}", func(w http.ResponseWriter, r *http.Request) {
		user, id := r.PathValue("user"), r.PathValue("id")
		f.mu.Lock()
		f.polls[id]++
		n, dest := f.polls[id], f.dest[id]
		f.mu.Unlock()
		state, bytes := "Queued, Remotely", 0
		switch {
		case f.users[user] == "ok" && n == 1:
			state, bytes = "InProgress", 10
		case f.users[user] == "ok":
			d, remote, _ := strings.Cut(dest, "|")
			name := remote[strings.LastIndex(remote, `\`)+1:]
			if err := os.MkdirAll(filepath.Join(f.dir, d), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(f.dir, d, name), []byte("fLaC"), 0o644); err != nil {
				t.Error(err)
			}
			state, bytes = "Completed, Succeeded", 100
		case f.users[user] == "errors":
			state = "Completed, Errored"
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"state":%q,"bytesTransferred":%d,"size":100}`, id, state, bytes)
	})
	mux.HandleFunc("DELETE /api/v0/transfers/downloads/{user}/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.removed = append(f.removed, r.PathValue("user"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := New(srv.URL, "secret-key", f.dir, nil)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return f, c
}

func response(user string, free bool) Response {
	return Response{Username: user, HasFreeUploadSlot: free, Files: []File{flac(`Music\Ashe\01 Stop The Wedding!.flac`, 198)}}
}

func TestFetchDownloadsTheFileAndSaysWhereItIs(t *testing.T) {
	f, c := newFake(t, map[string]string{"alice": "ok"}, []Response{response("alice", true)})
	var seen [][2]int64
	got, err := c.Fetch(context.Background(), wedding, func(done, total int64) { seen = append(seen, [2]int64{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "01 Stop The Wedding!.flac" || !strings.HasPrefix(got, filepath.Join(f.dir, "dredge")+string(filepath.Separator)) {
		t.Errorf("path = %s, want the file in its own folder under dredge/", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Error(err)
	}
	if len(f.queries) != 1 || f.queries[0] != "Ashe stop the wedding" || f.key != "secret-key" {
		t.Errorf("search = %q with key %q", f.queries, f.key)
	}
	if !f.searchGone || len(f.removed) != 1 {
		t.Errorf("search deleted = %v, transfers removed = %q: slskd should be left tidy", f.searchGone, f.removed)
	}
	if len(seen) == 0 || seen[len(seen)-1] != [2]int64{100, 100} {
		t.Errorf("progress = %v", seen)
	}
}

func TestFetchTriesTheNextUserWhenOneFails(t *testing.T) {
	f, c := newFake(t, map[string]string{"gone": "offline", "broken": "errors", "stuck": "stuck", "bob": "ok"}, []Response{
		response("gone", true), response("broken", true), response("stuck", true), response("bob", false),
	})
	// only three are tried, so bob, last in line, is never reached
	if _, err := c.Fetch(context.Background(), wedding, nil); err == nil || !strings.Contains(err.Error(), "never started") {
		t.Fatalf("Fetch = %v, want the last failure", err)
	}
	if len(f.removed) != 2 {
		t.Errorf("removed = %q, want the failed and the stuck transfers cleared", f.removed)
	}

	f, c = newFake(t, map[string]string{"broken": "errors", "bob": "ok"}, []Response{response("broken", true), response("bob", false)})
	if got, err := c.Fetch(context.Background(), wedding, nil); err != nil || got == "" {
		t.Fatalf("Fetch = %q %v, want bob's file", got, err)
	}
	if f.polls["t-bob"] == 0 {
		t.Error("bob was never asked")
	}
}

func TestFetchFindsNothingThatFits(t *testing.T) {
	_, c := newFake(t, nil, []Response{{Username: "u", Files: []File{flac(`Ashe\Moral of the Story.flac`, 201)}}})
	if _, err := c.Fetch(context.Background(), wedding, nil); !errors.Is(err, ErrNoFile) {
		t.Errorf("Fetch = %v, want ErrNoFile", err)
	}
	noLength := wedding
	noLength.DurationMS = 0
	if _, err := c.Fetch(context.Background(), noLength, nil); !errors.Is(err, ErrNoFile) {
		t.Errorf("Fetch without a length = %v, want ErrNoFile without searching", err)
	}
}
