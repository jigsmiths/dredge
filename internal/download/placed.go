package download

import "github.com/jigsmiths/dredge/internal/resolve"

// Placed lists the library file of every item that is there, in item order. A
// duplicate takes the file of the track it repeats. results may be shorter
// than items, or nil.
func Placed(items []resolve.Item, results []Result) []string {
	here := func(i int) string {
		if i < len(results) {
			switch results[i].Outcome {
			case Downloaded, Exists:
				return results[i].Path
			}
		}
		if items[i].Status == resolve.Have {
			return items[i].Path
		}
		return ""
	}
	byISRC := map[string]string{}
	for i, it := range items {
		if p := here(i); p != "" && it.Track.ISRC != "" {
			byISRC[it.Track.ISRC] = p
		}
	}
	var paths []string
	for i, it := range items {
		p := here(i)
		if p == "" && it.Status == resolve.Duplicate {
			p = byISRC[it.Track.ISRC]
		}
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
