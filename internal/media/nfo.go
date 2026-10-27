package media

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	"nuvio/internal/model"
)

// nfoData is the subset of Kodi-style .nfo metadata Nuvio understands.
type nfoData struct {
	Title      string     `xml:"title"`
	Plot       string     `xml:"plot"`
	Outline    string     `xml:"outline"`
	Tagline    string     `xml:"tagline"`
	Year       int        `xml:"year"`
	Rating     float64    `xml:"rating"`
	MPAARating string     `xml:"mpaa"`
	Runtime    int        `xml:"runtime"`
	Genres     []string   `xml:"genre"`
	Actors     []nfoActor `xml:"actor"`
}

type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Thumb string `xml:"thumb"`
}

// movieNFO looks for <video>.nfo first and falls back to movie.nfo.
func movieNFO(ve *videoEntry) *nfoData {
	name := findNFO(ve.dir, ve.base+".nfo", "movie.nfo")
	if name == "" {
		return nil
	}
	return readNFO(name)
}

// seriesNFO looks for <folder name>.nfo, the cleaned series title and the
// usual Kodi names.
func seriesNFO(dir, name string) *nfoData {
	base := filepath.Base(dir)
	path := findNFO(dir, base+".nfo", name+".nfo", "tvshow.nfo", "series.nfo")
	if path == "" {
		return nil
	}
	return readNFO(path)
}

func readNFO(path string) *nfoData {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var n nfoData
	if err := xml.Unmarshal(data, &n); err != nil {
		return nil
	}
	return &n
}

// applyNFO copies non-empty NFO values onto an item. Local metadata wins over
// TMDb, so the item is marked with MetadataSource "nfo".
func applyNFO(it *model.Item, n *nfoData) {
	if n == nil {
		return
	}
	changed := false
	if v := strings.TrimSpace(n.Title); v != "" {
		it.Name = v
		it.SortName = sortName(v)
		changed = true
	}
	if v := firstNonEmpty(n.Plot, n.Outline); v != "" {
		it.Overview = strings.TrimSpace(v)
		changed = true
	}
	if v := strings.TrimSpace(n.Tagline); v != "" {
		it.Taglines = []string{v}
		changed = true
	}
	if n.Year > 0 {
		it.Year = n.Year
		changed = true
	}
	if n.Rating > 0 {
		it.Rating = n.Rating
		changed = true
	}
	if v := strings.TrimSpace(n.MPAARating); v != "" {
		it.OfficialRating = v
		changed = true
	}
	if genres := cleanNFOStrings(n.Genres); len(genres) > 0 {
		it.Genres = genres
		changed = true
	}
	if n.Runtime > 0 && it.RuntimeTicks == 0 {
		it.RuntimeTicks = int64(n.Runtime) * 60 * 10_000_000
		changed = true
	}
	if people := nfoPeople(n.Actors); len(people) > 0 {
		it.People = people
		changed = true
	}
	if changed {
		it.MetadataSource = "nfo"
	}
}

func nfoPeople(actors []nfoActor) []model.Person {
	out := make([]model.Person, 0, len(actors))
	for _, a := range actors {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		p := model.Person{Name: name, Role: strings.TrimSpace(a.Role), Type: "Actor"}
		if thumb := strings.TrimSpace(a.Thumb); strings.HasPrefix(thumb, "http://") || strings.HasPrefix(thumb, "https://") {
			p.ImageURL = thumb
		}
		out = append(out, p)
		if len(out) == 30 {
			break
		}
	}
	return out
}

func cleanNFOStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// findNFO checks exact names first and then falls back to a case-insensitive
// match so MOVIE.NFO and SHOW.NFO work on case-sensitive filesystems.
func findNFO(dir string, names ...string) string {
	for _, name := range names {
		if name == "" {
			continue
		}
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	want := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			want[strings.ToLower(name)] = true
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if want[strings.ToLower(entry.Name())] {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}
