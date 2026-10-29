package media

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"

	"nuvio/internal/model"
)

// nfoData is the subset of Kodi-style .nfo metadata Nuvio understands. Numeric
// fields are strings so odd values in real-world NFOs do not abort the parse.
type nfoData struct {
	Title      string         `xml:"title"`
	Plot       string         `xml:"plot"`
	Outline    string         `xml:"outline"`
	Tagline    string         `xml:"tagline"`
	Year       string         `xml:"year"`
	Rating     nfoRatingField `xml:"rating"`
	Ratings    nfoRatings     `xml:"ratings"`
	MPAARating string         `xml:"mpaa"`
	Runtime    string         `xml:"runtime"`
	Genres     []string       `xml:"genre"`
	Actors     []nfoActor     `xml:"actor"`
}

// nfoRatingField accepts both <rating>7.5</rating> and <rating><value>7.5</value></rating>.
type nfoRatingField struct {
	Text  string `xml:",chardata"`
	Value string `xml:"value"`
}

type nfoRatings struct {
	Items []struct {
		Value string `xml:"value"`
	} `xml:"rating"`
}

type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Thumb string `xml:"thumb"`
}

// movieNFO looks for <video>.nfo first and falls back to movie.nfo.
func movieNFO(ve *videoEntry) *nfoData {
	path := findNFO(ve.dir, ve.base+".nfo", "movie.nfo")
	if path == "" {
		return nil
	}
	return readNFO(path)
}

// episodeNFO looks for an NFO named after the episode file.
func episodeNFO(ve *videoEntry) *nfoData {
	path := findNFO(ve.dir, ve.base+".nfo", "episode.nfo")
	if path == "" {
		return nil
	}
	return readNFO(path)
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
	data = decodeNFO(data)
	var n nfoData
	dec := xml.NewDecoder(bytes.NewReader(data))
	// NFOs are converted to UTF-8 above; accept whatever the declaration says.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	if err := dec.Decode(&n); err != nil {
		return nil
	}
	return &n
}

// decodeNFO normalises UTF-8/GBK/GB18030/Big5 NFO files to UTF-8.
func decodeNFO(data []byte) []byte {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	switch declaredEncoding(data) {
	case "gbk", "gb2312", "gb18030", "cp936":
		if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), data); err == nil {
			return out
		}
	case "big5", "cp950":
		if out, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), data); err == nil {
			return out
		}
	}
	if utf8.Valid(data) {
		return data
	}
	if out, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), data); err == nil {
		return out
	}
	if out, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), data); err == nil {
		return out
	}
	return data
}

func declaredEncoding(data []byte) string {
	head := data
	if len(head) > 256 {
		head = head[:256]
	}
	s := strings.ToLower(string(head))
	i := strings.Index(s, "encoding=")
	if i < 0 {
		return ""
	}
	s = strings.TrimLeft(s[i+len("encoding="):], " \t\r\n")
	if len(s) == 0 || (s[0] != '"' && s[0] != '\'') {
		return ""
	}
	q := s[0]
	s = s[1:]
	if j := strings.IndexByte(s, q); j >= 0 {
		return s[:j]
	}
	return ""
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
		it.SortName = model.SortName(v)
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
	if year := int(nfoNumber(n.Year)); year > 0 {
		it.Year = year
		changed = true
	}
	if rating := nfoRating(n); rating > 0 {
		it.Rating = rating
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
	if minutes := int(nfoNumber(n.Runtime)); minutes > 0 && it.RuntimeTicks == 0 {
		it.RuntimeTicks = int64(minutes) * 60 * 10_000_000
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

// nfoRating handles both <rating>7.5</rating> and the newer nested ratings.
func nfoRating(n *nfoData) float64 {
	if v := nfoNumber(n.Rating.Text); v > 0 {
		return v
	}
	if v := nfoNumber(n.Rating.Value); v > 0 {
		return v
	}
	for _, r := range n.Ratings.Items {
		if v := nfoNumber(r.Value); v > 0 {
			return v
		}
	}
	return 0
}

func nfoNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	start := strings.IndexAny(s, "0123456789")
	if start < 0 {
		return 0
	}
	s = s[start:]
	end := 0
	for end < len(s) && ((s[end] >= '0' && s[end] <= '9') || s[end] == '.') {
		end++
	}
	if end == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSuffix(s[:end], "."), 64)
	return v
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
