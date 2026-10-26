// Package catalog keeps the in-memory media index and answers browse queries.
package catalog

import (
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
	mu        sync.RWMutex
	items     map[string]*model.Item
	children  map[string][]string
	views     []*model.Library
	scanning  bool
	lastScan  time.Time
	lastError string
	scanned   int
}

// New creates an empty catalog.
func New() *Catalog {
	return &Catalog{items: map[string]*model.Item{}, children: map[string][]string{}}
}

// Replace swaps the whole catalog after a scan.
func (c *Catalog) Replace(views []*model.Library, items []*model.Item) {
	byID := make(map[string]*model.Item, len(items))
	children := map[string][]string{}
	for _, it := range items {
		byID[it.ID] = it
		children[it.ParentID] = append(children[it.ParentID], it.ID)
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
			return strings.ToLower(a.SortName) < strings.ToLower(b.SortName)
		})
	}
	c.mu.Lock()
	c.items = byID
	c.children = children
	c.views = views
	c.scanned = len(items)
	c.lastScan = time.Now()
	c.scanning = false
	c.lastError = ""
	c.mu.Unlock()
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
	Genre        string
	Year         int
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
	matched := make([]*model.Item, 0, len(candidates))
	seen := map[string]bool{}
	for _, it := range candidates {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
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
		if q.Search != "" && !matchSearch(it, q.Search) {
			continue
		}
		if q.Genre != "" {
			found := false
			for _, g := range it.Genres {
				if strings.EqualFold(g, q.Genre) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if q.Year > 0 && it.Year != q.Year {
			continue
		}
		if keep != nil && !keep(it) {
			continue
		}
		matched = append(matched, it)
	}
	sortItems(matched, q.SortBy, q.Desc)
	total := len(matched)
	start, end := q.Start, q.Start+q.Limit
	if start > total {
		start = total
	}
	if q.Limit <= 0 {
		end = total
	}
	if end > total {
		end = total
	}
	if start < 0 {
		start = 0
	}
	return matched[start:end], total
}

func (c *Catalog) candidatesLocked(q Query) []*model.Item {
	if len(q.IDs) > 0 {
		out := make([]*model.Item, 0, len(q.IDs))
		for _, id := range q.IDs {
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

func matchSearch(it *model.Item, term string) bool {
	term = strings.ToLower(term)
	if strings.Contains(strings.ToLower(it.Name), term) {
		return true
	}
	if it.SeriesName != "" && strings.Contains(strings.ToLower(it.SeriesName), term) {
		return true
	}
	return false
}

func sortItems(items []*model.Item, sortBy string, desc bool) {
	less := func(i, j int) bool { return strings.ToLower(items[i].SortName) < strings.ToLower(items[j].SortName) }
	switch strings.ToLower(sortBy) {
	case "datecreated", "datelastcontentadded", "premieredate":
		less = func(i, j int) bool { return items[i].AddedAt.Before(items[j].AddedAt) }
	case "communityrating":
		less = func(i, j int) bool { return items[i].Rating < items[j].Rating }
	case "productionyear":
		less = func(i, j int) bool { return items[i].Year < items[j].Year }
	case "runtime":
		less = func(i, j int) bool { return items[i].RuntimeTicks < items[j].RuntimeTicks }
	case "random":
		less = func(i, j int) bool { return items[i].ID < items[j].ID }
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return less(j, i)
		}
		return less(i, j)
	})
}

// Latest returns recently added movies/episodes for a library.
func (c *Catalog) Latest(libID string, limit int) []*model.Item {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []*model.Item{}
	for _, it := range c.items {
		if it.LibraryID != libID {
			continue
		}
		if it.Type == model.TypeMovie || it.Type == model.TypeEpisode {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddedAt.Equal(out[j].AddedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].AddedAt.After(out[j].AddedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
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
