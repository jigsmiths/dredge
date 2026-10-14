// Package jobs queues Spotify links and works through them in the background,
// recording progress per track so it can be shown while it runs and after.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Status string

const (
	Queued    Status = "queued"
	Resolving Status = "resolving" // fetching the link and checking the library
	Running   Status = "running"   // downloading
	Done      Status = "done"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

func (s Status) active() bool { return s == Queued || s == Resolving || s == Running }

// Track statuses. Pending and Downloading are still to do; the rest are final.
const (
	TrackPending     = "pending"
	TrackDownloading = "downloading"
	TrackDone        = "done"
	TrackHave        = "have"
	TrackNoMatch     = "no_match"
	TrackAmbiguous   = "ambiguous"
	TrackDuplicate   = "duplicate"
	TrackNotStocked  = "not_stocked"
	TrackFailed      = "failed"
)

var (
	ErrNotFound = errors.New("job not found")
	ErrConflict = errors.New("job is in the wrong state for that")
)

type Job struct {
	ID         int64          `json:"id"`
	URL        string         `json:"url"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Status     Status         `json:"status"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"` // tracks per status
}

type Track struct {
	Pos     int      `json:"pos"`
	Title   string   `json:"title"`
	Artists []string `json:"artists"`
	Album   string   `json:"album,omitempty"`
	ISRC    string   `json:"isrc,omitempty"`
	Status  string   `json:"status"`
	Path    string   `json:"path,omitempty"` // relative to the library root
	Bytes   int64    `json:"bytes,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db} }

func unix(t time.Time) int64 { return t.Unix() }

func ptr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0)
	return &t
}

const jobCols = `id, url, kind, name, status, error, created_at, started_at, finished_at`

type scanner interface{ Scan(...any) error }

func scanJob(r scanner) (Job, error) {
	var j Job
	var created int64
	var started, finished sql.NullInt64
	if err := r.Scan(&j.ID, &j.URL, &j.Kind, &j.Name, &j.Status, &j.Error, &created, &started, &finished); err != nil {
		return Job{}, err
	}
	j.CreatedAt, j.StartedAt, j.FinishedAt = time.Unix(created, 0), ptr(started), ptr(finished)
	j.Counts = map[string]int{}
	return j, nil
}

// Seed is what a job that has no Spotify page to fetch starts with.
type Seed struct {
	Kind, Name string
	Tracks     []Track
}

// Create queues url, or returns the active job for it. created is false then.
func (s *Store) Create(ctx context.Context, url string, seed *Seed) (j Job, created bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs
		WHERE url = ? AND status IN ('queued','resolving','running') ORDER BY id LIMIT 1`, url)
	if j, err = scanJob(row); err == nil {
		return j, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, err
	}
	var kind, name string
	if seed != nil {
		kind, name = seed.Kind, seed.Name
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (url, kind, name, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		url, kind, name, Queued, unix(time.Now()))
	if err != nil {
		return Job{}, false, err
	}
	id, _ := res.LastInsertId()
	if seed != nil {
		if err := insertTracks(ctx, tx, id, seed.Tracks); err != nil {
			return Job{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	j, err = s.Get(ctx, id)
	return j, true, err
}

func (s *Store) Get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM job_tracks WHERE job_id = ? GROUP BY status`, id)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return Job{}, err
		}
		j.Counts[st], j.Total = n, j.Total+n
	}
	return j, rows.Err()
}

// List returns jobs newest first, optionally only those with status.
func (s *Store) List(ctx context.Context, status string) ([]Job, error) {
	q, args := `SELECT id FROM jobs`, []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	out := make([]Job, 0, len(ids))
	for _, id := range ids {
		j, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

func (s *Store) Tracks(ctx context.Context, id int64) ([]Track, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pos, title, artists, album, isrc, status, path, bytes, reason
		FROM job_tracks WHERE job_id = ? ORDER BY pos`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Track{}
	for rows.Next() {
		var t Track
		var artists string
		if err := rows.Scan(&t.Pos, &t.Title, &artists, &t.Album, &t.ISRC, &t.Status, &t.Path, &t.Bytes, &t.Reason); err != nil {
			return nil, err
		}
		t.Artists = []string{}
		if artists != "" {
			t.Artists = strings.Split(artists, "\n")
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Claim takes the oldest queued job and marks it resolving; nil if none.
func (s *Store) Claim(ctx context.Context) (*Job, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `UPDATE jobs SET status = ?, started_at = ?, error = ''
		WHERE id = (SELECT id FROM jobs WHERE status = 'queued' ORDER BY id LIMIT 1)
		RETURNING id`, Resolving, unix(time.Now())).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j, err := s.Get(ctx, id)
	return &j, err
}

func (s *Store) SetCollection(ctx context.Context, id int64, kind, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET kind = ?, name = ? WHERE id = ?`, kind, name, id)
	return err
}

// SetStatus moves a job on; a finished status also stamps the finish time.
func (s *Store) SetStatus(ctx context.Context, id int64, st Status, errMsg string) error {
	var finished any
	if !st.active() {
		finished = unix(time.Now())
	}
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		st, errMsg, finished, id)
	return err
}

// ReplaceTracks sets the track list of a job, dropping any earlier run's.
func (s *Store) ReplaceTracks(ctx context.Context, id int64, tracks []Track) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_tracks WHERE job_id = ?`, id); err != nil {
		return err
	}
	if err := insertTracks(ctx, tx, id, tracks); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTracks(ctx context.Context, tx *sql.Tx, id int64, tracks []Track) error {
	for _, t := range tracks {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_tracks (job_id, pos, title, artists, album, isrc, status, path, bytes, reason)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, t.Pos, t.Title, strings.Join(t.Artists, "\n"), t.Album, t.ISRC, t.Status, t.Path, t.Bytes, t.Reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetTrack(ctx context.Context, id int64, pos int, status, path string, bytes int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_tracks SET status = ?, path = ?, bytes = ?, reason = ?
		WHERE job_id = ? AND pos = ?`, status, path, bytes, reason, id, pos)
	return err
}

// Requeue puts a finished job back in the queue to run again.
func (s *Store) Requeue(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = '', finished_at = NULL
		WHERE id = ? AND status IN ('done','failed','cancelled')`, Queued, id)
	return affected(ctx, s, res, err, id)
}

// Cancel cancels a queued job. A job already running is stopped by the Manager.
func (s *Store) Cancel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, finished_at = ?
		WHERE id = ? AND status = 'queued'`, Cancelled, unix(time.Now()), id)
	return affected(ctx, s, res, err, id)
}

// Delete removes the record of a finished job. Files are never touched.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE id = ? AND status IN ('done','failed','cancelled')`, id)
	if err := affected(ctx, s, res, err, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_tracks WHERE job_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// affected turns "no row changed" into not found or wrong state.
func affected(ctx context.Context, s *Store, res sql.Result, err error, id int64) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	return ErrConflict
}

// Recover queues the jobs a crash or restart left half done. Their tracks are
// rebuilt when they run again.
func (s *Store) Recover(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE status IN ('resolving','running')`, Queued)
	return err
}
