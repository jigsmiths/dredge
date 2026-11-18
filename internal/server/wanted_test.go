package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/db"
	"github.com/jigsmiths/dredge/internal/jobs"
	"github.com/jigsmiths/dredge/internal/track"
)

func TestListCheckAndRemoveWantedTracks(t *testing.T) {
	conn, err := db.Connect(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	m := jobs.NewManager(jobs.NewStore(conn), jobs.Deps{})
	h := New(Options{Jobs: m, Search: fakeSearch{}})

	if _, _, raw := call(t, h, "GET", "/api/wanted", ""); strings.TrimSpace(raw) != "[]" {
		t.Errorf("empty list = %s, want []", raw)
	}
	if err := m.AddWanted(context.Background(), track.Track{Title: "Stop the Wedding", Artists: []string{"Ashe"}, SourceID: "abc"}, "no match"); err != nil {
		t.Fatal(err)
	}
	code, _, raw := call(t, h, "GET", "/api/wanted", "")
	if code != http.StatusOK || !strings.Contains(raw, `"title":"Stop the Wedding"`) || !strings.Contains(raw, `"artists":["Ashe"]`) {
		t.Errorf("list: %d %s", code, raw)
	}
	if code, _, _ := call(t, h, "POST", "/api/wanted", `{"title":"Pushing Daisies","artists":["Ashe"]}`); code != http.StatusCreated {
		t.Errorf("want by name = %d, want 201", code)
	}
	for _, body := range []string{`nope`, `{"title":"x"}`, `{"artists":["Ashe"]}`} {
		if code, m, _ := call(t, h, "POST", "/api/wanted", body); code != http.StatusBadRequest || m["error"] == nil {
			t.Errorf("want %s = %d %v, want 400", body, code, m)
		}
	}
	if _, _, raw := call(t, h, "GET", "/api/wanted", ""); !strings.Contains(raw, `"title":"Pushing Daisies"`) {
		t.Errorf("list after want = %s", raw)
	}
	if code, _, _ := call(t, h, "POST", "/api/wanted/check", ""); code != http.StatusAccepted {
		t.Errorf("check = %d, want 202", code)
	}
	if code, _, _ := call(t, h, "DELETE", "/api/wanted/1", ""); code != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", code)
	}
	for _, p := range []string{"/api/wanted/1", "/api/wanted/abc"} {
		if code, _, _ := call(t, h, "DELETE", p, ""); code != http.StatusNotFound {
			t.Errorf("DELETE %s = %d, want 404", p, code)
		}
	}
}
