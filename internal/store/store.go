// Package store persists accounts, tokens, libraries, settings, scanned media
// items and per-user playback state in SQLite. nuvio.db is created on first run.
package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"nuvio/internal/model"
)

// Errors returned by the store.
var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
)

// Store is a concurrency-safe SQLite backed data store.
type Store struct {
	db       *sql.DB
	path     string
	serverID string
	created  time.Time

	settingsMu sync.RWMutex
	settings   model.ServerSettings
}

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	password_hash TEXT NOT NULL,
	salt TEXT NOT NULL,
	is_admin INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	allowed_libraries TEXT NOT NULL DEFAULT '',
	allow_transcode INTEGER NOT NULL DEFAULT 1,
	allow_remux INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS tokens (
	token TEXT PRIMARY KEY,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	device_id TEXT NOT NULL DEFAULT '',
	device_name TEXT NOT NULL DEFAULT '',
	client TEXT NOT NULL DEFAULT '',
	client_version TEXT NOT NULL DEFAULT '',
	remote_address TEXT NOT NULL DEFAULT '',
	last_seen TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS libraries (
	id TEXT PRIMARY KEY,
	data TEXT NOT NULL,
	added_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS items (
	id TEXT PRIMARY KEY,
	library_id TEXT NOT NULL,
	data TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_items_library ON items(library_id);
CREATE TABLE IF NOT EXISTS user_items (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	item_id TEXT NOT NULL,
	position_ticks INTEGER NOT NULL DEFAULT 0,
	played INTEGER NOT NULL DEFAULT 0,
	play_count INTEGER NOT NULL DEFAULT 0,
	last_played TEXT,
	favorite INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (user_id, item_id)
);
CREATE INDEX IF NOT EXISTS idx_user_items_resume ON user_items(user_id, played, position_ticks);
CREATE TABLE IF NOT EXISTS display_preferences (
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	client TEXT NOT NULL,
	key TEXT NOT NULL,
	data TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (user_id, client, key)
);
`

// Open opens (or creates) nuvio.db below dir.
func Open(dir, defaultServerName string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dbPath := filepath.Join(dir, "nuvio.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("create schema: %w", err)
		}
	}
	s := &Store{db: db, path: dbPath}
	s.serverID, _ = s.metaGet("server_id")
	if s.serverID == "" {
		s.serverID = randomHex(16)
		if err := s.metaSet("server_id", s.serverID); err != nil {
			db.Close()
			return nil, err
		}
	}
	created, _ := s.metaGet("created_at")
	if created == "" {
		s.created = time.Now().UTC()
		if err := s.metaSet("created_at", s.created.Format(time.RFC3339Nano)); err != nil {
			db.Close()
			return nil, err
		}
	} else {
		s.created, _ = time.Parse(time.RFC3339Nano, created)
	}

	rawSettings, ok := s.metaGet("settings")
	if !ok || rawSettings == "" {
		s.settings = model.DefaultServerSettings(defaultServerName)
		if err := s.saveSettings(); err != nil {
			db.Close()
			return nil, err
		}
	} else if err := json.Unmarshal([]byte(rawSettings), &s.settings); err != nil {
		db.Close()
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	if s.settings.ServerName == "" {
		s.settings.ServerName = defaultServerName
	}
	if s.settings.MetadataLanguage == "" {
		s.settings.MetadataLanguage = "zh-CN"
	}
	if s.settings.MaxBitrateMbps == 0 {
		s.settings.MaxBitrateMbps = 20
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) metaGet(key string) (string, bool) {
	var v string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	return v, err == nil
}

func (s *Store) metaSet(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) saveSettings() error {
	raw, err := json.Marshal(s.settings)
	if err != nil {
		return err
	}
	return s.metaSet("settings", string(raw))
}

// Path returns the SQLite database location.
func (s *Store) Path() string { return s.path }

// ServerID is a stable identifier exposed through SystemInfo.
func (s *Store) ServerID() string { return s.serverID }

// Secret is reserved for signed playback identifiers.
func (s *Store) Secret() []byte {
	v, _ := s.metaGet("secret")
	b, _ := base64.StdEncoding.DecodeString(v)
	return b
}

// StartedAt is when the database was created.
func (s *Store) StartedAt() time.Time { return s.created }

// Save flushes pending WAL pages (SQLite commits every statement already).
func (s *Store) Save() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

// SetupComplete reports whether at least one account exists.
func (s *Store) SetupComplete() bool {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// UserCount returns the number of accounts.
func (s *Store) UserCount() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n
}

// Users returns all accounts without password material.
func (s *Store) Users() []model.User {
	rows, err := s.db.Query("SELECT id, name, is_admin, created_at, password_hash <> '', allowed_libraries, allow_transcode, allow_remux FROM users ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		var u model.User
		var created, libs string
		var admin int
		if err := rows.Scan(&u.ID, &u.Name, &admin, &created, &u.HasPassword, &libs, &u.AllowTranscode, &u.AllowRemux); err != nil {
			continue
		}
		u.AllowedLibraries = decodeLibraries(libs)
		u.IsAdmin = admin == 1
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, u)
	}
	return out
}

// FindUser looks up an account by id.
func (s *Store) FindUser(id string) (*model.User, bool) {
	var u model.User
	var created, libs string
	var admin int
	err := s.db.QueryRow("SELECT id, name, is_admin, created_at, password_hash <> '', allowed_libraries, allow_transcode, allow_remux FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Name, &admin, &created, &u.HasPassword, &libs, &u.AllowTranscode, &u.AllowRemux)
	if err != nil {
		return nil, false
	}
	u.AllowedLibraries = decodeLibraries(libs)
	u.IsAdmin = admin == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, true
}

// FindUserByName looks up an account by (case-insensitive) name.
func (s *Store) FindUserByName(name string) (*model.User, bool) {
	var u model.User
	var created, libs string
	var admin int
	err := s.db.QueryRow("SELECT id, name, is_admin, created_at, password_hash <> '', allowed_libraries, allow_transcode, allow_remux FROM users WHERE name = ? COLLATE NOCASE", name).
		Scan(&u.ID, &u.Name, &admin, &created, &u.HasPassword, &libs, &u.AllowTranscode, &u.AllowRemux)
	if err != nil {
		return nil, false
	}
	u.AllowedLibraries = decodeLibraries(libs)
	u.IsAdmin = admin == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, true
}

// CreateUser adds an account, hashing the password with PBKDF2-SHA256.
func (s *Store) CreateUser(name, password string, admin bool) (*model.User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("Username cannot be empty")
	}
	if len(password) < 4 {
		return nil, errors.New("Password must be at least 4 characters")
	}
	salt, hash := hashPassword(password)
	u := &model.User{
		ID:             randomHex(16),
		Name:           name,
		PasswordHash:   hash,
		Salt:           salt,
		IsAdmin:        admin,
		AllowTranscode: true,
		AllowRemux:     true,
		CreatedAt:      time.Now().UTC(),
	}
	adminInt := 0
	if admin {
		adminInt = 1
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO users(id, name, password_hash, salt, is_admin, created_at, allowed_libraries, allow_transcode, allow_remux)
		VALUES(?, ?, ?, ?, ?, ?, '', 1, 1)`,
		u.ID, u.Name, u.PasswordHash, u.Salt, adminInt, u.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrUserExists
	}
	c := *u
	c.PasswordHash, c.Salt = "", ""
	c.HasPassword = true
	return &c, nil
}

// VerifyPassword checks a candidate password for userID.
func (s *Store) VerifyPassword(userID, password string) bool {
	var salt, hash string
	if err := s.db.QueryRow("SELECT salt, password_hash FROM users WHERE id = ?", userID).Scan(&salt, &hash); err != nil {
		return false
	}
	got := pbkdf2([]byte(password), mustB64(salt), 210000, 32)
	return subtle.ConstantTimeCompare(got, mustB64(hash)) == 1
}

// Authenticate returns the user matching name/password.
func (s *Store) Authenticate(name, password string) (*model.User, bool) {
	var u model.User
	var created, salt, hash, libs string
	var admin int
	err := s.db.QueryRow(`SELECT id, name, is_admin, created_at, salt, password_hash, allowed_libraries, allow_transcode, allow_remux
		FROM users WHERE name = ? COLLATE NOCASE`, name).
		Scan(&u.ID, &u.Name, &admin, &created, &salt, &hash, &libs, &u.AllowTranscode, &u.AllowRemux)
	if err != nil {
		return nil, false
	}
	got := pbkdf2([]byte(password), mustB64(salt), 210000, 32)
	if subtle.ConstantTimeCompare(got, mustB64(hash)) != 1 {
		return nil, false
	}
	u.IsAdmin = admin == 1
	u.HasPassword = hash != ""
	u.AllowedLibraries = decodeLibraries(libs)
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, true
}

// SessionMeta describes the client that opened a token. All fields are
// optional; missing values are simply not displayed in the device list.
type SessionMeta struct {
	DeviceID   string
	DeviceName string
	Client     string
	Version    string
	Address    string
}

// Session is a signed-in device belonging to an account.
type Session struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"deviceName"`
	Client     string    `json:"client"`
	Version    string    `json:"version"`
	Address    string    `json:"address"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeen   time.Time `json:"lastSeen"`
	Current    bool      `json:"current"`
}

