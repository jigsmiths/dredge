// Package flacmeta rewrites the tags of a FLAC file. It streams: only the
// metadata blocks are held in memory, never the audio, so several tracks of
// tens of megabytes can be tagged at once.
package flacmeta

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	blockStreamInfo    = 0
	blockPadding       = 1
	blockSeekTable     = 3
	blockVorbisComment = 4
	blockPicture       = 6

	frontCover = 3
	vendor     = "dredge"
)

type Tags struct {
	Title       string
	Artists     []string
	Album       string
	AlbumArtist string
	TrackNo     int
	DiscNo      int
	Date        string
	ISRC        string
	Cover       []byte // JPEG or PNG; anything else is not embedded
}

// Rewrite copies a FLAC stream from src to dst with its tags and cover
// replaced. Nothing is written to dst unless the metadata is valid.
func Rewrite(src io.Reader, dst io.Writer, t Tags) error {
	var marker [4]byte
	if _, err := io.ReadFull(src, marker[:]); err != nil || string(marker[:]) != "fLaC" {
		return errors.New("flacmeta: not a FLAC file")
	}

	var kept [][]byte // type byte, then the data
	sawInfo := false
	for {
		var h [4]byte
		if _, err := io.ReadFull(src, h[:]); err != nil {
			return fmt.Errorf("flacmeta: truncated metadata: %w", err)
		}
		typ, last := h[0]&0x7f, h[0]&0x80 != 0
		data := make([]byte, int(h[1])<<16|int(h[2])<<8|int(h[3]))
		if _, err := io.ReadFull(src, data); err != nil {
			return fmt.Errorf("flacmeta: truncated metadata: %w", err)
		}
		switch typ {
		case blockPadding, blockVorbisComment, blockPicture:
		default:
			kept = append(kept, append([]byte{typ}, data...))
		}
		sawInfo = sawInfo || typ == blockStreamInfo
		if last {
			break
		}
	}
	if !sawInfo {
		return errors.New("flacmeta: no STREAMINFO block")
	}

	blocks := append(kept, append([]byte{blockVorbisComment}, vorbisComments(t)...))
	if pic := picture(t.Cover); pic != nil {
		blocks = append(blocks, append([]byte{blockPicture}, pic...))
	}

	w := bufio.NewWriterSize(dst, 1<<20)
	if _, err := w.WriteString("fLaC"); err != nil {
		return err
	}
	for i, b := range blocks {
		h := b[0]
		if i == len(blocks)-1 {
			h |= 0x80
		}
		n := len(b) - 1
		if _, err := w.Write([]byte{h, byte(n >> 16), byte(n >> 8), byte(n)}); err != nil {
			return err
		}
		if _, err := w.Write(b[1:]); err != nil {
			return err
		}
	}
	if _, err := io.Copy(w, src); err != nil {
		return err
	}
	return w.Flush()
}

// Read returns the tags of a FLAC stream, with every value of a repeated
// key such as ARTIST. It stops after the metadata and never reads the audio.
func Read(src io.Reader) (Tags, error) {
	var marker [4]byte
	if _, err := io.ReadFull(src, marker[:]); err != nil || string(marker[:]) != "fLaC" {
		return Tags{}, errors.New("flacmeta: not a FLAC file")
	}
	var t Tags
	for {
		var h [4]byte
		if _, err := io.ReadFull(src, h[:]); err != nil {
			return Tags{}, fmt.Errorf("flacmeta: truncated metadata: %w", err)
		}
		n := int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
		if h[0]&0x7f == blockVorbisComment {
			data := make([]byte, n)
			if _, err := io.ReadFull(src, data); err != nil {
				return Tags{}, fmt.Errorf("flacmeta: truncated metadata: %w", err)
			}
			if err := parseComments(data, &t); err != nil {
				return Tags{}, err
			}
		} else if _, err := io.CopyN(io.Discard, src, n); err != nil {
			return Tags{}, fmt.Errorf("flacmeta: truncated metadata: %w", err)
		}
		if h[0]&0x80 != 0 {
			return t, nil
		}
	}
}

