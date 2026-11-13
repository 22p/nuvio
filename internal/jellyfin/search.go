package jellyfin

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"nuvio/internal/ai"
	"nuvio/internal/catalog"
	"nuvio/internal/model"
	"nuvio/internal/vectors"
)

type aiSearchResponse struct {
	Items            []itemDTO `json:"Items"`
	TotalRecordCount int       `json:"TotalRecordCount"`
	StartIndex       int       `json:"StartIndex"`
	Mode             string    `json:"Mode"`
}

// embeddingClient returns the configured semantic-search client, or nil when
// the feature is disabled or incomplete.
func (a *API) embeddingClient() *ai.Client {
	st := a.Store.Settings()
	if !st.AISearchEnabled || st.AIEmbeddingURL == "" || st.AIEmbeddingModel == "" {
		return nil
	}
	return ai.NewEmbedding(st.AIEmbeddingURL, st.AIEmbeddingKey, st.AIEmbeddingModel)
}

func (a *API) handleAISearch(w http.ResponseWriter, r *http.Request) {
	u, ok := a.resolveUser(w, r, requestUserID(r, ""))
	if !ok {
		return
	}
	var body struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, 400, "Invalid request format")
		return
	}
	query := strings.TrimSpace(body.Query)
	if query == "" {
		writeError(w, 400, "query is required")
		return
	}
	if len([]rune(query)) > 300 {
		query = string([]rune(query)[:300])
	}
	limit := body.Limit
	if limit <= 0 {
		limit = 24
	}
	if limit > 50 {
		limit = 50
	}

	keyword, _ := a.Catalog.Query(catalog.Query{
		Search:    query,
		Types:     []string{model.TypeMovie, model.TypeSeries, model.TypeEpisode},
		Recursive: true,
		Limit:     60,
	}, func(it *model.Item) bool { return canSeeItem(u, it) })

	scores := map[string]float64{}
	addRank := func(id string, rank int) {
		scores[id] += 1 / float64(60+rank+1)
	}
	for i, it := range keyword {
		addRank(it.ID, i)
	}

	mode := "keyword"
	client := a.embeddingClient()
	st := a.Store.Settings()
	if client != nil && a.Vectors != nil && a.Vectors.Count() > 0 && a.Vectors.Model() == st.AIEmbeddingModel {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		vecs, err := client.Embed(ctx, []string{query})
		cancel()
		if err == nil && len(vecs) == 1 {
			vectors.Normalize(vecs[0])
			hits := a.Vectors.Search(vecs[0], limit*5, func(id string) bool {
				it, ok := a.Catalog.Get(id)
				return ok && canSeeItem(u, it)
			})
			for i, hit := range hits {
				addRank(hit.ID, i)
			}
			mode = "hybrid"
		} else if err != nil {
			log.Printf("AI search embedding failed: %v", err)
		}
	}

	if len(scores) == 0 {
		writeJSON(w, 200, aiSearchResponse{Items: []itemDTO{}, Mode: mode})
		return
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return scores[ids[i]] > scores[ids[j]] })

	items := make([]itemDTO, 0, limit)
	for _, id := range ids {
		it, ok := a.Catalog.Get(id)
		if !ok || !canSeeItem(u, it) {
			continue
		}
		items = append(items, a.buildItem(it, u.ID, false, ""))
		if len(items) == limit {
			break
		}
	}
	writeJSON(w, 200, aiSearchResponse{Items: items, TotalRecordCount: len(items), Mode: mode})
}