// sessionID is an opaque, stable handle for a token. The token itself never
// leaves the server.
func sessionID(token string) string {
	sum := sha256.Sum256([]byte("session:" + token))
	return hex.EncodeToString(sum[:8])
}

// tokenTouchInterval throttles last-seen writes on the hot authentication path.
const tokenTouchInterval = 45 * time.Second

// IssueToken mints a persistent access token for a user.
func (s *Store) IssueToken(userID string, meta SessionMeta) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < 3; i++ {
		tok := randomHex(24)
		_, err := s.db.Exec(`INSERT INTO tokens(token, user_id, created_at, device_id, device_name, client, client_version, remote_address, last_seen)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tok, userID, now, meta.DeviceID, meta.DeviceName, meta.Client, meta.Version, meta.Address, now)
		if err == nil {
			return tok, nil
		}
	}
	return "", errors.New("Could not create an access token")
}

// UserByToken resolves an access token.
func (s *Store) UserByToken(token string) (*model.User, bool) {
	if token == "" {
		return nil, false
	}
	var u model.User
	var created, libs, seen string
	var admin int
	err := s.db.QueryRow(`SELECT u.id, u.name, u.is_admin, u.created_at, u.password_hash <> '', u.allowed_libraries, u.allow_transcode, u.allow_remux, t.last_seen
		FROM tokens t JOIN users u ON u.id = t.user_id WHERE t.token = ?`, token).
		Scan(&u.ID, &u.Name, &admin, &created, &u.HasPassword, &libs, &u.AllowTranscode, &u.AllowRemux, &seen)
	if err != nil {
		return nil, false
	}
	if lastSeen, err := time.Parse(time.RFC3339Nano, seen); err != nil || time.Since(lastSeen) > tokenTouchInterval {
		_, _ = s.db.Exec("UPDATE tokens SET last_seen = ? WHERE token = ?",
			time.Now().UTC().Format(time.RFC3339Nano), token)
	}
	u.AllowedLibraries = decodeLibraries(libs)
	u.IsAdmin = admin == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, true
}

// RevokeToken invalidates a token.
func (s *Store) RevokeToken(token string) {
	_, _ = s.db.Exec("DELETE FROM tokens WHERE token = ?", token)
}

// Sessions lists the devices signed in to an account. The token supplied with
// the current request is marked so the UI can keep it apart from the rest.
func (s *Store) Sessions(userID, currentToken string) []Session {
	rows, err := s.db.Query(`SELECT token, created_at, last_seen, device_name, client, client_version, remote_address
		FROM tokens WHERE user_id = ?`, userID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var token, created, seen string
		var sess Session
		if err := rows.Scan(&token, &created, &seen, &sess.DeviceName, &sess.Client, &sess.Version, &sess.Address); err != nil {
			continue
		}
		sess.ID = sessionID(token)
		sess.Current = currentToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(currentToken)) == 1
		sess.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		sess.LastSeen, _ = time.Parse(time.RFC3339Nano, seen)
		if sess.LastSeen.IsZero() {
			sess.LastSeen = sess.CreatedAt
		}
		out = append(out, sess)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

// RefreshSession replaces the descriptive metadata of a token with what the
// current request reports. Empty fields are ignored so a client that sends
// fewer headers never erases what we already know.
func (s *Store) RefreshSession(token string, meta SessionMeta) {
	if token == "" {
		return
	}
	_, _ = s.db.Exec(`UPDATE tokens SET
		device_id = COALESCE(NULLIF(?, ''), device_id),
		device_name = COALESCE(NULLIF(?, ''), device_name),
		client = COALESCE(NULLIF(?, ''), client),
		client_version = COALESCE(NULLIF(?, ''), client_version),
		remote_address = COALESCE(NULLIF(?, ''), remote_address)
		WHERE token = ?`,
		meta.DeviceID, meta.DeviceName, meta.Client, meta.Version, meta.Address, token)
}

// RevokeSession invalidates one of a user's tokens by its opaque session id.
func (s *Store) RevokeSession(userID, id string) bool {
	if id == "" {
		return false
	}
	rows, err := s.db.Query("SELECT token FROM tokens WHERE user_id = ?", userID)
	if err != nil {
		return false
	}
	target := ""
	for rows.Next() {
		var token string
		if rows.Scan(&token) != nil {
			continue
		}
		if sessionID(token) == id {
			target = token
			break
		}
	}
	rows.Close()
	if target == "" {
		return false
	}
	res, err := s.db.Exec("DELETE FROM tokens WHERE token = ? AND user_id = ?", target, userID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// DisplayPreferences returns the stored display preferences for a user,
// client and preference key.
func (s *Store) DisplayPreferences(userID, client, key string) (map[string]any, bool) {
	var raw string
	err := s.db.QueryRow(`SELECT data FROM display_preferences
		WHERE user_id = ? AND client = ? AND key = ?`, userID, client, key).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, false
	}
	return out, true
}

// SaveDisplayPreferences replaces the stored display preferences.
func (s *Store) SaveDisplayPreferences(userID, client, key string, data map[string]any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = s.db.Exec(`INSERT INTO display_preferences(user_id, client, key, data, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(user_id, client, key) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		userID, client, key, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
}

// Libraries returns the configured libraries.
func (s *Store) Libraries() []*model.Library {
	rows, err := s.db.Query("SELECT data FROM libraries ORDER BY added_at")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []*model.Library{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var lib model.Library
		if err := json.Unmarshal([]byte(raw), &lib); err != nil {
			continue
		}
		if lib.Paths == nil {
			lib.Paths = []string{}
		}
		out = append(out, &lib)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.Before(out[j].AddedAt) })
	return out
}

// AddLibrary registers a library.
func (s *Store) AddLibrary(name, kind string, paths []string, demo bool) (*model.Library, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("Library name cannot be empty")
	}
	switch kind {
	case model.LibraryMovies, model.LibraryShows, model.LibraryMixed:
	default:
		return nil, errors.New("Unknown library type")
	}
	if !demo && len(paths) == 0 {
		return nil, errors.New("At least one media folder is required")
	}
	l := &model.Library{
		ID:      randomHex(16),
		Name:    name,
		Type:    kind,
		Paths:   paths,
		Demo:    demo,
		AddedAt: time.Now().UTC(),
	}
	if err := s.insertLibrary(l); err != nil {
		return nil, err
	}
	return l, nil
}

