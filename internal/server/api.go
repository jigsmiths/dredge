package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/jigsmiths/dredge/internal/jobs"
	"github.com/jigsmiths/dredge/internal/library"
)

type Options struct {
	Jobs   *jobs.Manager
	Scan   func(context.Context) (library.ScanStats, error)
	APIKey string // when set, /api needs it in X-Api-Key
}

type api struct{ Options }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing a response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps a jobs error to a response.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		writeError(w, http.StatusNotFound, "job not found")
	case errors.Is(err, jobs.ErrConflict):
		writeError(w, http.StatusConflict, "the job is not in a state that allows that")
	case errors.Is(err, jobs.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("api", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// requireKey lets a request through only with the API key, when one is set.
func (a *api) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.APIKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Key")), []byte(a.APIKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or wrong X-Api-Key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) routes(r chi.Router) {
	r.Use(a.requireKey)
	r.Post("/jobs", a.submit)
	r.Get("/jobs", a.list)
	r.Route("/jobs/{id}", func(r chi.Router) {
		r.Get("/", a.get)
		r.Delete("/", a.remove)
		r.Post("/retry", a.retry)
		r.Post("/cancel", a.cancel)
	})
	r.Post("/scan", a.scan)
}

func jobID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return 0, false
	}
	return id, true
}

func (a *api) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, `expected {"url": "<spotify link>"}`)
		return
	}
	j, created, err := a.Jobs.Submit(r.Context(), body.URL)
	if err != nil {
		fail(w, err)
		return
	}
	status := http.StatusOK // already queued or running
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, j)
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	js, err := a.Jobs.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, js)
}

func (a *api) get(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	j, err := a.Jobs.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	ts, err := a.Jobs.Tracks(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		jobs.Job
		Tracks []jobs.Track `json:"tracks"`
	}{j, ts})
}

func (a *api) retry(w http.ResponseWriter, r *http.Request) {
	a.act(w, r, a.Jobs.Retry, http.StatusAccepted)
}

func (a *api) cancel(w http.ResponseWriter, r *http.Request) {
	a.act(w, r, a.Jobs.Stop, http.StatusAccepted)
}

func (a *api) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	if err := a.Jobs.Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) act(w http.ResponseWriter, r *http.Request, f func(context.Context, int64) error, status int) {
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	if err := f(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	j, err := a.Jobs.Get(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, status, j)
}

func (a *api) scan(w http.ResponseWriter, r *http.Request) {
	st, err := a.Scan(r.Context())
	if err != nil {
		slog.Error("scan", "err", err)
		writeError(w, http.StatusInternalServerError, "scan failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"files": st.Files, "indexed": st.Indexed, "unchanged": st.Unchanged,
		"removed": st.Removed, "unreadable": st.Unreadable,
	})
}
