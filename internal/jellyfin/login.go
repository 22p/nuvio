package jellyfin

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Login throttling. PBKDF2 is deliberately expensive, so an unauthenticated
// caller could otherwise spend the server's CPU by repeating failed logins for
// a known account. A small fixed window also slows password guessing without
// turning a single bad client into a permanent account lockout.
const (
	loginMaxFailures = 8
	loginWindow      = 10 * time.Minute
	loginBlock       = 10 * time.Minute
	loginMaxEntries  = 4096
	loginConcurrency = 4
	loginQueueWait   = 2 * time.Second
)

type loginAttempt struct {
	failures     int
	reset        time.Time
	blockedUntil time.Time
}

func (a *API) loginKey(r *http.Request, username string) string {
	ip := ClientAddress(r)
	if len(ip) > 128 {
		ip = ip[:128]
	}
	name := strings.ToLower(strings.TrimSpace(username))
	if len(name) > 128 {
		name = name[:128]
	}
	return ip + "|" + name
}

// loginBlocked reports whether a key is currently throttled and, if so, how
// long the caller should wait.
func (a *API) loginBlocked(key string) (time.Duration, bool) {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	entry := a.loginAttempt[key]
	if entry == nil {
		return 0, true
	}
	if now.Before(entry.blockedUntil) {
		return time.Until(entry.blockedUntil), false
	}
	if now.After(entry.reset) {
		delete(a.loginAttempt, key)
	}
	return 0, true
}

func (a *API) loginFailed(key string) {
	now := time.Now()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.loginAttempt == nil {
		a.loginAttempt = make(map[string]*loginAttempt)
	}
	if len(a.loginAttempt) >= loginMaxEntries {
		a.pruneLoginsLocked(now)
		if len(a.loginAttempt) >= loginMaxEntries {
			// Keep memory bounded. The hash semaphore still protects the CPU
			// if an attacker keeps inventing keys.
			return
		}
	}
	entry := a.loginAttempt[key]
	if entry == nil {
		entry = &loginAttempt{reset: now.Add(loginWindow)}
		a.loginAttempt[key] = entry
	}
	if now.After(entry.reset) {
		entry.failures = 0
		entry.reset = now.Add(loginWindow)
	}
	entry.failures++
	if entry.failures >= loginMaxFailures {
		entry.blockedUntil = now.Add(loginBlock)
		entry.failures = 0
	}
}

func (a *API) loginSucceeded(key string) {
	a.loginMu.Lock()
	delete(a.loginAttempt, key)
	a.loginMu.Unlock()
}

func (a *API) pruneLoginsLocked(now time.Time) {
	for key, entry := range a.loginAttempt {
		if now.After(entry.reset) && now.After(entry.blockedUntil) {
			delete(a.loginAttempt, key)
		}
	}
}

// acquireLogin bounds concurrent password hashes so a flood of login requests
// cannot occupy every CPU core while still allowing normal clients through.
func (a *API) acquireLogin(ctx context.Context) bool {
	a.loginOnce.Do(func() {
		a.loginSem = make(chan struct{}, loginConcurrency)
	})
	select {
	case a.loginSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(loginQueueWait):
		return false
	}
}

func (a *API) releaseLogin() {
	<-a.loginSem
}
