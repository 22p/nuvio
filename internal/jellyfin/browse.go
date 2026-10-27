package jellyfin

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"nuvio/internal/hls"
	"nuvio/internal/model"
)

// Catalog derived endpoints ------------------------------------------------

func genreID(name string) string {
	sum := md5.Sum([]byte("genre:" + strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

func genreItem(name string) *model.Item {
	name = strings.TrimSpace(name)
	return &model.Item{
		ID: genreID(name), Type: "Genre", Name: name, SortName: name,
		ArtSeed: "genre:" + strings.ToLower(name),
	}
}

func personItem(name, imageURL string) *model.Item {
	name = strings.TrimSpace(name)
	return &model.Item{
		ID: personID(name), Type: "Person", Name: name, SortName: name,
		ArtSeed: "person:" + strings.ToLower(name), PosterURL: imageURL,
	}
}

// virtualItem resolves synthetic catalog entries that clients can address as
// regular items: libraries, the root folder, genres and people.
func (a *API) virtualItem(id string) (*model.Item, bool) {
	id = normalizeID(id)
	if id == rootItemID {
		return rootViewItem(a.Store.Settings().ServerName), true
	}
	for _, v := range a.Catalog.Views() {
		if v.Library.ID == id {
			return viewItem(v.Library.ID, v.Library.Name), true
		}
	}
	for _, it := range a.Catalog.All() {
		for _, p := range it.People {
			if personID(p.Name) == id {
				return personItem(p.Name, p.ImageURL), true
			}
		}
		for _, g := range it.Genres {
			if genreID(g) == id {
				return genreItem(g), true
			}
		}
	}
	return nil, false
}

func (a *API) handleSystemConfiguration(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}
	s := a.Store.Settings()
	uiCulture := s.MetadataLanguage
	if uiCulture == "" {
		uiCulture = "en-US"
	}
	country := "US"
	switch {
	case strings.HasPrefix(uiCulture, "zh-TW"), strings.HasPrefix(uiCulture, "zh-HK"):
		country = "TW"
	case strings.HasPrefix(uiCulture, "zh"):
		country = "CN"
	case strings.HasPrefix(uiCulture, "ja"):
		country = "JP"
	case strings.HasPrefix(uiCulture, "ko"):
		country = "KR"
	}
	writeJSON(w, 200, map[string]any{
		"ServerName":                          s.ServerName,
		"UICulture":                           uiCulture,
		"MetadataCountryCode":                 country,
		"PreferredMetadataLanguage":           s.MetadataLanguage,
		"IsStartupWizardCompleted":            a.Store.SetupComplete(),
		"QuickConnectAvailable":               false,
		"EnableGroupingMoviesIntoCollections": false,
		"EnableGroupingShowsIntoCollections":  false,
		"EnableExternalContentInSuggestions":  false,
		"DisplaySpecialsWithinSeasons":        true,
		"EnableLegacyAuthorization":           false,
		"EnableCaseSensitiveItemIds":          true,
		"AllowClientLogUpload":                false,
		"EnableMetrics":                       false,
		"MaxResumePct":                        90,
		"MinResumePct":                        5,
		"MinResumeDurationSeconds":            300,
		"MaxAudiobookResume":                  5,
		"MinAudiobookResume":                  5,
		"RemoteClientBitrateLimit":            0,
		"LibraryScanFanoutConcurrency":        0,
		"LibraryMetadataRefreshConcurrency":   0,
		"LogFileRetentionDays":                3,
		"ActivityLogRetentionDays":            30,
		"CachePath":                           a.DataDir,
		"MetadataPath":                        "",
		"ContentTypes":                        []any{},
		"CodecsUsed":                          []any{},
		"MetadataOptions":                     []any{},
		"PathSubstitutions":                   []any{},
		"PluginRepositories":                  []any{},
		"CastReceiverApplications":            []any{},
		"SortRemoveCharacters":                []any{},
		"SortRemoveWords":                     []any{},
		"SortReplaceCharacters":               []any{},
	})
}

func (a *API) handleGenres(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, queryValues(r).Get("userId"))
	if !ok {
		return
	}
	v := queryValues(r)
	parent := normalizeID(v.Get("parentId"))
	types := splitList(v.Get("includeItemTypes"))
	search := strings.ToLower(strings.TrimSpace(v.Get("searchTerm")))

	type genre struct {
		name  string
		count int
	}
	seen := map[string]*genre{}
	for _, it := range a.Catalog.All() {
		if !canSeeItem(u, it) {
			continue
		}
		if parent != "" && it.LibraryID != parent && it.SeriesID != parent && it.SeasonID != parent {
			continue
		}
		if len(types) > 0 && !containsFold(types, it.Type) {
			continue
		}
		for _, raw := range it.Genres {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if search != "" && !strings.Contains(key, search) {
				continue
			}
			if seen[key] == nil {
				seen[key] = &genre{name: name}
			}
			seen[key].count++
		}
	}
	list := make([]*genre, 0, len(seen))
	for _, g := range seen {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].name) < strings.ToLower(list[j].name) })
	start := hls.ParseInt(v.Get("startIndex"), 0)
	limit := hls.ParseInt(v.Get("limit"), 0)
	total := len(list)
	if start > total {
		start = total
	}
	end := start + limit
	if limit <= 0 || end > total {
		end = total
	}
	items := make([]itemDTO, 0, end-start)
	for _, g := range list[start:end] {
		dto := a.buildItem(genreItem(g.name), u.ID, false, "")
		dto.LocationType = "Virtual"
		items = append(items, dto)
	}
	if b, ok := boolParam(r, "enableTotalRecordCount"); ok && !b {
		total = 0
	}
	writeJSON(w, 200, queryResult{Items: items, TotalRecordCount: total, StartIndex: start})
}

