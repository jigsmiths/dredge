package flacmeta

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const streamInfoLen = 34

// DurationMS is the length a FLAC stream declares, or 0 if it does not say.
func DurationMS(src io.Reader) (int, error) {
	var head [8]byte
	if _, err := io.ReadFull(src, head[:]); err != nil || string(head[:4]) != "fLaC" {
		return 0, errors.New("flacmeta: not a FLAC file")
	}
	if head[4]&0x7f != blockStreamInfo {
		return 0, errors.New("flacmeta: no STREAMINFO block")
	}
	if n := int(head[5])<<16 | int(head[6])<<8 | int(head[7]); n != streamInfoLen {
		return 0, fmt.Errorf("flacmeta: STREAMINFO is %d bytes, want %d", n, streamInfoLen)
	}
	d := make([]byte, streamInfoLen)
	if _, err := io.ReadFull(src, d); err != nil {
		return 0, fmt.Errorf("flacmeta: truncated metadata: %w", err)
	}
	// 20 bits of sample rate, 3 of channels, 5 of depth, 36 of samples
	w := binary.BigEndian.Uint64(d[10:18])
	rate, samples := w>>44, w&(1<<36-1)
	if rate == 0 || samples == 0 {
		return 0, nil
	}
	return int(samples * 1000 / rate), nil
}
