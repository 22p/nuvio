// Package metadata enriches scanned titles with movie/TV metadata from TMDb.
// It only runs for real library items, never for the built-in demo entries.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"nuvio/internal/model"
)

// Enricher resolves TMDb matches sequentially to stay within the public API
// rate limits. A nil or empty APIKey makes it a no-op.
type Enricher struct {
	APIKey    string
	Language  string
	BaseURL   string
	ImageBase string
	HTTP      *http.Client

	mu          sync.Mutex
	genres      map[string]map[int]string
	disabled    bool
	disabledWhy string
}

// New creates an enricher. language defaults to zh-CN.
func New(apiKey, language string) *Enricher {
	if language == "" {
		language = "zh-CN"
	}
	return &Enricher{
		APIKey:    strings.TrimSpace(apiKey),
		Language:  language,
		BaseURL:   "https://api.themoviedb.org/3",
		ImageBase: "https://image.tmdb.org/t/p",
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		genres:    map[string]map[int]string{},
	}
}

type searchResult struct {
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	VoteAverage   float64 `json:"vote_average"`
	Popularity    float64 `json:"popularity"`
	GenreIDs      []int   `json:"genre_ids"`
}

type searchResponse struct {
	Results []searchResult `json:"results"`
}

// Enrich fills metadata for movies and series, then copies series artwork down
// to seasons and episodes that have no artwork of their own.
func (e *Enricher) Enrich(ctx context.Context, items []*model.Item) error {
	if e == nil || e.APIKey == "" || len(items) == 0 {
		return nil
	}
	var firstErr error
	failures := 0
	for _, it := range items {
		if it == nil || it.Demo {
			continue
		}
		if it.Type != model.TypeMovie && it.Type != model.TypeSeries {
			continue
		}
		if it.TMDBID > 0 && it.PosterURL != "" && it.LogoURL != "" && it.MetadataLanguage == e.Language {
			continue
		}
		if e.isDisabled() {
			break
		}
		res, err := e.lookup(ctx, it)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failures++
			if e.isDisabled() || failures >= 3 {
				e.disable("repeated request failures")
				break
			}
			continue
		}
		failures = 0
		if res == nil {
			continue
		}
		e.apply(ctx, it, *res)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(90 * time.Millisecond):
		}
	}
	e.inheritSeriesArt(items)
	e.enrichEpisodes(ctx, items)
	return firstErr
}

func (e *Enricher) isDisabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disabled
}

func (e *Enricher) disable(reason string) {
	e.mu.Lock()
	e.disabled = true
	e.disabledWhy = reason
	e.mu.Unlock()
	log.Printf("TMDb metadata disabled: %s", reason)
}

