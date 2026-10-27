package jellyfin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"nuvio/internal/store"
)

func TestAuthenticateResponseShape(t *testing.T) {
	st, err := store.Open(t.TempDir(), "Test")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateUser("alice", "secret", false); err != nil {
		t.Fatal(err)
	}
	a := &API{Store: st}
	req := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(`{"Username":"alice","Pw":"secret"}`))
	rec := httptest.NewRecorder()
	a.handleAuthenticate(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		User struct {
			Id     string         `json:"Id"`
			Policy map[string]any `json:"Policy"`
		} `json:"User"`
		SessionInfo map[string]any `json:"SessionInfo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.User.Id) != 32 {
		t.Fatalf("user id is not a simple UUID: %q", body.User.Id)
	}
	for _, key := range []string{
		"ForceRemoteSourceTranscoding", "EnableAllChannels", "RemoteClientBitrateLimit",
		"AuthenticationProviderId", "PasswordResetProviderId", "SyncPlayAccess",
	} {
		if _, ok := body.User.Policy[key]; !ok {
			t.Errorf("policy missing %s", key)
		}
	}
	for _, key := range []string{
		"PlayableMediaTypes", "UserId", "LastActivityDate", "LastPlaybackCheckIn",
		"IsActive", "SupportsMediaControl", "SupportsRemoteControl",
		"HasCustomDeviceName", "SupportedCommands", "PlayState", "Capabilities",
	} {
		if _, ok := body.SessionInfo[key]; !ok {
			t.Errorf("session info missing %s", key)
		}
	}
}