func (a *API) handlePersons(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, queryValues(r).Get("userId"))
	if !ok {
		return
	}
	v := queryValues(r)
	parent := normalizeID(v.Get("parentId"))
	appearsIn := normalizeID(v.Get("appearsInItemId"))
	personTypes := splitList(v.Get("personTypes"))
	search := strings.ToLower(strings.TrimSpace(v.Get("searchTerm")))

	type personEntry struct {
		name  string
		image string
	}
	seen := map[string]personEntry{}
	for _, it := range a.Catalog.All() {
		if !canSeeItem(u, it) {
			continue
		}
		if appearsIn != "" && it.ID != appearsIn {
			continue
		}
		if parent != "" && it.LibraryID != parent && it.SeriesID != parent && it.SeasonID != parent {
			continue
		}
		for _, p := range it.People {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				continue
			}
			typ := p.Type
			if typ == "" {
				typ = "Actor"
			}
			if len(personTypes) > 0 && !containsFold(personTypes, typ) {
				continue
			}
			key := strings.ToLower(name)
			if search != "" && !strings.Contains(key, search) {
				continue
			}
			if _, ok := seen[key]; !ok {
				seen[key] = personEntry{name: name, image: p.ImageURL}
			}
		}
	}
	list := make([]personEntry, 0, len(seen))
	for _, p := range seen {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].name) < strings.ToLower(list[j].name) })
	start := hls.ParseInt(v.Get("startIndex"), 0)
	limit := hls.ParseInt(v.Get("limit"), 0)
	total := len(list)
	if start > total {
		start = total
	}
	end := start + limit
	if limit <= 0 || end > total {
		end = total
	}
	items := make([]itemDTO, 0, end-start)
	for _, p := range list[start:end] {
		dto := a.buildItem(personItem(p.name, p.image), u.ID, false, "")
		dto.LocationType = "Virtual"
		items = append(items, dto)
	}
	writeJSON(w, 200, queryResult{Items: items, TotalRecordCount: total, StartIndex: start})
}

// Display preferences -----------------------------------------------------

func (a *API) handleDisplayPreferences(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, queryValues(r).Get("userId"))
	if !ok {
		return
	}
	client := strings.TrimSpace(queryValues(r).Get("client"))
	if client == "" {
		writeError(w, 400, "client is required")
		return
	}
	id := r.PathValue("id")
	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeError(w, 400, "Invalid request format")
			return
		}
		body["Id"] = id
		body["Client"] = client
		a.Store.SaveDisplayPreferences(u.ID, client, id, body)
		w.WriteHeader(204)
		return
	}
	prefs, ok := a.Store.DisplayPreferences(u.ID, client, id)
	if !ok {
		prefs = map[string]any{
			"SortBy":             "SortName",
			"SortOrder":          "Ascending",
			"IndexBy":            "None",
			"RememberIndexing":   false,
			"PrimaryImageHeight": 250,
			"PrimaryImageWidth":  250,
			"ScrollDirection":    "Horizontal",
			"ShowBackdrop":       true,
			"RememberSorting":    true,
			"ShowSidebar":        true,
			"ViewType":           "",
			"CustomPrefs":        map[string]string{},
		}
	}
	prefs["Id"] = id
	prefs["Client"] = client
	writeJSON(w, 200, prefs)
}
