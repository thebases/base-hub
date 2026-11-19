package routes

// File overview:
// - Responsibility: in-memory login rate limiting (SEC-03) for POST /oauth/token, covering both
//   the RFC 6749 form-encoded password grant and the JSON-body login variant (password_login.go)
//   — both share this one limiter since they're the same endpoint now.
// - Key symbols: `loginRateLimiter`, `oauthLoginLimiter`, `oauthGrantLimiter`.
// - Budgets: per (login, IP) lockout after 5 failures; per login across all IPs
//   after 20 (behind a proxy or NAT many clients share one IP, and an attacker
//   can rotate IPs); per-IP request window. The client IP is only the real one
//   when `server.trustedProxies` lists the proxy (internal/cmd/server.go).
//
// The limiter is in-memory on purpose: the current deployment is single-instance.
// A multi-instance deployment must move this state to a shared store before scaling out.

import (
	"strings"
	"sync"
	"time"
)

const (
	loginFailureThreshold = 5
	loginBackoffBase      = 30 * time.Second
	loginBackoffCap       = 15 * time.Minute
	loginFailureIdleReset = time.Hour
	loginIPWindow         = time.Minute
	loginIPWindowLimit    = 30
	// loginAccountFailureThreshold locks a login on every IP. Higher than the
	// pair threshold so one noisy client cannot lock the account quickly.
	loginAccountFailureThreshold = 20
	// grantIPWindowLimit caps client grants (client_credentials,
	// authorization_code, refresh_token) per IP. Higher than the password
	// budget: refreshes from many users behind one NAT go through it.
	grantIPWindowLimit = 120
)

type loginFailureState struct {
	count        int
	blockedUntil time.Time
	lastFailure  time.Time
}

type loginRateLimiter struct {
	mu            sync.Mutex
	now           func() time.Time
	ipWindowLimit int
	// accountThreshold arms the per-login lockout across all IPs; 0 = off.
	accountThreshold int
	failures         map[string]*loginFailureState // key: login|ip
	loginFailures    map[string]*loginFailureState // key: login, every IP
	ipHits           map[string][]time.Time
	lastPrune        time.Time
}

func newLoginRateLimiter() *loginRateLimiter {
	return newRateLimiter(loginIPWindowLimit, loginAccountFailureThreshold)
}

func newRateLimiter(ipWindowLimit, accountThreshold int) *loginRateLimiter {
	return &loginRateLimiter{
		now:              time.Now,
		ipWindowLimit:    ipWindowLimit,
		accountThreshold: accountThreshold,
		failures:         map[string]*loginFailureState{},
		loginFailures:    map[string]*loginFailureState{},
		ipHits:           map[string][]time.Time{},
	}
}

// oauthLoginLimiter is the process-wide limiter used by both password-login
// flows POST /oauth/token accepts (form-encoded grant_type=password and the
// JSON-body variant in password_login.go).
var oauthLoginLimiter = newLoginRateLimiter()

// oauthGrantLimiter throttles the non-password grants on POST /oauth/token.
// Failure keys are "client:<client_id>" (secret checks cost a bcrypt each);
// refresh_token uses only the IP window (the token itself is unguessable, and
// expired sessions behind one NAT must not lock each other out). No lockout
// across IPs: a client_id is public (authorize URLs), so it would let anyone
// lock a working integration out.
var oauthGrantLimiter = newRateLimiter(grantIPWindowLimit, 0)

func normalizeLoginKey(login string) string {
	return strings.ToLower(strings.TrimSpace(login))
}

func loginFailureKey(login, ip string) string {
	return normalizeLoginKey(login) + "|" + ip
}

func (l *loginRateLimiter) allow(login, ip string) (retryAfter time.Duration, allowed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// Sweep other IPs' stale entries here too; otherwise memory grows with every
	// unique IP until some failure happens to trigger recordFailure's prune.
	if now.Sub(l.lastPrune) >= loginIPWindow {
		l.pruneStaleLocked(now)
		l.lastPrune = now
	}

	hits := pruneLoginHitsBefore(l.ipHits[ip], now.Add(-loginIPWindow))
	if len(hits) >= l.ipWindowLimit {
		l.ipHits[ip] = hits
		return hits[0].Add(loginIPWindow).Sub(now), false
	}
	l.ipHits[ip] = append(hits, now)

	state := l.failures[loginFailureKey(login, ip)]
	if state != nil && now.Before(state.blockedUntil) {
		return state.blockedUntil.Sub(now), false
	}
	if key := normalizeLoginKey(login); key != "" {
		if state := l.loginFailures[key]; state != nil && now.Before(state.blockedUntil) {
			return state.blockedUntil.Sub(now), false
		}
	}
	return 0, true
}

func (l *loginRateLimiter) recordFailure(login, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneStaleLocked(now)

	bumpFailureLocked(l.failures, loginFailureKey(login, ip), loginFailureThreshold, now)
	if key := normalizeLoginKey(login); key != "" && l.accountThreshold > 0 {
		bumpFailureLocked(l.loginFailures, key, l.accountThreshold, now)
	}
}

// bumpFailureLocked counts one failure on states[key] and arms an exponential
// backoff once the count reaches threshold. Caller holds l.mu.
func bumpFailureLocked(states map[string]*loginFailureState, key string, threshold int, now time.Time) {
	state := states[key]
	if state == nil {
		state = &loginFailureState{}
		states[key] = state
	}
	state.count++
	state.lastFailure = now
	if state.count < threshold {
		return
	}
	exponent := state.count - threshold
	if exponent > 10 {
		exponent = 10
	}
	backoff := loginBackoffBase << exponent
	if backoff > loginBackoffCap {
		backoff = loginBackoffCap
	}
	state.blockedUntil = now.Add(backoff)
}

// recordSuccess clears the (login, IP) budget only. The per-login count is
// left to decay (loginFailureIdleReset): a success from one IP must not reset
// failures an attacker spread over other IPs.
func (l *loginRateLimiter) recordSuccess(login, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, loginFailureKey(login, ip))
}

func (l *loginRateLimiter) pruneStaleLocked(now time.Time) {
	for _, states := range []map[string]*loginFailureState{l.failures, l.loginFailures} {
		for key, state := range states {
			if now.After(state.blockedUntil) && now.Sub(state.lastFailure) > loginFailureIdleReset {
				delete(states, key)
			}
		}
	}
	cutoff := now.Add(-loginIPWindow)
	for ip, hits := range l.ipHits {
		pruned := pruneLoginHitsBefore(hits, cutoff)
		if len(pruned) == 0 {
			delete(l.ipHits, ip)
			continue
		}
		l.ipHits[ip] = pruned
	}
}

func pruneLoginHitsBefore(hits []time.Time, cutoff time.Time) []time.Time {
	idx := 0
	for idx < len(hits) && !hits[idx].After(cutoff) {
		idx++
	}
	return hits[idx:]
}
