package store

import (
	"testing"
	"time"

	"nuvio/internal/model"
)

func TestAuthTokensAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	if s.SetupComplete() {
		t.Fatal("fresh store should not be set up")
	}
	u, err := s.CreateUser("alice", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("ALICE", "other", false); err == nil {
		t.Fatal("duplicate user accepted")
	}
	if _, ok := s.Authenticate("alice", "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	got, ok := s.Authenticate("alice", "secret")
	if !ok || got.ID != u.ID {
		t.Fatal("valid login failed")
	}
	if !got.HasPassword {
		t.Fatal("authenticated user should report a password")
	}
	if found, ok := s.FindUser(u.ID); !ok || !found.HasPassword {
		t.Fatal("looked-up user should report a password")
	}
	tok, err := s.IssueToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.UserByToken(tok); !ok {
		t.Fatal("token did not resolve")
	}
	s.SetProgress(u.ID, "movie1", 12_000_000, false)
	s.SetFavorite(u.ID, "movie1", true)
	s.SetPlayed(u.ID, "movie2", true)
	s.UpdateSettings(func(st *model.ServerSettings) {
		st.MetadataLanguage = "en-US"
		st.TMDBAPIKey = "test-key"
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.SetupComplete() {
		t.Fatal("users not persisted")
	}
	if _, ok := reopened.UserByToken(tok); !ok {
		t.Fatal("token not persisted")
	}
	ud := reopened.GetUserData(u.ID, "movie1")
	if ud.PositionTicks != 12_000_000 || !ud.Favorite {
		t.Fatalf("user data not persisted: %+v", ud)
	}
	if !reopened.GetUserData(u.ID, "movie2").Played {
		t.Fatal("played state not persisted")
	}
	if st := reopened.Settings(); st.MetadataLanguage != "en-US" || st.TMDBAPIKey != "test-key" {
		t.Fatalf("settings not persisted: %+v", st)
	}

	reopened.RevokeToken(tok)
	if _, ok := reopened.UserByToken(tok); ok {
		t.Fatal("revoked token still valid")
	}
}

func TestProgressStateMachine(t *testing.T) {
	s, err := Open(t.TempDir(), "Test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser("bob", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	s.SetProgress(u.ID, "m", 5_000_000, false)
	if ud := s.GetUserData(u.ID, "m"); ud.PositionTicks != 5_000_000 || ud.Played {
		t.Fatalf("initial progress: %+v", ud)
	}
	s.SetProgress(u.ID, "m", 5_000_000, true)
	ud := s.GetUserData(u.ID, "m")
	if !ud.Played || ud.PositionTicks != 0 || ud.PlayCount != 1 {
		t.Fatalf("finished state: %+v", ud)
	}
	// Re-watching must make it resumable again without losing play count.
	s.SetProgress(u.ID, "m", 1_000_000, false)
	ud = s.GetUserData(u.ID, "m")
	if ud.Played || ud.PositionTicks != 1_000_000 || ud.PlayCount != 1 {
		t.Fatalf("rewatch state: %+v", ud)
	}
	if _, ok := s.Resumable(u.ID)["m"]; !ok {
		t.Fatal("resumable item missing")
	}
	s.SetPlayed(u.ID, "m", true)
	if ud := s.GetUserData(u.ID, "m"); !ud.Played || ud.PositionTicks != 0 || ud.PlayCount != 2 {
		t.Fatalf("mark played again: %+v", ud)
	}
}

func TestItemsLibrariesAndScanTime(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.AddLibrary("Movies", model.LibraryMovies, []string{"/media/movies"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if libs := s.Libraries(); len(libs) != 1 || libs[0].Name != "Movies" {
		t.Fatalf("libraries: %+v", libs)
	}
	items := []*model.Item{
		{ID: "m1", Type: model.TypeMovie, Name: "One", LibraryID: lib.ID, PosterURL: "https://image.tmdb.org/x.jpg"},
		{ID: "m2", Type: model.TypeMovie, Name: "Two", LibraryID: lib.ID},
	}
	if err := s.ReplaceItems(items); err != nil {
		t.Fatal(err)
	}
	if s.ItemCount() != 2 || s.LastScan().IsZero() {
		t.Fatalf("items=%d scan=%v", s.ItemCount(), s.LastScan())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.Items()
	if err != nil || len(loaded) != 2 {
		t.Fatalf("loaded %d items (%v)", len(loaded), err)
	}
	if reopened.LastScan().IsZero() {
		t.Fatal("last scan not persisted")
	}
	if err := reopened.RemoveLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	if reopened.ItemCount() != 0 || len(reopened.Libraries()) != 0 {
		t.Fatal("library removal did not clear items")
	}
}

func TestEnsureDemoLibraryKeepsFixedID(t *testing.T) {
	s, err := Open(t.TempDir(), "Test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lib := &model.Library{ID: "demo-movies", Name: "Demo", Type: model.LibraryMovies, AddedAt: time.Now(), Demo: true}
	if !s.EnsureLibrary(lib) {
		t.Fatal("demo library not inserted")
	}
	if s.EnsureLibrary(lib) {
		t.Fatal("duplicate demo library inserted")
	}
	if libs := s.Libraries(); len(libs) != 1 || libs[0].ID != "demo-movies" {
		t.Fatalf("libraries: %+v", libs)
	}
}

func TestUserPolicyAndDeletion(t *testing.T) {
	s, err := Open(t.TempDir(), "Nuvio")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	u, err := s.CreateUser("viewer", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if !u.AllowTranscode || !u.AllowRemux {
		t.Fatal("new users should allow transcode and remux by default")
	}
	if err := s.UpdateUserPolicy(u.ID, []string{"lib1", "lib2"}, false, true); err != nil {
		t.Fatal(err)
	}
	got, ok := s.FindUser(u.ID)
	if !ok {
		t.Fatal("user missing after policy update")
	}
	if len(got.AllowedLibraries) != 2 || got.AllowTranscode || !got.AllowRemux {
		t.Fatalf("policy not persisted: %+v", got)
	}
	tok, err := s.IssueToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	viaToken, ok := s.UserByToken(tok)
	if !ok || len(viaToken.AllowedLibraries) != 2 || viaToken.AllowTranscode || !viaToken.AllowRemux {
		t.Fatalf("token lookup lost the policy: %+v", viaToken)
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindUser(u.ID); ok {
		t.Fatal("deleted user still resolves")
	}
	if err := s.DeleteUser(u.ID); err != ErrUserNotFound {
		t.Fatalf("second delete = %v, want ErrUserNotFound", err)
	}
}

func TestAdminCount(t *testing.T) {
	s, err := Open(t.TempDir(), "Nuvio")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateUser("root", "secret", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("second", "secret", true); err != nil {
		t.Fatal(err)
	}
	if n := s.AdminCount(); n != 2 {
		t.Fatalf("AdminCount = %d, want 2", n)
	}
}

func TestRemuxSettingDefaultsForLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "Nuvio")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.metaSet("settings", `{"serverName":"Nuvio","transcodeEnabled":true}`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	reopened, err := Open(dir, "Nuvio")
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Settings().RemuxEnabled {
		t.Fatal("legacy settings should enable remuxing by default")
	}
	reopened.UpdateSettings(func(st *model.ServerSettings) { st.RemuxEnabled = false })
	reopened.Close()

	again, err := Open(dir, "Nuvio")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Settings().RemuxEnabled {
		t.Fatal("explicitly disabled remuxing was re-enabled on restart")
	}
}
