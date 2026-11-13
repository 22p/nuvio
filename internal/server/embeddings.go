package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"nuvio/internal/ai"
	"nuvio/internal/model"
	"nuvio/internal/vectors"
)

// embeddingStatus is shown in the admin overview.
type embeddingStatus struct {
	Indexed int    `json:"indexed"`
	Total   int    `json:"total"`
	Running bool   `json:"running"`
	Model   string `json:"model,omitempty"`
	Format  string `json:"format,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) embeddingClient() *ai.Client {
	st := s.Store.Settings()
	if !st.AISearchEnabled || st.AIEmbeddingURL == "" || st.AIEmbeddingModel == "" {
		return nil
	}
	return ai.NewEmbedding(st.AIEmbeddingURL, st.AIEmbeddingKey, st.AIEmbeddingModel)
}

// EmbeddingStatus returns a snapshot of the semantic-search index.
func (s *Server) EmbeddingStatus() embeddingStatus {
	s.embedMu.Lock()
	defer s.embedMu.Unlock()
	out := s.embedStatus
	if s.Vectors != nil {
		out.Indexed = s.Vectors.Count()
		if out.Model == "" {
			out.Model = s.Vectors.Model()
		}
		if out.Format == "" {
			out.Format = s.Vectors.Format()
		}
	}
	return out
}

// StartEmbeddingIndex rebuilds missing or stale embeddings in the background.
// force re-embeds every item.
func (s *Server) StartEmbeddingIndex(force bool) {
	s.embedMu.Lock()
	if s.embedding {
		s.embedPending = true
		s.embedForce = s.embedForce || force
		s.embedMu.Unlock()
		return
	}
	// A running scan will trigger indexing when it finishes.
	if s.isScanning() {
		s.embedPending = true
		s.embedForce = s.embedForce || force
		s.embedMu.Unlock()
		return
	}
	force = force || s.embedForce
	s.embedding = true
	s.embedPending = false
	s.embedForce = false
	s.embedStatus.Running = true
	s.embedStatus.Error = ""
	s.embedMu.Unlock()
	go s.runEmbeddingIndex(force)
}

func (s *Server) runEmbeddingIndex(force bool) {
	client := s.embeddingClient()
	st := s.Store.Settings()
	modelName := strings.TrimSpace(st.AIEmbeddingModel)
	format := strings.ToLower(strings.TrimSpace(st.AIEmbeddingFormat))
	if format != "int8" {
		format = "float32"
	}
	lang := strings.TrimSpace(st.MetadataLanguage)
	if lang == "" {
		lang = "en-US"
	}
	items := s.searchableItems()

	s.embedMu.Lock()
	s.embedStatus.Total = len(items)
	s.embedStatus.Model = modelName
	s.embedStatus.Format = format
	s.embedStatus.Running = true
	s.embedMu.Unlock()

	defer func() {
		s.embedMu.Lock()
		if s.Vectors != nil {
			s.embedStatus.Indexed = s.Vectors.Count()
		}
		s.embedding = false
		s.embedStatus.Running = false
		pending, forceNext := s.embedPending, s.embedForce
		s.embedPending = false
		s.embedForce = false
		s.embedMu.Unlock()
		if pending {
			s.StartEmbeddingIndex(forceNext)
		}
	}()

	if client == nil || s.Vectors == nil {
		return
	}
	if s.Vectors.Count() > 0 && (s.Vectors.Model() != modelName || s.Vectors.Format() != format) {
		if err := s.Vectors.Reset(); err != nil {
			s.setEmbeddingError(err)
			return
		}
		force = true
	}

	keep := make(map[string]bool, len(items))
	const batchSize = 16
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		type pendingItem struct {
			it   *model.Item
			doc  string
			hash string
		}
		batch := make([]pendingItem, 0, end-start)
		for _, it := range items[start:end] {
			keep[it.ID] = true
			doc := vectors.Document(it)
			if doc == "" {
				continue
			}
			hash := vectors.Hash(doc)
			if !force {
				if entry, ok := s.Vectors.Get(it.ID); ok && entry.Model == modelName && entry.Lang == lang && entry.Hash == hash {
					continue
				}
			}
			batch = append(batch, pendingItem{it: it, doc: doc, hash: hash})
		}
		if len(batch) == 0 {
			continue
		}
		texts := make([]string, len(batch))
		for i := range batch {
			texts[i] = batch[i].doc
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		vecs, err := client.Embed(ctx, texts)
		cancel()
		if err != nil {
			s.setEmbeddingError(err)
			log.Printf("Embedding index failed: %v", err)
			return
		}
		for i, vec := range vecs {
			if err := s.Vectors.Put(batch[i].it.ID, modelName, format, lang, batch[i].hash, vec); err != nil {
				s.setEmbeddingError(err)
				log.Printf("Embedding index failed: %v", err)
				return
			}
		}
		s.embedMu.Lock()
		s.embedStatus.Indexed = s.Vectors.Count()
		s.embedMu.Unlock()
	}
	if err := s.Vectors.Prune(keep); err != nil {
		s.setEmbeddingError(err)
	}
}

func (s *Server) setEmbeddingError(err error) {
	s.embedMu.Lock()
	s.embedStatus.Error = err.Error()
	s.embedMu.Unlock()
}

func (s *Server) searchableItems() []*model.Item {
	all := s.Catalog.All()
	out := make([]*model.Item, 0, len(all))
	for _, it := range all {
		if it.Type == model.TypeMovie || it.Type == model.TypeSeries {
			out = append(out, it)
		}
	}
	return out
}

func (s *Server) handleReindexEmbeddings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	if s.embeddingClient() == nil || s.Vectors == nil {
		writeJSON(w, 400, map[string]any{"error": "AI search is not configured"})
		return
	}
	s.StartEmbeddingIndex(true)
	writeJSON(w, 202, s.EmbeddingStatus())
}