func (s *Store) insertLibrary(l *model.Library) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT OR IGNORE INTO libraries(id, data, added_at) VALUES(?, ?, ?)",
		l.ID, string(raw), l.AddedAt.Format(time.RFC3339Nano))
	return err
}

// EnsureLibrary inserts a library with a fixed id if it is not present yet.
func (s *Store) EnsureLibrary(l *model.Library) bool {
	if l.AddedAt.IsZero() {
		l.AddedAt = time.Now().UTC()
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return false
	}
	res, err := s.db.Exec("INSERT OR IGNORE INTO libraries(id, data, added_at) VALUES(?, ?, ?)",
		l.ID, string(raw), l.AddedAt.Format(time.RFC3339Nano))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// SetPassword replaces a user's password.
func (s *Store) SetPassword(userID, password string) error {
	salt, hash := hashPassword(password)
	res, err := s.db.Exec("UPDATE users SET salt = ?, password_hash = ? WHERE id = ?", salt, hash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// UpdateUserPolicy replaces a user's library access and media permissions.
func (s *Store) UpdateUserPolicy(userID string, libraries []string, allowTranscode, allowRemux bool) error {
	res, err := s.db.Exec(`UPDATE users SET allowed_libraries = ?, allow_transcode = ?, allow_remux = ? WHERE id = ?`,
		encodeLibraries(libraries), boolInt(allowTranscode), boolInt(allowRemux), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetUserAdmin promotes or demotes an account.
func (s *Store) SetUserAdmin(userID string, admin bool) error {
	res, err := s.db.Exec("UPDATE users SET is_admin = ? WHERE id = ?", boolInt(admin), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes an account together with its tokens and playback state.
func (s *Store) DeleteUser(userID string) error {
	res, err := s.db.Exec("DELETE FROM users WHERE id = ?", userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// AdminCount reports how many administrators remain.
func (s *Store) AdminCount() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin = 1").Scan(&n)
	return n
}

func encodeLibraries(libs []string) string {
	clean := make([]string, 0, len(libs))
	for _, id := range libs {
		id = strings.TrimSpace(id)
		if id != "" {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeLibraries(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// RemoveLibrary deletes a library and its items.
func (s *Store) RemoveLibrary(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM items WHERE library_id = ?", id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec("DELETE FROM libraries WHERE id = ?", id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Settings returns the server settings.
func (s *Store) Settings() model.ServerSettings {
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.settings
}

// UpdateSettings replaces the server settings.
func (s *Store) UpdateSettings(fn func(*model.ServerSettings)) model.ServerSettings {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	fn(&s.settings)
	if s.settings.MetadataLanguage == "" {
		s.settings.MetadataLanguage = "zh-CN"
	}
	_ = s.saveSettings()
	return s.settings
}

// Items loads every persisted catalog item.
func (s *Store) Items() ([]*model.Item, error) {
	rows, err := s.db.Query("SELECT data FROM items")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Item{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var it model.Item
		if err := json.Unmarshal([]byte(raw), &it); err != nil {
			return nil, fmt.Errorf("parse item: %w", err)
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

// ReplaceItems atomically replaces the persisted catalog and records the scan
// time. User progress lives in user_items and is unaffected.
func (s *Store) ReplaceItems(items []*model.Item) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM items"); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare("INSERT OR REPLACE INTO items(id, library_id, data, updated_at) VALUES(?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339Nano)
	for _, it := range items {
		if it == nil || it.ID == "" {
			continue
		}
		raw, err := json.Marshal(it)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := stmt.Exec(it.ID, it.LibraryID, string(raw), nowText); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES('last_scan', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, nowText); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// LastScan returns the last successful catalog persistence time.
func (s *Store) LastScan() time.Time {
	v, ok := s.metaGet("last_scan")
	if !ok {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// ItemCount returns the persisted catalog size.
func (s *Store) ItemCount() int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM items").Scan(&n)
	return n
}

// UserData bundles the per-user state for an item.
type UserData struct {
	PositionTicks int64
	Played        bool
	PlayCount     int
	LastPlayed    time.Time
	Favorite      bool
}

// GetUserData returns playback state for one item.
func (s *Store) GetUserData(userID, itemID string) UserData {
	var ud UserData
	var played, playCount, favorite int
	var last sql.NullString
	err := s.db.QueryRow(`SELECT position_ticks, played, play_count, last_played, favorite
		FROM user_items WHERE user_id = ? AND item_id = ?`, userID, itemID).
		Scan(&ud.PositionTicks, &played, &playCount, &last, &favorite)
	if err != nil {
		return ud
	}
	ud.Played = played == 1
	ud.PlayCount = playCount
	ud.Favorite = favorite == 1
	if last.Valid {
		ud.LastPlayed, _ = time.Parse(time.RFC3339Nano, last.String)
	}
	return ud
}

// SetPlayed marks an item watched/unwatched.
func (s *Store) SetPlayed(userID, itemID string, played bool) {
	playedInt := 0
	playCount := 0
	if played {
		playedInt = 1
		playCount = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.Exec(`INSERT INTO user_items(user_id, item_id, position_ticks, played, play_count, last_played, favorite)
		VALUES(?, ?, 0, ?, ?, ?, 0)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			position_ticks = 0,
			played = excluded.played,
			play_count = user_items.play_count + CASE WHEN excluded.played = 1 AND user_items.played = 0 THEN 1 ELSE 0 END,
			last_played = CASE WHEN excluded.played = 1 THEN excluded.last_played ELSE user_items.last_played END`,
		userID, itemID, playedInt, playCount, now)
}

// SetProgress records playback position. played is set when the client reports
// the item as finished.
func (s *Store) SetProgress(userID, itemID string, positionTicks int64, played bool) {
	if positionTicks < 0 {
		positionTicks = 0
	}
	if played {
		positionTicks = 0
	}
	playedInt := 0
	initialCount := 0
	if played {
		playedInt = 1
		initialCount = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.Exec(`INSERT INTO user_items(user_id, item_id, position_ticks, played, play_count, last_played, favorite)
		VALUES(?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(user_id, item_id) DO UPDATE SET
			position_ticks = excluded.position_ticks,
			played = CASE
				WHEN excluded.played = 1 THEN 1
				WHEN excluded.position_ticks > 0 THEN 0
				ELSE user_items.played END,
			play_count = user_items.play_count + CASE WHEN excluded.played = 1 AND user_items.played = 0 THEN 1 ELSE 0 END,
			last_played = excluded.last_played`,
		userID, itemID, positionTicks, playedInt, initialCount, now)
}

// ToggleFavorite flips the favourite flag and returns the new value.
func (s *Store) ToggleFavorite(userID, itemID string) bool {
	v := !s.GetUserData(userID, itemID).Favorite
	s.SetFavorite(userID, itemID, v)
	return v
}

// SetFavorite sets the favourite flag.
func (s *Store) SetFavorite(userID, itemID string, fav bool) {
	f := 0
	if fav {
		f = 1
	}
	_, _ = s.db.Exec(`INSERT INTO user_items(user_id, item_id, position_ticks, played, play_count, last_played, favorite)
		VALUES(?, ?, 0, 0, 0, NULL, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET favorite = excluded.favorite`,
		userID, itemID, f)
}

// Resumable returns item ids watched by the user that still have progress.
func (s *Store) Resumable(userID string) map[string]model.Progress {
	out := map[string]model.Progress{}
	rows, err := s.db.Query(`SELECT item_id, position_ticks, played, play_count, last_played
		FROM user_items WHERE user_id = ? AND position_ticks > 0 AND played = 0`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var p model.Progress
		var played int
		var last sql.NullString
		if err := rows.Scan(&id, &p.PositionTicks, &played, &p.PlayCount, &last); err != nil {
			continue
		}
		p.Played = played == 1
		if last.Valid {
			p.LastPlayed, _ = time.Parse(time.RFC3339Nano, last.String)
		}
		out[id] = p
	}
	return out
}

// PlayedIDs returns every watched item id for a user.
func (s *Store) PlayedIDs(userID string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query("SELECT item_id FROM user_items WHERE user_id = ? AND played = 1", userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// FavoriteIDs returns favourited item ids.
func (s *Store) FavoriteIDs(userID string) map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query("SELECT item_id FROM user_items WHERE user_id = ? AND favorite = 1", userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

func hashPassword(password string) (saltB64, hashB64 string) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := pbkdf2([]byte(password), salt, 210000, 32)
	return base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(key)
}

func pbkdf2(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	buf := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func mustB64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
