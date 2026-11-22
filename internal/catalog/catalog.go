// Package catalog keeps the in-memory media index and answers browse queries.
package catalog

import (
	"cmp"
	"crypto/md5"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"nuvio/internal/model"
)

// View is a library decorated with its item count.
type View struct {
	Library   *model.Library
	ItemCount int
	AddedAt   time.Time
}

// Catalog is a concurrency-safe index over all scanned items.
type Catalog struct {
	mu         sync.RWMutex
	items      map[string]*model.Item
	children   map[string][]string
	lowerNames map[string]string
	genres     map[string]string
	persons    map[string]model.Person
	views      []*model.Library
	scanning   bool
	lastScan   time.Time
	lastError  string
	scanned    int
}

// New creates an empty catalog.
func New() *Catalog {
	return &Catalog{
		items:      map[string]*model.Item{},
		children:   map[string][]string{},
		lowerNames: map[string]string{},
		genres:     map[string]string{},
		persons:    map[string]model.Person{},
	}
}

// Replace swaps the whole catalog after a scan.
func (c *Catalog) Replace(views []*model.Library, items []*model.Item) {
	byID := make(map[string]*model.Item, len(items))
	children := map[string][]string{}
	// Precompute the case-folded sort name once per scan; Query then sorts
	// without allocating on every comparison.
	lowerNames := make(map[string]string, len(items))
	// People are not library items, but image requests address them by id.
	// Index them here so a detail page with two dozen cast members does not
	// rescan the whole catalog for every avatar request.
	persons := make(map[string]model.Person)
	// Genres are addressed by a hashed id too; index the id back to its name
	// so genre detail and artwork requests do not scan every item.
	genres := make(map[string]string)
	for _, it := range items {
		byID[it.ID] = it
		children[it.ParentID] = append(children[it.ParentID], it.ID)
		lowerNames[it.ID] = strings.ToLower(it.SortName)
		for _, g := range it.Genres {
			if name := strings.TrimSpace(g); name != "" {
				genres[GenreKey(name)] = name
			}
		}
		for _, p := range it.People {
			if strings.TrimSpace(p.Name) == "" {
				continue
			}
			key := PersonKey(p.Name)
			if old, ok := persons[key]; !ok || (old.ImageURL == "" && p.ImageURL != "") {
				persons[key] = p
			}
		}
	}
	for parent := range children {
		ids := children[parent]
		sort.SliceStable(ids, func(i, j int) bool {
			a, b := byID[ids[i]], byID[ids[j]]
			if a.Type == model.TypeEpisode && b.Type == model.TypeEpisode {
				if a.SeasonNumber != b.SeasonNumber {
					return a.SeasonNumber < b.SeasonNumber
				}
				if a.EpisodeNumber != b.EpisodeNumber {
					return a.EpisodeNumber < b.EpisodeNumber
				}
			}
			return lowerNames[ids[i]] < lowerNames[ids[j]]
		})
	}
	c.mu.Lock()
	c.items = byID
	c.children = children
	c.lowerNames = lowerNames
	c.genres = genres
	c.persons = persons
	c.views = views
	c.scanned = len(items)
	c.lastScan = time.Now()
	c.scanning = false
	c.lastError = ""
	c.mu.Unlock()
}

// PersonKey is the stable id exposed for a cast member. People have no
// library item of their own, so the id is derived from the name.
func PersonKey(name string) string {
	sum := md5.Sum([]byte("person:" + strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

// Person returns a cast member by the id used in Jellyfin DTOs.
func (c *Catalog) Person(id string) (model.Person, bool) {
	c.mu.RLock()
	p, ok := c.persons[id]
	c.mu.RUnlock()
	return p, ok
}

// GenreKey is the stable id exposed for a genre.
func GenreKey(name string) string {
	sum := md5.Sum([]byte("genre:" + strings.ToLower(strings.TrimSpace(name))))
	return hex.EncodeToString(sum[:])
}

// Genre returns the display name for a genre id.
func (c *Catalog) Genre(id string) (string, bool) {
	c.mu.RLock()
	name, ok := c.genres[id]
	c.mu.RUnlock()
	return name, ok
}

// SetLastScan restores the persisted scan timestamp after loading the catalog.
func (c *Catalog) SetLastScan(t time.Time) {
	if t.IsZero() {
		return
	}
	c.mu.Lock()
	c.lastScan = t
	c.mu.Unlock()
}

// SetViews updates the view list before items are scanned.
func (c *Catalog) SetViews(views []*model.Library) {
	c.mu.Lock()
	c.views = views
	c.mu.Unlock()
}

// Add merges a single new item (currently unused but handy for incremental scans).
func (c *Catalog) Add(it *model.Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[it.ID] = it
	c.children[it.ParentID] = append(c.children[it.ParentID], it.ID)
}

// SetScanning updates the scan status shown in the admin UI.
func (c *Catalog) SetScanning(v bool) {
	c.mu.Lock()
	c.scanning = v
	c.mu.Unlock()
}

// SetScanError records a failed scan.
func (c *Catalog) SetScanError(msg string) {
	c.mu.Lock()
	c.scanning = false
	c.lastError = msg
	c.mu.Unlock()
}

// Status describes the last scan.
type Status struct {
	Scanning  bool      `json:"scanning"`
	LastScan  time.Time `json:"lastScan"`
	LastError string    `json:"lastError,omitempty"`
	ItemCount int       `json:"itemCount"`
}

// Status returns a snapshot of scan state.
func (c *Catalog) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Status{Scanning: c.scanning, LastScan: c.lastScan, LastError: c.lastError, ItemCount: c.scanned}
}

// Get resolves an item id.
func (c *Catalog) Get(id string) (*model.Item, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	it, ok := c.items[id]
	return it, ok
}

// Views returns the configured libraries with item counts.
func (c *Catalog) Views() []View {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]View, 0, len(c.views))
	for _, lib := range c.views {
		out = append(out, View{Library: lib, ItemCount: len(c.children[lib.ID]), AddedAt: lib.AddedAt})
	}
	return out
}

