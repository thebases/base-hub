package routes

// File overview:
// - Responsibility: verifies the SEC-03 login rate-limit policy deterministically.
// - Key symbols: focused Test cases plus a fake-clock limiter fixture.
// - Pattern: Locks down lockout, backoff, reset, and IP-window behavior so the
//   auth transport keeps rejecting brute force the same way after refactors.

import (
	"fmt"
	"testing"
	"time"
)

// newTestLoginRateLimiter returns a limiter driven by a mutable fake clock so
// tests can cross backoff windows without sleeping.
func newTestLoginRateLimiter() (*loginRateLimiter, *time.Time) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	limiter := newLoginRateLimiter()
	limiter.now = func() time.Time { return now }
	return limiter, &now
}

func TestLoginRateLimiterLockoutAndExponentialBackoff(t *testing.T) {
	limiter, clock := newTestLoginRateLimiter()

	// Four failures stay under the threshold: attempts remain allowed.
	for i := 0; i < 4; i++ {
		if _, allowed := limiter.allow("admin", "10.0.0.1"); !allowed {
			t.Fatalf("attempt %d should be allowed before the threshold", i+1)
		}
		limiter.recordFailure("admin", "10.0.0.1")
	}

	// The fifth failure crosses the threshold and arms the base lockout.
	limiter.recordFailure("admin", "10.0.0.1")
	retryAfter, allowed := limiter.allow("admin", "10.0.0.1")
	if allowed {
		t.Fatalf("expected lockout after %d failures", loginFailureThreshold)
	}
	if retryAfter <= 0 || retryAfter > loginBackoffBase {
		t.Fatalf("retryAfter = %v, want (0, %v]", retryAfter, loginBackoffBase)
	}

	// Crossing the base window unlocks the pair again.
	*clock = clock.Add(loginBackoffBase + time.Second)
	if _, allowed := limiter.allow("admin", "10.0.0.1"); !allowed {
		t.Fatalf("expected attempt to be allowed after the base backoff expired")
	}

	// One more failure doubles the lockout window.
	limiter.recordFailure("admin", "10.0.0.1")
	*clock = clock.Add(loginBackoffBase + time.Second)
	if _, allowed := limiter.allow("admin", "10.0.0.1"); allowed {
		t.Fatalf("expected doubled backoff to still block after the base window")
	}
	*clock = clock.Add(loginBackoffBase + time.Second)
	if _, allowed := limiter.allow("admin", "10.0.0.1"); !allowed {
		t.Fatalf("expected attempt to be allowed after the doubled backoff expired")
	}
}

func TestLoginRateLimiterBackoffIsCapped(t *testing.T) {
	limiter, _ := newTestLoginRateLimiter()

	// A very long failure streak must clamp at the cap instead of overflowing.
	for i := 0; i < 200; i++ {
		limiter.recordFailure("admin", "10.0.0.1")
	}
	retryAfter, allowed := limiter.allow("admin", "10.0.0.1")
	if allowed {
		t.Fatalf("expected lockout after a long failure streak")
	}
	if retryAfter <= 0 || retryAfter > loginBackoffCap {
		t.Fatalf("retryAfter = %v, want (0, %v]", retryAfter, loginBackoffCap)
	}
}

func TestLoginRateLimiterSuccessResetsFailureBudget(t *testing.T) {
	limiter, _ := newTestLoginRateLimiter()

	for i := 0; i < loginFailureThreshold; i++ {
		limiter.recordFailure("admin", "10.0.0.1")
	}
	if _, allowed := limiter.allow("admin", "10.0.0.1"); allowed {
		t.Fatalf("expected lockout before the successful login")
	}

	// A verified login clears the pair immediately; the next attempt proceeds.
	limiter.recordSuccess("admin", "10.0.0.1")
	if _, allowed := limiter.allow("admin", "10.0.0.1"); !allowed {
		t.Fatalf("expected attempt to be allowed after a successful login reset")
	}

	// The budget restarts from zero: a single new failure must not re-lock.
	limiter.recordFailure("admin", "10.0.0.1")
	if _, allowed := limiter.allow("admin", "10.0.0.1"); !allowed {
		t.Fatalf("expected a single post-reset failure to stay under the threshold")
	}
}

func TestLoginRateLimiterKeysAreScopedPerLoginAndIP(t *testing.T) {
	limiter, _ := newTestLoginRateLimiter()

	for i := 0; i < loginFailureThreshold; i++ {
		limiter.recordFailure("admin", "10.0.0.1")
	}

	// The same login from another IP and another login from the same IP keep
	// their own budgets; only the failing pair is locked.
	if _, allowed := limiter.allow("admin", "10.0.0.2"); !allowed {
		t.Fatalf("expected a different IP to keep its own failure budget")
	}
	if _, allowed := limiter.allow("operator", "10.0.0.1"); !allowed {
		t.Fatalf("expected a different login to keep its own failure budget")
	}
	if _, allowed := limiter.allow("Admin ", "10.0.0.1"); allowed {
		t.Fatalf("expected login normalization to share one budget across casing")
	}
}

