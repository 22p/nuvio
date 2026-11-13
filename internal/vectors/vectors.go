// Package vectors stores semantic-search embeddings in a separate SQLite
// database using the sqlite-vec extension.
package vectors

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

const schema = `
CREATE TABLE IF NOT EXISTS embedding_meta (
	item_id    TEXT PRIMARY KEY,
	model      TEXT NOT NULL,
	lang       TEXT NOT NULL,
	dims       INTEGER NOT NULL,
	text_hash  TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_embedding_meta_model ON embedding_meta(model);
`

// Entry describes a stored embedding without its vector.
type Entry struct {
	Model string
	Lang  string
	Hash  string
}

// Store owns the embeddings database.
type Store struct {
	db *sql.DB

	mu     sync.RWMutex
	model  string
	dims   int
	dbPath string
}

// Open opens or creates the embeddings database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create embeddings dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open embeddings db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure embeddings db: %w", err)
	}
	// The first implementation kept vectors in this table. Drop it so the
	// metadata is rebuilt against the vec0 tables.
	if _, err := db.Exec(`DROP TABLE IF EXISTS item_embeddings`); err != nil {
		db.Close()
		return nil, fmt.Errorf("drop legacy embeddings: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create embeddings schema: %w", err)
	}
	s := &Store{db: db, dbPath: path}
	if err := s.loadMeta(); err != nil {
		db.Close()
		return nil, err
	}
	if s.dims > 0 {
		if err := s.ensureVecTableLocked(s.dims); err != nil {
			db.Close()
			return nil, err
		}
		var metaCount, vecCount int
		_ = db.QueryRow(`SELECT count(*) FROM embedding_meta`).Scan(&metaCount)
		_ = db.QueryRow("SELECT count(*) FROM " + vecTable(s.dims)).Scan(&vecCount)
		if metaCount != vecCount {
			// A missing or partial vector table must be rebuilt, not treated
			// as an up-to-date index.
			_, _ = db.Exec(`DELETE FROM embedding_meta`)
			s.dims = 0
			s.model = ""
		}
	}
	return s, nil
}

func (s *Store) loadMeta() error {
	var model string
	var dims int
	err := s.db.QueryRow(`SELECT model, dims FROM embedding_meta LIMIT 1`).Scan(&model, &dims)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load embeddings: %w", err)
	}
	s.model = model
	s.dims = dims
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.dbPath }

// Count returns the number of stored embeddings.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM embedding_meta`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// Dims returns the vector dimension, or zero when the store is empty.
func (s *Store) Dims() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dims
}

// Model returns the model name of stored vectors, or an empty string.
func (s *Store) Model() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

// Get returns metadata for one stored embedding.
func (s *Store) Get(id string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var e Entry
	err := s.db.QueryRow(`SELECT model, lang, text_hash FROM embedding_meta WHERE item_id = ?`, id).
		Scan(&e.Model, &e.Lang, &e.Hash)
	if err != nil {
		return Entry{}, false
	}
	return e, true
}

