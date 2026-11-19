package jobs

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jigsmiths/dredge/internal/track"
)

var ErrWantedNotFound = errors.New("wanted track not found")

// Wanted is a track the catalogue did not have, kept to be looked for again.
type Wanted struct {
	ID         int64      `json:"id"`
	Title      string     `json:"title"`
	Artists    []string   `json:"artists"`
	Album      string     `json:"album,omitempty"`
	DurationMS int        `json:"duration_ms,omitempty"`
	ISRC       string     `json:"isrc,omitempty"`
	SourceID   string     `json:"source_id,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	AddedAt    time.Time  `json:"added_at"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
}

func (w Wanted) Track() track.Track {
	return track.Track{Title: w.Title, Artists: w.Artists, Album: w.Album, DurationMS: w.DurationMS, ISRC: w.ISRC, SourceID: w.SourceID}
}

func wantedKey(t track.Track) string {
	if t.SourceID != "" {
		return t.SourceID
	}
	if t.ISRC != "" {
		return "isrc:" + t.ISRC
	}
	return strings.ToLower(t.Title + "\n" + strings.Join(t.Artists, "\n"))
}

// AddWanted keeps t to look for again. Adding it twice only updates the reason.
func (s *Store) AddWanted(ctx context.Context, t track.Track, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO wanted (key, title, artists, album, duration_ms, isrc, source_id, reason, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET reason = excluded.reason`,
		wantedKey(t), t.Title, strings.Join(t.Artists, "\n"), t.Album, t.DurationMS, t.ISRC, t.SourceID, reason, unix(time.Now()))
	return err
}

// ListWanted returns the wanted tracks, newest first.
func (s *Store) ListWanted(ctx context.Context) ([]Wanted, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, artists, album, duration_ms, isrc, source_id, reason, added_at, checked_at
		FROM wanted ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Wanted{}
	for rows.Next() {
		var w Wanted
		var artists string
		var added int64
		var checked sql.NullInt64
		if err := rows.Scan(&w.ID, &w.Title, &artists, &w.Album, &w.DurationMS, &w.ISRC, &w.SourceID, &w.Reason, &added, &checked); err != nil {
			return nil, err
		}
		w.Artists = []string{}
		if artists != "" {
			w.Artists = strings.Split(artists, "\n")
		}
		w.AddedAt, w.CheckedAt = time.Unix(added, 0), ptr(checked)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWanted(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM wanted WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWantedNotFound
	}
	return nil
}

// CheckedWanted records a look that did not find the track.
func (s *Store) CheckedWanted(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE wanted SET checked_at = ?, reason = ? WHERE id = ?`, unix(time.Now()), reason, id)
	return err
}

// GetWanted returns one wanted track. The list stays short, so it is read whole.
func (s *Store) GetWanted(ctx context.Context, id int64) (Wanted, error) {
	ws, err := s.ListWanted(ctx)
	if err != nil {
		return Wanted{}, err
	}
	for _, w := range ws {
		if w.ID == id {
			return w, nil
		}
	}
	return Wanted{}, ErrWantedNotFound
}