func (e *Enricher) lookup(ctx context.Context, it *model.Item) (*searchResult, error) {
	kind := "movie"
	year := it.Year
	if it.Type == model.TypeSeries {
		kind = "tv"
	}
	q := url.Values{}
	q.Set("api_key", e.APIKey)
	q.Set("language", e.Language)
	q.Set("query", it.Name)
	q.Set("include_adult", "false")
	if year > 0 {
		if kind == "movie" {
			q.Set("year", fmt.Sprintf("%d", year))
		} else {
			q.Set("first_air_date_year", fmt.Sprintf("%d", year))
		}
	}
	path := "/search/" + kind
	var out searchResponse
	if err := e.getJSON(ctx, path+"?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	// A year in the filename can be wrong. Retry once without it before giving up.
	if len(out.Results) == 0 && year > 0 {
		q.Del("year")
		q.Del("first_air_date_year")
		if err := e.getJSON(ctx, path+"?"+q.Encode(), &out); err != nil {
			return nil, err
		}
	}
	best, bestScore := -1, 0
	for i, r := range out.Results {
		score := matchScore(it.Name, it.Year, r)
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 || bestScore < 4 {
		return nil, nil
	}
	return &out.Results[best], nil
}

func matchScore(query string, year int, r searchResult) int {
	qn := normalizeTitle(query)
	if qn == "" {
		return 0
	}
	best := 0
	for _, candidate := range []string{r.Title, r.Name, r.OriginalTitle, r.OriginalName} {
		cn := normalizeTitle(candidate)
		if cn == "" {
			continue
		}
		switch {
		case cn == qn:
			best = max(best, 6)
		case strings.Contains(cn, qn) || strings.Contains(qn, cn):
			best = max(best, 3)
		}
	}
	resultYear := yearOf(r.ReleaseDate, r.FirstAirDate)
	if year > 0 && resultYear > 0 {
		switch {
		case year == resultYear:
			best += 3
		case abs(year-resultYear) == 1:
			best++
		}
	}
	return best
}

func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func yearOf(values ...string) int {
	for _, v := range values {
		if len(v) >= 4 {
			y := 0
			for i := 0; i < 4; i++ {
				if v[i] < '0' || v[i] > '9' {
					y = 0
					break
				}
				y = y*10 + int(v[i]-'0')
			}
			if y > 1800 {
				return y
			}
		}
	}
	return 0
}

func (e *Enricher) apply(ctx context.Context, it *model.Item, r searchResult) {
	local := it.MetadataSource == "nfo"
	it.TMDBID = r.ID
	it.MetadataLanguage = e.Language
	if local {
		// Local .nfo metadata wins; TMDb only fills the gaps.
		if it.Overview == "" {
			it.Overview = r.Overview
		}
		if it.Year == 0 {
			if y := yearOf(r.ReleaseDate, r.FirstAirDate); y > 0 {
				it.Year = y
				if it.PremiereDate == "" {
					it.PremiereDate = fmt.Sprintf("%d-01-01T00:00:00.0000000Z", y)
				}
			}
		}
		if it.Rating == 0 {
			it.Rating = r.VoteAverage
		}
		if len(it.Genres) == 0 {
			if names := e.genreNames(ctx, kindOfItem(it), r.GenreIDs); len(names) > 0 {
				it.Genres = names
			}
		}
	} else {
		it.MetadataSource = "tmdb"
		if r.Overview != "" {
			it.Overview = r.Overview
		}
		if y := yearOf(r.ReleaseDate, r.FirstAirDate); y > 0 {
			it.Year = y
			if it.PremiereDate == "" {
				it.PremiereDate = fmt.Sprintf("%d-01-01T00:00:00.0000000Z", y)
			}
		}
		if r.VoteAverage > 0 {
			it.Rating = r.VoteAverage
		}
		if names := e.genreNames(ctx, kindOfItem(it), r.GenreIDs); len(names) > 0 {
			it.Genres = names
		}
	}
	if r.PosterPath != "" {
		it.PosterURL = e.ImageBase + "/w780" + r.PosterPath
	}
	if r.BackdropPath != "" {
		it.BackdropURL = e.ImageBase + "/w1280" + r.BackdropPath
	}
	if it.LogoURL == "" {
		it.LogoURL = e.logoURL(ctx, kindOfItem(it), r.ID)
	}
	if len(it.People) == 0 {
		it.People = e.credits(ctx, kindOfItem(it), r.ID)
	}
}

// imageLanguage is TMDb's two-letter form of the configured language.
func (e *Enricher) imageLanguage() string {
	lang := strings.ToLower(strings.TrimSpace(e.Language))
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	return lang
}

type imagesResponse struct {
	Logos []struct {
		FilePath    string  `json:"file_path"`
		Language    string  `json:"iso_639_1"`
		VoteAverage float64 `json:"vote_average"`
		Width       int     `json:"width"`
	} `json:"logos"`
}

// logoURL picks the best transparent logo for a title. Only PNGs are used:
// TMDb also serves SVG logos, which the image cache cannot label reliably.
func (e *Enricher) logoURL(ctx context.Context, kind string, id int) string {
	if id <= 0 {
		return ""
	}
	lang := e.imageLanguage()
	q := url.Values{}
	q.Set("api_key", e.APIKey)
	q.Set("include_image_language", lang+",en,null")
	var out imagesResponse
	if err := e.getJSON(ctx, fmt.Sprintf("/%s/%d/images?%s", kind, id, q.Encode()), &out); err != nil {
		return ""
	}
	best, bestScore := "", -1.0
	for _, logo := range out.Logos {
		if logo.FilePath == "" || !strings.HasSuffix(strings.ToLower(logo.FilePath), ".png") {
			continue
		}
		score := logo.VoteAverage
		switch {
		case lang != "" && strings.EqualFold(logo.Language, lang):
			score += 100
		case strings.EqualFold(logo.Language, "en"):
			score += 50
		case logo.Language == "":
			score += 10
		}
		if logo.Width >= 500 {
			score++
		}
		if score > bestScore {
			best, bestScore = logo.FilePath, score
		}
	}
	if best == "" {
		return ""
	}
	return e.ImageBase + "/w500" + best
}

type seasonDetails struct {
	PosterPath string `json:"poster_path"`
	Episodes   []struct {
		EpisodeNumber int     `json:"episode_number"`
		Overview      string  `json:"overview"`
		StillPath     string  `json:"still_path"`
		AirDate       string  `json:"air_date"`
		VoteAverage   float64 `json:"vote_average"`
	} `json:"episodes"`
}

// enrichEpisodes fills episode stills, overviews and air dates from TMDb.
// Season details are fetched once per season and only when something is
// missing, so a steady-state rescan makes no extra requests.
func (e *Enricher) enrichEpisodes(ctx context.Context, items []*model.Item) {
	series := map[string]*model.Item{}
	seasons := map[string]map[int]*model.Item{}
	episodes := map[string]map[int][]*model.Item{}
	for _, it := range items {
		if it == nil {
			continue
		}
		switch it.Type {
		case model.TypeSeries:
			series[it.ID] = it
		case model.TypeSeason:
			if it.SeriesID == "" || it.SeasonNumber < 0 {
				continue
			}
			m := seasons[it.SeriesID]
			if m == nil {
				m = map[int]*model.Item{}
				seasons[it.SeriesID] = m
			}
			m[it.SeasonNumber] = it
		case model.TypeEpisode:
			if it.SeriesID == "" {
				continue
			}
			m := episodes[it.SeriesID]
			if m == nil {
				m = map[int][]*model.Item{}
				episodes[it.SeriesID] = m
			}
			m[it.SeasonNumber] = append(m[it.SeasonNumber], it)
		}
	}

	for seriesID, bySeason := range episodes {
		s := series[seriesID]
		if s == nil || s.TMDBID <= 0 {
			continue
		}
		numbers := make([]int, 0, len(bySeason))
		for number := range bySeason {
			numbers = append(numbers, number)
		}
		sort.Ints(numbers)
		for _, number := range numbers {
			group := bySeason[number]
			season := seasons[seriesID][number]
			if !e.needsSeason(ctx, group, season) {
				continue
			}
			details, err := e.fetchSeason(ctx, s.TMDBID, number)
			if err != nil {
				continue
			}
			if season != nil {
				// A dedicated season poster beats the inherited series one.
				if season.PosterPath == "" && details.PosterPath != "" {
					season.PosterURL = e.ImageBase + "/w500" + details.PosterPath
				}
				season.MetadataLanguage = e.Language
			}
			byNumber := make(map[int]int, len(details.Episodes))
			for i, ep := range details.Episodes {
				byNumber[ep.EpisodeNumber] = i
			}
			for _, ep := range group {
				i, ok := byNumber[ep.EpisodeNumber]
				if !ok {
					continue
				}
				info := details.Episodes[i]
				if ep.Overview == "" || ep.MetadataSource != "nfo" {
					if info.Overview != "" {
						ep.Overview = info.Overview
					}
				}
				if info.StillPath != "" {
					ep.ThumbURL = e.ImageBase + "/w300" + info.StillPath
				}
				if ep.PremiereDate == "" && info.AirDate != "" {
					ep.PremiereDate = info.AirDate + "T00:00:00.0000000Z"
				}
				if ep.Rating == 0 && info.VoteAverage > 0 {
					ep.Rating = info.VoteAverage
				}
				ep.MetadataLanguage = e.Language
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(90 * time.Millisecond):
			}
		}
	}
}

func (e *Enricher) needsSeason(ctx context.Context, episodes []*model.Item, season *model.Item) bool {
	if ctx.Err() != nil {
		return false
	}
	if season != nil && season.MetadataLanguage != e.Language {
		return true
	}
	for _, ep := range episodes {
		if ep.MetadataLanguage != e.Language || ep.Overview == "" || ep.ThumbURL == "" {
			return true
		}
	}
	return false
}

func (e *Enricher) fetchSeason(ctx context.Context, seriesID, season int) (*seasonDetails, error) {
	q := url.Values{}
	q.Set("api_key", e.APIKey)
	q.Set("language", e.Language)
	var out seasonDetails
	if err := e.getJSON(ctx, fmt.Sprintf("/tv/%d/season/%d?%s", seriesID, season, q.Encode()), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type creditsResponse struct {
	Cast []struct {
		Name        string `json:"name"`
		Character   string `json:"character"`
		ProfilePath string `json:"profile_path"`
	} `json:"cast"`
}

// credits returns the top billed actors for a movie or series. Failures are
// ignored so cast data never blocks the rest of the enrichment.
func (e *Enricher) credits(ctx context.Context, kind string, id int) []model.Person {
	if id <= 0 {
		return nil
	}
	q := url.Values{}
	q.Set("api_key", e.APIKey)
	q.Set("language", e.Language)
	var out creditsResponse
	if err := e.getJSON(ctx, fmt.Sprintf("/%s/%d/credits?%s", kind, id, q.Encode()), &out); err != nil {
		return nil
	}
	people := make([]model.Person, 0, len(out.Cast))
	for _, c := range out.Cast {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		p := model.Person{Name: name, Role: strings.TrimSpace(c.Character), Type: "Actor"}
		if c.ProfilePath != "" {
			p.ImageURL = e.ImageBase + "/w185" + c.ProfilePath
		}
		people = append(people, p)
		if len(people) == 24 {
			break
		}
	}
	return people
}

func kindOfItem(it *model.Item) string {
	if it.Type == model.TypeSeries {
		return "tv"
	}
	return "movie"
}

func (e *Enricher) genreNames(ctx context.Context, kind string, ids []int) []string {
	if len(ids) == 0 {
		return nil
	}
	names := e.genreMap(ctx, kind)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if name, ok := names[id]; ok && name != "" {
			out = append(out, name)
		}
	}
	return out
}

func (e *Enricher) genreMap(ctx context.Context, kind string) map[int]string {
	e.mu.Lock()
	if m, ok := e.genres[kind]; ok {
		e.mu.Unlock()
		return m
	}
	e.mu.Unlock()
	q := url.Values{}
	q.Set("api_key", e.APIKey)
	q.Set("language", e.Language)
	var out struct {
		Genres []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"genres"`
	}
	if err := e.getJSON(ctx, "/genre/"+kind+"/list?"+q.Encode(), &out); err != nil {
		return nil
	}
	m := make(map[int]string, len(out.Genres))
	for _, g := range out.Genres {
		m[g.ID] = g.Name
	}
	e.mu.Lock()
	e.genres[kind] = m
	e.mu.Unlock()
	return m
}

func (e *Enricher) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(e.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		e.disable("invalid or unauthorized API key")
		return fmt.Errorf("TMDb HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("TMDb HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

func (e *Enricher) inheritSeriesArt(items []*model.Item) {
	series := map[string]*model.Item{}
	for _, it := range items {
		if it != nil && it.Type == model.TypeSeries {
			series[it.ID] = it
		}
	}
	for _, it := range items {
		if it == nil || it.SeriesID == "" {
			continue
		}
		s := series[it.SeriesID]
		if s == nil {
			continue
		}
		if it.PosterPath == "" && it.PosterURL == "" {
			it.PosterURL = s.PosterURL
		}
		if it.BackdropPath == "" && it.BackdropURL == "" {
			it.BackdropURL = s.BackdropURL
		}
		if len(it.Genres) == 0 {
			it.Genres = s.Genres
		}
		if len(it.People) == 0 {
			it.People = s.People
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