func TestLoginRateLimiterIPWindowThrottlesAcrossLogins(t *testing.T) {
	limiter, clock := newTestLoginRateLimiter()

	// Credential stuffing cycles login names; the per-IP window must still cap
	// raw volume from one source.
	for i := 0; i < loginIPWindowLimit; i++ {
		login := "user" + string(rune('a'+i%26))
		if _, allowed := limiter.allow(login, "10.0.0.9"); !allowed {
			t.Fatalf("attempt %d should fit inside the IP window", i+1)
		}
	}
	retryAfter, allowed := limiter.allow("another", "10.0.0.9")
	if allowed {
		t.Fatalf("expected the IP window to throttle attempt %d", loginIPWindowLimit+1)
	}
	if retryAfter <= 0 || retryAfter > loginIPWindow {
		t.Fatalf("retryAfter = %v, want (0, %v]", retryAfter, loginIPWindow)
	}

	// Once the window slides past the burst, attempts flow again.
	*clock = clock.Add(loginIPWindow + time.Second)
	if _, allowed := limiter.allow("another", "10.0.0.9"); !allowed {
		t.Fatalf("expected attempts to resume after the IP window slid past the burst")
	}
}

func TestLoginRateLimiterAllowPrunesIdleIPs(t *testing.T) {
	l := newLoginRateLimiter()
	clock := time.Now()
	l.now = func() time.Time { return clock }
	for i := 0; i < 50; i++ {
		l.allow("u", fmt.Sprintf("10.0.0.%d", i))
	}
	clock = clock.Add(2 * loginIPWindow)
	l.allow("u", "10.9.9.9")
	if len(l.ipHits) != 1 {
		t.Fatalf("expected stale IPs pruned on allow, got %d entries", len(l.ipHits))
	}
}

func TestLoginRateLimiterLocksLoginAcrossIPs(t *testing.T) {
	limiter, _ := newTestLoginRateLimiter()

	// Failures spread over many IPs (or many clients behind one rotated
	// egress) never trip a single pair, but they must lock the login.
	for i := 0; i < loginAccountFailureThreshold; i++ {
		ip := fmt.Sprintf("10.1.%d.%d", i/250, i%250)
		if _, allowed := limiter.allow("admin", ip); !allowed {
			t.Fatalf("attempt %d should be allowed before the account threshold", i+1)
		}
		limiter.recordFailure("admin", ip)
	}
	if _, allowed := limiter.allow("Admin", "10.9.9.9"); allowed {
		t.Fatalf("expected the login to be locked on a fresh IP after %d failures", loginAccountFailureThreshold)
	}
	if _, allowed := limiter.allow("operator", "10.9.9.9"); !allowed {
		t.Fatalf("another login must keep its own budget")
	}

	// A success from one IP clears only that pair, not the account count.
	limiter.recordSuccess("admin", "10.1.0.0")
	if _, allowed := limiter.allow("admin", "10.9.9.9"); allowed {
		t.Fatalf("a single success must not reset failures spread over other IPs")
	}
}

func TestOAuthGrantLimiterIPWindowAndClientLockout(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(grantIPWindowLimit, 0)
	limiter.now = func() time.Time { return now }

	// Refresh calls (key "") only spend the IP window and never lock.
	for i := 0; i < grantIPWindowLimit; i++ {
		if _, allowed := limiter.allow("", "10.0.0.1"); !allowed {
			t.Fatalf("refresh %d should fit the grant IP window", i+1)
		}
	}
	if _, allowed := limiter.allow("", "10.0.0.1"); allowed {
		t.Fatalf("expected the grant IP window to throttle call %d", grantIPWindowLimit+1)
	}

	// Bad client secrets lock that client on that IP after the pair threshold.
	key := grantClientKey("7")
	for i := 0; i < loginFailureThreshold; i++ {
		limiter.recordFailure(key, "10.0.0.2")
	}
	if _, allowed := limiter.allow(key, "10.0.0.2"); allowed {
		t.Fatalf("expected client lockout after %d bad secrets", loginFailureThreshold)
	}
	if _, allowed := limiter.allow(grantClientKey("8"), "10.0.0.2"); !allowed {
		t.Fatalf("another client must keep its own budget")
	}

	// No lockout across IPs: bad secrets from many IPs never lock the client
	// for a caller on a clean IP.
	for i := 0; i < 3*loginAccountFailureThreshold; i++ {
		limiter.recordFailure(key, fmt.Sprintf("10.2.0.%d", i))
	}
	if _, allowed := limiter.allow(key, "10.3.0.1"); !allowed {
		t.Fatalf("grant limiter must not lock a client across IPs")
	}
}