// Put stores a normalized vector, replacing any previous value.
func (s *Store) Put(id, model, lang, hash string, vec []float32) error {
	if id == "" || len(vec) == 0 {
		return fmt.Errorf("empty embedding")
	}
	copyVec := append([]float32(nil), vec...)
	normalize(copyVec)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dims == 0 {
		s.dims = len(copyVec)
		s.model = model
	}
	if len(copyVec) != s.dims {
		return fmt.Errorf("embedding dimension %d does not match %d", len(copyVec), s.dims)
	}
	if err := s.ensureVecTableLocked(s.dims); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin embedding update: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO embedding_meta(item_id, model, lang, dims, text_hash, updated_at)
		VALUES(?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(item_id) DO UPDATE SET
			model = excluded.model,
			lang = excluded.lang,
			dims = excluded.dims,
			text_hash = excluded.text_hash,
			updated_at = excluded.updated_at`,
		id, model, lang, len(copyVec), hash); err != nil {
		tx.Rollback()
		return fmt.Errorf("save embedding metadata: %w", err)
	}
	// vec0 does not support INSERT OR REPLACE; delete first for idempotency.
	if _, err := tx.Exec("DELETE FROM "+vecTable(s.dims)+" WHERE item_id = ?", id); err != nil {
		tx.Rollback()
		return fmt.Errorf("replace embedding: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO "+vecTable(s.dims)+"(item_id, embedding) VALUES(?, ?)", id, encodeVector(copyVec)); err != nil {
		tx.Rollback()
		return fmt.Errorf("save embedding vector: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit embedding: %w", err)
	}
	s.model = model
	return nil
}

// Reset removes every embedding, used when the model changes.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.dropVecTablesLocked(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM embedding_meta`); err != nil {
		return fmt.Errorf("reset embeddings: %w", err)
	}
	s.dims = 0
	s.model = ""
	return nil
}

// Prune deletes embeddings whose item id is not in keep.
func (s *Store) Prune(keep map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT item_id FROM embedding_meta`)
	if err != nil {
		return fmt.Errorf("list embeddings: %w", err)
	}
	var remove []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			remove = append(remove, id)
		}
	}
	rows.Close()
	for _, id := range remove {
		if _, err := s.db.Exec(`DELETE FROM embedding_meta WHERE item_id = ?`, id); err != nil {
			return fmt.Errorf("prune embedding: %w", err)
		}
		if s.dims > 0 {
			if _, err := s.db.Exec("DELETE FROM "+vecTable(s.dims)+" WHERE item_id = ?", id); err != nil {
				return fmt.Errorf("prune embedding vector: %w", err)
			}
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM embedding_meta`).Scan(&n); err == nil && n == 0 {
		s.dims = 0
		s.model = ""
	}
	return nil
}

// Result is one vector-search hit.
type Result struct {
	ID    string
	Score float32
}

// Search returns the nearest vectors, skipping ids that allow rejects.
func (s *Store) Search(query []float32, limit int, allow func(string) bool) []Result {
	if limit <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.dims == 0 || len(query) != s.dims {
		return nil
	}
	k := limit * 20
	if k < 100 {
		k = 100
	}
	rows, err := s.db.Query(
		fmt.Sprintf("SELECT item_id, distance FROM %s WHERE embedding MATCH ? AND k = %d", vecTable(s.dims), k),
		encodeVector(query))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]Result, 0, limit)
	for rows.Next() {
		var id string
		var distance float64
		if err := rows.Scan(&id, &distance); err != nil {
			return out
		}
		if allow != nil && !allow(id) {
			continue
		}
		out = append(out, Result{ID: id, Score: float32(1 - distance)})
		if len(out) == limit {
			break
		}
	}
	return out
}

func (s *Store) ensureVecTableLocked(dims int) error {
	if dims <= 0 {
		return fmt.Errorf("invalid embedding dimension")
	}
	_, err := s.db.Exec(fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(item_id TEXT PRIMARY KEY, embedding FLOAT[%d] distance_metric=cosine)",
		vecTable(dims), dims))
	if err != nil {
		return fmt.Errorf("create vector table: %w", err)
	}
	return nil
}

func (s *Store) dropVecTablesLocked() error {
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'vec_items_%'`)
	if err != nil {
		return fmt.Errorf("list vector tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if strings.HasPrefix(name, "vec_items_") {
			names = append(names, name)
		}
	}
	rows.Close()
	for _, name := range names {
		if _, err := s.db.Exec("DROP TABLE " + name); err != nil {
			return fmt.Errorf("drop vector table: %w", err)
		}
	}
	return nil
}

func vecTable(dims int) string {
	return fmt.Sprintf("vec_items_%d", dims)
}

// Normalize scales v to unit length in place.
func Normalize(v []float32) { normalize(v) }

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}

func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
