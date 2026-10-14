package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jigsmiths/dredge/internal/match"
)

type fakeSearch struct{}

func (fakeSearch) Search(_ context.Context, q string, _ int) ([]match.Candidate, error) {
	switch q {
	case "barracuda":
		return []match.Candidate{
			{ISRC: "USSM10009075", Title: "Barracuda", Artists: []string{"Heart"}, DurationMS: 261933},
			{ISRC: "USSM10009075", Title: "Barracuda", Artists: []string{"Heart"}, DurationMS: 261933}, // another release
			{ISRC: "USXX10000001", Title: "Barracuda (Live)", Artists: []string{"Heart"}, DurationMS: 300000},
		}, nil
	case "boom":
		return nil, errors.New("monochrome down")
	}
	return nil, nil
}

func TestSearchListsEachRecordingOnce(t *testing.T) {
	h := newServer(t, "")
	code, _, raw := call(t, h, "GET", "/api/search?q=barracuda", "")
	if code != http.StatusOK || strings.Count(raw, `"USSM10009075"`) != 1 || !strings.Contains(raw, `"Barracuda (Live)"`) ||
		!strings.Contains(raw, `"duration_ms":261933`) {
		t.Errorf("search: %d %s", code, raw)
	}
}

func TestSearchWithNoHitsIsAnEmptyList(t *testing.T) {
	h := newServer(t, "")
	if code, _, raw := call(t, h, "GET", "/api/search?q=zzz", ""); code != http.StatusOK || strings.TrimSpace(raw) != "[]" {
		t.Errorf("search: %d %q", code, raw)
	}
}

func TestSearchNeedsAQueryAndReportsUpstreamTrouble(t *testing.T) {
	h := newServer(t, "")
	if code, _, _ := call(t, h, "GET", "/api/search?q=%20", ""); code != http.StatusBadRequest {
		t.Errorf("blank query = %d, want 400", code)
	}
	if code, _, _ := call(t, h, "GET", "/api/search?q=boom", ""); code != http.StatusBadGateway {
		t.Errorf("upstream failure = %d, want 502", code)
	}
}

func TestSearchNeedsTheAPIKeyToo(t *testing.T) {
	h := newServer(t, "s3cret")
	if code, _, _ := call(t, h, "GET", "/api/search?q=barracuda", ""); code != http.StatusUnauthorized {
		t.Errorf("no key = %d", code)
	}
}

func TestSubmitAPickedTrack(t *testing.T) {
	h := newServer(t, "")
	body := `{"isrc":"ussm10009075","title":"Barracuda","artists":["Heart"]}`
	code, j, _ := call(t, h, "POST", "/api/jobs", body)
	if code != http.StatusAccepted || j["name"] != "Heart - Barracuda" || j["kind"] != "track" || j["total"] != float64(1) {
		t.Fatalf("submit: %d %v", code, j)
	}
	if code, again, _ := call(t, h, "POST", "/api/jobs", body); code != http.StatusOK || again["id"] != j["id"] {
		t.Errorf("resubmit: %d %v", code, again)
	}
	_, got, _ := call(t, h, "GET", "/api/jobs/1", "")
	if ts, _ := got["tracks"].([]any); len(ts) != 1 {
		t.Errorf("tracks = %v", got["tracks"])
	}
}

func TestSubmitAPickedTrackRejectsBadInput(t *testing.T) {
	h := newServer(t, "")
	for name, body := range map[string]string{
		"bad isrc":  `{"isrc":"x","title":"T","artists":["A"]}`,
		"no artist": `{"isrc":"USSM10009075","title":"T"}`,
		"both":      `{"url":"` + plink + `","isrc":"USSM10009075","title":"T","artists":["A"]}`,
	} {
		if code, _, _ := call(t, h, "POST", "/api/jobs", body); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, code)
		}
	}
}