func parseComments(d []byte, t *Tags) error {
	next := func() ([]byte, bool) {
		if len(d) < 4 {
			return nil, false
		}
		n := int(binary.LittleEndian.Uint32(d))
		if n < 0 || len(d)-4 < n {
			return nil, false
		}
		v := d[4 : 4+n]
		d = d[4+n:]
		return v, true
	}
	if _, ok := next(); !ok { // vendor
		return errors.New("flacmeta: corrupt tag block")
	}
	if len(d) < 4 {
		return errors.New("flacmeta: corrupt tag block")
	}
	count := binary.LittleEndian.Uint32(d)
	d = d[4:]
	for range count {
		c, ok := next()
		if !ok {
			return errors.New("flacmeta: corrupt tag block")
		}
		key, val, found := strings.Cut(string(c), "=")
		if !found {
			continue
		}
		switch strings.ToUpper(key) {
		case "TITLE":
			t.Title = val
		case "ARTIST":
			t.Artists = append(t.Artists, val)
		case "ALBUM":
			t.Album = val
		case "ALBUMARTIST":
			t.AlbumArtist = val
		case "TRACKNUMBER":
			t.TrackNo = leadingInt(val)
		case "DISCNUMBER":
			t.DiscNo = leadingInt(val)
		case "DATE":
			t.Date = val
		case "ISRC":
			t.ISRC = val
		}
	}
	return nil
}

// leadingInt reads "3" from "3" or "3/12".
func leadingInt(s string) int {
	n, _, _ := strings.Cut(s, "/")
	v, _ := strconv.Atoi(strings.TrimSpace(n))
	return v
}

func vorbisComments(t Tags) []byte {
	var comments []string
	add := func(key, val string) {
		if val = strings.TrimSpace(val); val != "" {
			comments = append(comments, key+"="+val)
		}
	}
	add("TITLE", t.Title)
	for _, a := range t.Artists {
		add("ARTIST", a)
	}
	add("ALBUM", t.Album)
	add("ALBUMARTIST", t.AlbumArtist)
	if t.TrackNo > 0 {
		add("TRACKNUMBER", strconv.Itoa(t.TrackNo))
	}
	if t.DiscNo > 0 {
		add("DISCNUMBER", strconv.Itoa(t.DiscNo))
	}
	add("DATE", t.Date)
	add("ISRC", t.ISRC)

	d := binary.LittleEndian.AppendUint32(nil, uint32(len(vendor)))
	d = append(d, vendor...)
	d = binary.LittleEndian.AppendUint32(d, uint32(len(comments)))
	for _, c := range comments {
		d = binary.LittleEndian.AppendUint32(d, uint32(len(c)))
		d = append(d, c...)
	}
	return d
}

func picture(img []byte) []byte {
	var mime string
	switch {
	case bytes.HasPrefix(img, []byte("\xff\xd8\xff")):
		mime = "image/jpeg"
	case bytes.HasPrefix(img, []byte("\x89PNG")):
		mime = "image/png"
	default:
		return nil
	}
	d := binary.BigEndian.AppendUint32(nil, frontCover)
	d = binary.BigEndian.AppendUint32(d, uint32(len(mime)))
	d = append(d, mime...)
	d = binary.BigEndian.AppendUint32(d, 0) // description
	d = append(d, make([]byte, 16)...)      // width, height, depth, colours: unknown
	d = binary.BigEndian.AppendUint32(d, uint32(len(img)))
	return append(d, img...)
}

// Retag rewrites the file in place through a temporary file, so a failure
// leaves the original untouched.
func Retag(path string, t Tags) (err error) {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".retag-*.part")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = Rewrite(bufio.NewReaderSize(src, 1<<20), tmp, t); err != nil {
		return err
	}
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
