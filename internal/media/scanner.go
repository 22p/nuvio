package media

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"nuvio/internal/model"
)

// Scanner walks a library path and produces catalog items.
type Scanner struct {
	FFprobe string
}

type videoEntry struct {
	path    string
	dir     string
	base    string // filename without extension
	name    string
	ext     string
	size    int64
	modTime time.Time
	media   model.MediaInfo
	subs    []model.Subtitle
}

type artSet struct {
	poster    string
	backdrop  string
	logo      string
	landscape string
}

var (
	episodeRe   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})[ ._-]?e(\d{1,3})(?:[^a-z0-9]|$)`)
	episodeRe2  = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{1,2})(?:[^0-9]|$)`)
	seasonDirRe = regexp.MustCompile(`(?i)^(?:season|series|s)[ ._-]*0*(\d{1,3})$`)
	specialsRe  = regexp.MustCompile(`(?i)^(?:specials?|extras?|bonus)$`)
	yearRe      = regexp.MustCompile(`(?:\(|\[|\.|\s)((?:19|20)\d{2})(?:\)|\]|\.|\s|$)`)
	bracketRe   = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	tokenRe     = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|576p|480p|uhd|bluray|blu-ray|brrip|bdrip|webrip|web-dl|webdl|hdtv|dvdrip|remux|x264|x265|h264|h265|hevc|avc|10bit|8bit|hdr|dv|dolby|vision|atmos|dts(?:-hd)?|truehd|aac|ac3|eac3|ddp?5[ .]1|proper|repack|extended|unrated|remastered|multi|dual|sub|eng|chi)\b`)
	multiSpace  = regexp.MustCompile(`\s{2,}`)
)

var skipDirs = map[string]bool{
	"@eaDir": true, "#recycle": true, "lost+found": true, "$RECYCLE.BIN": true,
	"System Volume Information": true, "node_modules": true, ".git": true,
	"transcodes": true, "cache": true, "extras": true, "featurettes": true,
}

// Scan reads every video below lib.Paths.
func (s *Scanner) Scan(ctx context.Context, lib *model.Library) ([]*model.Item, error) {
	videos := []*videoEntry{}
	artByDir := map[string]*artSet{}
	subsByDir := map[string][]string{}
	seen := map[string]bool{}

	for _, root := range lib.Paths {
		root = filepath.Clean(root)
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				name := d.Name()
				if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			dir := filepath.Dir(path)
			ext := strings.ToLower(filepath.Ext(name))
			if kind := artKind(name); kind != "" {
				set := artByDir[dir]
				if set == nil {
					set = &artSet{}
					artByDir[dir] = set
				}
				switch kind {
				case "poster":
					if set.poster == "" {
						set.poster = path
					}
				case "backdrop":
					if set.backdrop == "" {
						set.backdrop = path
					}
				case "logo":
					if set.logo == "" {
						set.logo = path
					}
				case "landscape":
					if set.landscape == "" {
						set.landscape = path
					}
				}
				return nil
			}
			if SubtitleExtensions[ext] {
				subsByDir[dir] = append(subsByDir[dir], path)
				return nil
			}
			if !VideoExtensions[ext] {
				return nil
			}
			if seen[path] {
				return nil
			}
			seen[path] = true
			info, _ := d.Info()
			ve := &videoEntry{
				path: path,
				dir:  dir,
				base: strings.TrimSuffix(name, filepath.Ext(name)),
				name: name,
				ext:  ext,
			}
			if info != nil {
				ve.size = info.Size()
				ve.modTime = info.ModTime()
			}
			videos = append(videos, ve)
			return nil
		})
		if err != nil && ctx.Err() != nil {
			return nil, err
		}
	}

	// Probe technical metadata in parallel; ffprobe can take a while.
	workers := runtime.NumCPU()
	if workers > 6 {
		workers = 6
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan *videoEntry)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ve := range jobs {
				ve.media = ProbeFile(ctx, ve.path, s.FFprobe)
			}
		}()
	}
	for _, ve := range videos {
		jobs <- ve
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	for _, ve := range videos {
		ve.subs = matchSubtitles(ve.dir, ve.base, subsByDir[ve.dir])
		normalizeMedia(&ve.media, ve.subs)
	}

	rootSet := map[string]bool{}
	for _, p := range lib.Paths {
		rootSet[filepath.Clean(p)] = true
	}

	var items []*model.Item
	if lib.Type == model.LibraryMovies {
		for _, ve := range videos {
			items = append(items, s.movieItem(lib, ve, artByDir, rootSet))
		}
		return items, nil
	}

	// Group episodes into series -> season.
	type seriesAgg struct {
		dir       string
		key       string
		id        string
		name      string
		year      int
		seasons   map[int][]*model.Item
		added     time.Time
		poster    string
		backdrop  string
		logo      string
		landscape string
		nfo       *nfoData
	}
	groups := map[string]*seriesAgg{}
	var movies []*videoEntry
	for _, ve := range videos {
		season, episode, ok := parseEpisode(ve.base, ve.dir)
		if !ok {
			if lib.Type == model.LibraryShows {
				season, episode, ok = 1, 0, true
			} else {
				movies = append(movies, ve)
				continue
			}
		}
		seriesDir := seriesDirFor(ve.dir)
		seriesName, seriesYear := cleanTitleYear(filepath.Base(seriesDir))
		seriesKey := seriesDir
		if rootSet[seriesDir] {
			if prefix, prefixYear := seriesPrefix(ve.base); prefix != "" {
				seriesName = prefix
				seriesKey = seriesDir + "::" + strings.ToLower(prefix)
				if prefixYear > 0 {
					seriesYear = prefixYear
				}
			}
		}
		agg := groups[seriesKey]
		if agg == nil {
			agg = &seriesAgg{
				dir: seriesDir, key: seriesKey,
				id:   md5hex("series:" + lib.ID + ":" + seriesKey),
				name: seriesName, year: seriesYear,
				seasons: map[int][]*model.Item{},
			}
			groups[seriesKey] = agg
		}
		if agg.added.Before(ve.modTime) {
			agg.added = ve.modTime
		}
		art := artByDir[seriesDir]
		if art != nil {
			agg.poster, agg.backdrop = art.poster, art.backdrop
			agg.logo, agg.landscape = art.logo, art.landscape
		}

		if episode == 0 {
			episode = len(agg.seasons[season]) + 1
		}
		seriesID := agg.id
		seasonID := md5hex(fmt.Sprintf("season:%s:%d", seriesID, season))
		epName, epYear := episodeTitle(ve.base, season, episode)
		if epYear == 0 {
			epYear = agg.year
		}
		ep := &model.Item{
			ID:            md5hex("episode:" + ve.path),
			Type:          model.TypeEpisode,
			Name:          epName,
			SortName:      sortName(epName),
			Year:          epYear,
			LibraryID:     lib.ID,
			ParentID:      seasonID,
			SeriesID:      seriesID,
			SeriesName:    agg.name,
			SeasonID:      seasonID,
			SeasonName:    seasonName(season),
			SeasonNumber:  season,
			EpisodeNumber: episode,
			Path:          ve.path,
			Container:     ve.media.Container,
			Size:          ve.size,
			AddedAt:       ve.modTime,
			Media:         &ve.media,
			PosterPath:    agg.poster,
			BackdropPath:  agg.backdrop,
			LogoPath:      agg.logo,
			ThumbPath:     agg.landscape,
			ArtSeed:       md5hex("art:" + ve.path),
		}
		if agg.year == 0 && epYear > 0 {
			agg.year = epYear
		}
		agg.seasons[season] = append(agg.seasons[season], ep)
	}

	for _, agg := range groups {
		if n := seriesNFO(agg.dir, agg.name); n != nil {
			agg.nfo = n
			if title := strings.TrimSpace(n.Title); title != "" {
				agg.name = title
			}
			if n.Year > 0 && agg.year == 0 {
				agg.year = n.Year
			}
		}
	}

	for _, agg := range groups {
		seriesID := agg.id
		series := &model.Item{
			ID:           seriesID,
			Type:         model.TypeSeries,
			Name:         agg.name,
			SortName:     sortName(agg.name),
			Year:         agg.year,
			LibraryID:    lib.ID,
			ParentID:     lib.ID,
			AddedAt:      agg.added,
			PosterPath:   agg.poster,
			BackdropPath: agg.backdrop,
			LogoPath:     agg.logo,
			ThumbPath:    agg.landscape,
			ArtSeed:      md5hex("art:" + agg.key),
			ChildCount:   len(agg.seasons),
		}
		applyNFO(series, agg.nfo)
		if series.AddedAt.IsZero() {
			series.AddedAt = time.Now().Add(-time.Hour * 24 * 365)
		}
		nums := make([]int, 0, len(agg.seasons))
		for n := range agg.seasons {
			nums = append(nums, n)
		}
		sort.Ints(nums)
		for _, n := range nums {
			eps := agg.seasons[n]
			sort.Slice(eps, func(i, j int) bool {
				if eps[i].EpisodeNumber != eps[j].EpisodeNumber {
					return eps[i].EpisodeNumber < eps[j].EpisodeNumber
				}
				return eps[i].Name < eps[j].Name
			})
			for _, ep := range eps {
				ep.ParentID = md5hex(fmt.Sprintf("season:%s:%d", seriesID, n))
				ep.SeriesID = seriesID
				ep.SeriesName = agg.name
				ep.SeasonID = ep.ParentID
				ep.SeasonName = seasonName(n)
				if agg.nfo != nil {
					if len(ep.People) == 0 {
						ep.People = nfoPeople(agg.nfo.Actors)
					}
					if len(ep.Genres) == 0 {
						ep.Genres = cleanNFOStrings(agg.nfo.Genres)
					}
				}
			}
			season := &model.Item{
				ID:           eps[0].ParentID,
				Type:         model.TypeSeason,
				Name:         seasonName(n),
				SortName:     fmt.Sprintf("%03d", n),
				LibraryID:    lib.ID,
				ParentID:     seriesID,
				SeriesID:     seriesID,
				SeriesName:   agg.name,
				SeasonNumber: n,
				ChildCount:   len(eps),
				AddedAt:      eps[len(eps)-1].AddedAt,
				PosterPath:   agg.poster,
				BackdropPath: agg.backdrop,
				LogoPath:     agg.logo,
				ThumbPath:    agg.landscape,
				ArtSeed:      md5hex(fmt.Sprintf("art:%s:%d", agg.dir, n)),
			}
			items = append(items, season)
			items = append(items, eps...)
		}
		items = append(items, series)
	}

	for _, ve := range movies {
		items = append(items, s.movieItem(lib, ve, artByDir, rootSet))
	}

	// Assign stable, unique sort keys and durations.
	for _, it := range items {
		if it.Media != nil {
			it.RuntimeTicks = it.Media.DurationTicks
		}
	}
	return items, nil
}

func (s *Scanner) movieItem(lib *model.Library, ve *videoEntry, artByDir map[string]*artSet, roots map[string]bool) *model.Item {
	title, year := movieTitle(ve, roots)
	art := artByDir[ve.dir]
	item := &model.Item{
		ID:           md5hex("movie:" + ve.path),
		Type:         model.TypeMovie,
		Name:         title,
		SortName:     sortName(title),
		Year:         year,
		LibraryID:    lib.ID,
		ParentID:     lib.ID,
		Path:         ve.path,
		Container:    ve.media.Container,
		Size:         ve.size,
		AddedAt:      ve.modTime,
		Media:        &ve.media,
		ArtSeed:      md5hex("art:" + ve.path),
		RuntimeTicks: ve.media.DurationTicks,
	}
	if art != nil {
		item.PosterPath = art.poster
		item.BackdropPath = art.backdrop
		item.LogoPath = art.logo
		item.ThumbPath = art.landscape
	}
	applyNFO(item, movieNFO(ve))
	return item
}

func artKind(name string) string {
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		return ""
	}
	switch stem {
	case "poster", "folder", "cover", "default", "movie":
		return "poster"
	case "fanart", "backdrop", "background":
		return "backdrop"
	case "logo", "clearlogo":
		return "logo"
	case "landscape", "thumb":
		return "landscape"
	}
	return ""
}

func parseEpisode(base, dir string) (season, episode int, ok bool) {
	if m := episodeRe.FindStringSubmatch(base); len(m) == 3 {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		return season, episode, true
	}
	if m := episodeRe2.FindStringSubmatch(base); len(m) == 3 {
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
		return season, episode, true
	}
	// Fall back to "Show/Season 1/03 Title.mkv".
	if sn, ok := seasonOfDir(dir); ok {
		if ep := leadingNumber(base); ep > 0 {
			return sn, ep, true
		}
		return sn, 0, true
	}
	return 0, 0, false
}

func seriesPrefix(base string) (string, int) {
	loc := episodeRe.FindStringIndex(base)
	if loc == nil {
		loc = episodeRe2.FindStringIndex(base)
	}
	if loc == nil || loc[0] == 0 {
		return "", 0
	}
	return cleanTitleYear(base[:loc[0]])
}

func seriesDirFor(dir string) string {
	if _, ok := seasonOfDir(dir); ok {
		return filepath.Dir(dir)
	}
	return dir
}

func seasonOfDir(dir string) (int, bool) {
	name := filepath.Base(dir)
	if m := seasonDirRe.FindStringSubmatch(name); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	if specialsRe.MatchString(name) {
		return 0, true
	}
	return 0, false
}

func seasonName(n int) string {
	if n == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %d", n)
}

func leadingNumber(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

func episodeTitle(base string, season, episode int) (string, int) {
	title := base
	year := 0
	if m := yearRe.FindStringSubmatch(title); len(m) == 2 {
		year, _ = strconv.Atoi(m[1])
	}
	if loc := episodeRe.FindStringIndex(title); loc != nil {
		title = title[loc[1]:]
	} else if loc := episodeRe2.FindStringIndex(title); loc != nil {
		title = title[loc[1]:]
	} else {
		title = strings.TrimLeft(title, "0123456789 ._-")
	}
	title = cleanTitle(title)
	if title == "" {
		title = fmt.Sprintf("Episode %d", episode)
	}
	return title, year
}

func movieTitle(ve *videoEntry, roots map[string]bool) (string, int) {
	base := ve.base
	dir := filepath.Dir(ve.path)
	folder := filepath.Base(dir)
	if !roots[dir] && filepath.Dir(dir) != dir {
		generic := regexp.MustCompile(`(?i)^(movie|video|index|main|feature|film|title)$`).MatchString(base)
		if generic || yearRe.MatchString(folder) {
			base = folder
		}
	}
	return cleanTitleYear(base)
}

func cleanTitleYear(s string) (string, int) {
	s = strings.TrimSuffix(s, filepath.Ext(s))
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	year := 0
	if m := yearRe.FindStringSubmatch(s); len(m) == 2 {
		year, _ = strconv.Atoi(m[1])
	}
	s = bracketRe.ReplaceAllString(s, " ")
	s = tokenRe.ReplaceAllString(s, " ")
	if m := yearRe.FindStringIndex(s); m != nil {
		s = s[:m[0]] + s[m[1]:]
	}
	s = cleanTitle(s)
	if s == "" {
		s = "Untitled"
	}
	return s, year
}

func cleanTitle(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ", " - ", " ").Replace(s)
	s = tokenRe.ReplaceAllString(s, " ")
	s = multiSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(strings.Trim(s, "-–—[](){}"))
}

func sortName(name string) string {
	l := strings.ToLower(name)
	for _, prefix := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, prefix) {
			return name[len(prefix):]
		}
	}
	return name
}

func matchSubtitles(dir, base string, files []string) []model.Subtitle {
	var out []model.Subtitle
	for _, path := range files {
		name := filepath.Base(path)
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !strings.EqualFold(stem, base) && !strings.HasPrefix(strings.ToLower(stem), strings.ToLower(base)+".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		lang := DetectSubtitleLanguage(name, base)
		s := model.Subtitle{
			Language: lang,
			Title:    LanguageName(lang),
			Codec:    subtitleCodec(ext),
			Path:     path,
		}
		if strings.Contains(strings.ToLower(stem), "forced") {
			s.Forced = true
			s.Title += " (Forced)"
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Language != out[j].Language {
			return out[i].Language < out[j].Language
		}
		return out[i].Path < out[j].Path
	})
	for i := range out {
		if i == 0 {
			out[i].Default = true
		}
	}
	return out
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
