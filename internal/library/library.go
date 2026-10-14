// Package library knows where tracks live on disk and what is already there,
// so nothing is downloaded twice.
package library

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dhowden/tag"

	"github.com/jigsmiths/dredge/internal/flacmeta"
	"github.com/jigsmiths/dredge/internal/track"
)

const maxName = 100

var unsafeRe = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]`)

// Path is where a downloaded track goes: Artist/Album/01 Title.flac, with the
// disc number in front for discs after the first.
func Path(root string, t track.Track) string {
	artist := t.AlbumArtist
	if strings.TrimSpace(artist) == "" && len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	name := t.Title
	switch {
	case t.TrackNo > 0 && t.DiscNo > 1:
		name = fmt.Sprintf("%d-%02d %s", t.DiscNo, t.TrackNo, name)
	case t.TrackNo > 0:
		name = fmt.Sprintf("%02d %s", t.TrackNo, name)
	}
	return filepath.Join(root,
		safe(artist, "Unknown Artist"), safe(t.Album, "Unknown Album"), safe(name, "Unknown Title")+".flac")
}

// safe makes one path component that cannot climb out of its directory.
func safe(s, fallback string) string {
	s = strings.Trim(unsafeRe.ReplaceAllString(s, "_"), " .")
	if r := []rune(s); len(r) > maxName {
		s = strings.Trim(string(r[:maxName]), " .")
	}
	if s == "" {
		return fallback
	}
	return s
}

type Index struct{ db *sql.DB }

func NewIndex(db *sql.DB) *Index { return &Index{db} }

type Entry struct {
	Path    string
	ISRC    string
	Title   string
	Artists []string
}

var splitRe = regexp.MustCompile(`(?i)\s*(?:;|/|,|&|\bfeat\.?|\bft\.?|\bfeaturing|\band\b|\bwith\b)\s*`)

// artistSet is every name an artist string could mean, as |a|b|. Tags often
// hold "A feat. B" in one value, and "Tyler, The Creator" must stay whole.
func artistSet(names []string) string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if n := track.NormArtist(s); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, name := range names {
		add(name)
		for _, part := range splitRe.Split(name, -1) {
			add(part)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "|" + strings.Join(out, "|") + "|"
}

func (x *Index) put(ctx context.Context, e Entry, mtime, size int64) error {
	_, err := x.db.ExecContext(ctx, `
		INSERT INTO library (path, isrc, title_norm, artists_norm, mtime, size) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET isrc = excluded.isrc, title_norm = excluded.title_norm,
			artists_norm = excluded.artists_norm, mtime = excluded.mtime, size = excluded.size`,
		e.Path, strings.ToUpper(strings.TrimSpace(e.ISRC)), track.NormTitle(e.Title), artistSet(e.Artists), mtime, size)
	return err
}

// Add records a file that exists, such as one just downloaded.
func (x *Index) Add(ctx context.Context, e Entry) error {
	fi, err := os.Stat(e.Path)
	if err != nil {
		return err
	}
	return x.put(ctx, e, fi.ModTime().UnixNano(), fi.Size())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type row struct{ path, artists string }

func (x *Index) rows(ctx context.Context, where string, arg string) ([]row, error) {
	rs, err := x.db.QueryContext(ctx, `SELECT path, artists_norm FROM library WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.path, &r.artists); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

// ByISRC finds a file with exactly this recording.
func (x *Index) ByISRC(ctx context.Context, isrc string) (string, bool, error) {
	isrc = strings.ToUpper(strings.TrimSpace(isrc))
	if isrc == "" {
		return "", false, nil
	}
	rs, err := x.rows(ctx, `isrc = ?`, isrc)
	if err != nil {
		return "", false, err
	}
	for _, r := range rs {
		if exists(r.path) {
			return r.path, true, nil
		}
	}
	return "", false, nil
}

// ByMeta finds a file with the same title and an artist in common, for files
// that carry no ISRC. Live, remix and similar tags stay part of the title.
func (x *Index) ByMeta(ctx context.Context, title string, artists []string) (string, bool, error) {
	t, wanted := track.NormTitle(title), track.NormArtists(artists)
	if t == "" || len(wanted) == 0 {
		return "", false, nil
	}
	rs, err := x.rows(ctx, `title_norm = ?`, t)
	if err != nil {
		return "", false, err
	}
	for _, r := range rs {
		for _, w := range wanted {
			if strings.Contains(r.artists, "|"+w+"|") && exists(r.path) {
				return r.path, true, nil
			}
		}
	}
	return "", false, nil
}

type ScanStats struct {
	Files      int // audio files seen
	Indexed    int // new or changed
	Unchanged  int
	Removed    int // in the index but no longer on disk
	Unreadable int // tags could not be read
}

var audioExts = map[string]bool{".flac": true, ".mp3": true, ".m4a": true, ".ogg": true}

type stamp struct{ mtime, size int64 }

// Scan brings the index in line with the files under root. Files whose size
// and modification time are unchanged are not read again.
func (x *Index) Scan(ctx context.Context, root string) (ScanStats, error) {
	var st ScanStats
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return st, fmt.Errorf("library: %q is not a directory", root)
	}
	prefix := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)

	known := map[string]stamp{}
	rows, err := x.db.QueryContext(ctx, `SELECT path, mtime, size FROM library WHERE substr(path, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var p string
		var s stamp
		if err := rows.Scan(&p, &s.mtime, &s.size); err != nil {
			_ = rows.Close()
			return st, err
		}
		known[p] = s
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	_ = rows.Close()

	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // an unreadable directory should not stop the scan
		}
		if d.IsDir() || !audioExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st.Files++
		seen[path] = true
		cur := stamp{info.ModTime().UnixNano(), info.Size()}
		if known[path] == cur {
			st.Unchanged++
			return nil
		}
		e, err := readEntry(path)
		if err != nil {
			st.Unreadable++
			return nil
		}
		if err := x.put(ctx, e, cur.mtime, cur.size); err != nil {
			return err
		}
		st.Indexed++
		return nil
	})
	if err != nil {
		return st, err
	}

	for p := range known {
		if seen[p] {
			continue
		}
		if _, err := x.db.ExecContext(ctx, `DELETE FROM library WHERE path = ?`, p); err != nil {
			return st, err
		}
		st.Removed++
	}
	return st, nil
}

func readEntry(path string) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = f.Close() }()
	e := Entry{Path: path}

	if strings.EqualFold(filepath.Ext(path), ".flac") {
		t, err := flacmeta.Read(f)
		if err != nil {
			return Entry{}, err
		}
		e.Title, e.ISRC = t.Title, t.ISRC
		e.Artists = append(t.Artists, t.AlbumArtist)
		return e, nil
	}

	m, err := tag.ReadFrom(f)
	if err != nil {
		return Entry{}, err
	}
	e.Title = m.Title()
	e.Artists = []string{m.Artist(), m.AlbumArtist()}
	for k, v := range m.Raw() {
		if s, ok := v.(string); ok && (strings.EqualFold(k, "isrc") || strings.EqualFold(k, "tsrc")) {
			e.ISRC = s
		}
	}
	return e, nil
}