// Children returns the direct children of an item or library.
func (c *Catalog) Children(parentID string) []*model.Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := c.children[parentID]
	out := make([]*model.Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := c.items[id]; ok {
			out = append(out, it)
		}
	}
	return out
}

// Query is a browse request. User-specific filtering happens in keep before
// pagination so page sizes stay correct.
type Query struct {
	ParentID     string
	Types        []string
	Recursive    bool
	Search       string
	SortBy       string
	Desc         bool
	Start        int
	Limit        int
	Genres       []string
	Years        []int
	ExcludeTypes []string
	IDs          []string
	IsFolder     *bool
}

// Query returns a page of items and the total count after filtering.
func (c *Catalog) Query(q Query, keep func(*model.Item) bool) ([]*model.Item, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	candidates := c.candidatesLocked(q)
	typeSet := map[string]bool{}
	for _, t := range q.Types {
		typeSet[t] = true
	}
	excludeSet := map[string]bool{}
	for _, t := range q.ExcludeTypes {
		excludeSet[t] = true
	}
	// Reuse the candidate backing array for filtering: at most one write per
	// read, so no second full-size slice is needed.
	matched := candidates[:0]
	term := strings.ToLower(q.Search)
	for _, it := range candidates {
		if len(typeSet) > 0 && !typeSet[it.Type] {
			continue
		}
		if excludeSet[it.Type] {
			continue
		}
		if q.IsFolder != nil {
			isFolder := it.Type == model.TypeSeries || it.Type == model.TypeSeason
			if isFolder != *q.IsFolder {
				continue
			}
		}
		if term != "" && !matchSearch(it, term) {
			continue
		}
		if len(q.Genres) > 0 {
			found := false
			for _, want := range q.Genres {
				for _, g := range it.Genres {
					if strings.EqualFold(g, want) {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				continue
			}
		}
		if len(q.Years) > 0 {
			found := false
			for _, year := range q.Years {
				if it.Year == year {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if keep != nil && !keep(it) {
			continue
		}
		matched = append(matched, it)
	}
	total := len(matched)
	start, end := PageBounds(q.Start, q.Limit, total)
	if start == end {
		return []*model.Item{}, total
	}
	return c.sortedPage(matched, q.SortBy, q.Desc, start, end), total
}

// topK returns the indices of the k best items under ord, in order. A binary
// heap over plain ints keeps the worst kept candidate at the root, so each
// remaining item costs O(log k) comparisons with no interface boxing.
func topK(n, k int, ord func(i, j int) int) []int {
	idx := make([]int, 0, k)
	for i := 0; i < n; i++ {
		if len(idx) < k {
			idx = append(idx, i)
			siftUp(idx, len(idx)-1, ord)
			continue
		}
		if ord(i, idx[0]) < 0 {
			idx[0] = i
			siftDown(idx, 0, ord)
		}
	}
	slices.SortFunc(idx, ord)
	return idx
}

// siftUp restores the "root is worst" invariant after appending an item.
func siftUp(idx []int, j int, ord func(i, j int) int) {
	for j > 0 {
		p := (j - 1) / 2
		if ord(idx[p], idx[j]) >= 0 {
			return
		}
		idx[p], idx[j] = idx[j], idx[p]
		j = p
	}
}

// siftDown restores the "root is worst" invariant after replacing the root.
func siftDown(idx []int, i int, ord func(i, j int) int) {
	n := len(idx)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		worst := left
		if right := left + 1; right < n && ord(idx[left], idx[right]) < 0 {
			worst = right
		}
		if ord(idx[i], idx[worst]) >= 0 {
			return
		}
		idx[i], idx[worst] = idx[worst], idx[i]
		i = worst
	}
}

// sortedPage returns items[start:end] in the requested order. A bounded page
// selects only the top `end` items with a heap (O(n log end)); the full sort is
// reserved for unbounded requests. Either way keys are resolved once and each
// comparison calls the key comparator once, not twice.
func (c *Catalog) sortedPage(items []*model.Item, sortBy string, desc bool, start, end int) []*model.Item {
	var byKey func(i, j int) int
	switch strings.ToLower(sortBy) {
	case "datecreated", "datelastcontentadded", "premieredate":
		byKey = func(i, j int) int { return items[i].AddedAt.Compare(items[j].AddedAt) }
	case "communityrating":
		byKey = func(i, j int) int { return cmp.Compare(items[i].Rating, items[j].Rating) }
	case "productionyear":
		byKey = func(i, j int) int { return cmp.Compare(items[i].Year, items[j].Year) }
	case "runtime":
		byKey = func(i, j int) int { return cmp.Compare(items[i].RuntimeTicks, items[j].RuntimeTicks) }
	case "random":
		byKey = func(i, j int) int { return strings.Compare(items[i].ID, items[j].ID) }
	default:
		keys := make([]string, len(items))
		for i, it := range items {
			if k, ok := c.lowerNames[it.ID]; ok {
				keys[i] = k
			} else {
				keys[i] = strings.ToLower(it.SortName)
			}
		}
		byKey = func(i, j int) int { return strings.Compare(keys[i], keys[j]) }
	}
	// Total order: key first, then ID so the heap and full-sort paths agree
	// even when many items share a sort key (e.g. "Season 1" or identical
	// episode titles). Without this, paging could skip or repeat tied items.
	ord := func(i, j int) int {
		if n := byKey(i, j); n != 0 {
			return n
		}
		return strings.Compare(items[i].ID, items[j].ID)
	}
	if desc {
		asc := ord
		ord = func(i, j int) int { return asc(j, i) }
	}

	var order []int
	if end < len(items) {
		order = topK(len(items), end, ord)
	} else {
		order = make([]int, len(items))
		for i := range order {
			order[i] = i
		}
		slices.SortFunc(order, ord)
	}
	out := make([]*model.Item, end-start)
	for i, idx := range order[start:end] {
		out[i] = items[idx]
	}
	return out
}

// PageBounds maps a client supplied start/limit pair onto [0,total]. Negative
// starts are treated as zero, a limit of zero or less means "to the end", and
// the arithmetic avoids start+limit overflowing for extreme query values.
func PageBounds(start, limit, total int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	if limit <= 0 || limit > total-start {
		return start, total
	}
	return start, start + limit
}

func (c *Catalog) candidatesLocked(q Query) []*model.Item {
	if len(q.IDs) > 0 {
		// Client supplied ids are the only source that can repeat, so dedup
		// here instead of charging every browse request for a map.
		out := make([]*model.Item, 0, len(q.IDs))
		seen := make(map[string]bool, len(q.IDs))
		for _, id := range q.IDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if it, ok := c.items[id]; ok {
				out = append(out, it)
			}
		}
		return out
	}
	if q.ParentID != "" {
		if _, ok := c.items[q.ParentID]; !ok {
			// A library view.
			if q.Recursive {
				out := []*model.Item{}
				for _, it := range c.items {
					if it.LibraryID == q.ParentID && it.Type != model.TypeSeason {
						out = append(out, it)
					}
				}
				return out
			}
			return mapItems(c.items, c.children[q.ParentID])
		}
		parent := c.items[q.ParentID]
		if q.Recursive && parent.Type == model.TypeSeries {
			out := []*model.Item{}
			for _, it := range c.items {
				if it.SeriesID == parent.ID && it.Type == model.TypeEpisode {
					out = append(out, it)
				}
			}
			return out
		}
		if q.Recursive && parent.Type == model.TypeSeason {
			return mapItems(c.items, c.children[parent.ID])
		}
		return mapItems(c.items, c.children[parent.ID])
	}
	// Global query (search).
	out := make([]*model.Item, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it)
	}
	return out
}

func mapItems(byID map[string]*model.Item, ids []string) []*model.Item {
	out := make([]*model.Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out
}

// matchSearch expects term to be lower cased by the caller so a browse query
// does not lowercase it once per item.
func matchSearch(it *model.Item, term string) bool {
	if strings.Contains(strings.ToLower(it.Name), term) {
		return true
	}
	if it.SeriesName != "" && strings.Contains(strings.ToLower(it.SeriesName), term) {
		return true
	}
	return false
}

// All returns every item (used by admin statistics).
func (c *Catalog) All() []*model.Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*model.Item, 0, len(c.items))
	for _, it := range c.items {
		out = append(out, it)
	}
	return out
}

// Counts summarises the catalog.
func (c *Catalog) Counts() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := map[string]int{}
	for _, it := range c.items {
		out[it.Type]++
	}
	out["Total"] = len(c.items)
	return out
}
