package jellyfin

import "testing"

func TestLoginLimiterBlocksKnownKey(t *testing.T) {
	a := &API{}
	key := "203.0.113.10|alice"
	for i := 0; i < loginMaxFailures; i++ {
		if _, ok := a.loginBlocked(key); !ok {
			t.Fatalf("key blocked too early at attempt %d", i)
		}
		a.loginFailed(key)
	}
	if _, ok := a.loginBlocked(key); ok {
		t.Fatal("key was not blocked after the failure limit")
	}
	if _, ok := a.loginBlocked("203.0.113.10|bob"); !ok {
		t.Fatal("a different account should not be blocked")
	}
	a.loginSucceeded(key)
	if _, ok := a.loginBlocked(key); !ok {
		t.Fatal("successful login did not clear the counter")
	}
}
