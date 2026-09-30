package httpserver

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// originAllowed reports whether a browser Origin header may talk to this
// daemon: it must match the Host the request was sent to, or be explicitly
// allowlisted via CLIFF_ALLOWED_ORIGINS. Requests without an Origin header
// (curl, CLI tools, same-origin GETs) are not browser cross-site requests.
func originAllowed(r *http.Request, allowed []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	for _, entry := range allowed {
		if strings.EqualFold(entry, origin) {
			return true
		}
	}
	return false
}

// requestIsSecure reports whether the client connection was HTTPS, either
// directly or through a TLS-terminating reverse proxy.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func withCommonHeaders(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			if !originAllowed(r, allowedOrigins) {
				// Browsers enforce CORS on reads; refuse everything else
				// (POST/PUT/DELETE/preflight) so cross-site pages cannot
				// trigger actions even where CORS would not block them.
				if !isSafeMethod(r.Method) || r.Method == http.MethodOptions {
					http.Error(w, "Cross-origin request blocked", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const (
	loginMaxFailures = 5
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
)

// loginLimiter throttles password guessing. Failures are tracked per client
// IP and per username so that neither rotating usernames nor rotating IPs
// gives unlimited attempts.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
	now     func() time.Time
}

type loginEntry struct {
	failures    int
	windowStart time.Time
	lockedUntil time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]*loginEntry{}, now: time.Now}
}

func loginKeys(r *http.Request, username string) []string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// Behind a local reverse proxy every request arrives from loopback, so
	// use the address the proxy appended to X-Forwarded-For instead.
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLoopback() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				ip = last
			}
		}
	}
	return []string{"ip:" + ip, "user:" + strings.ToLower(strings.TrimSpace(username))}
}

// blocked returns how long the caller must wait, or zero if allowed.
func (l *loginLimiter) blocked(keys []string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, key := range keys {
		if entry, ok := l.entries[key]; ok && entry.lockedUntil.After(now) {
			if remaining := entry.lockedUntil.Sub(now); remaining > wait {
				wait = remaining
			}
		}
	}
	return wait
}

func (l *loginLimiter) recordFailure(keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	for _, key := range keys {
		entry := l.entries[key]
		if entry == nil || now.Sub(entry.windowStart) > loginWindow {
			entry = &loginEntry{windowStart: now}
			l.entries[key] = entry
		}
		entry.failures++
		if entry.failures >= loginMaxFailures {
			entry.lockedUntil = now.Add(loginLockout)
		}
	}
}

// recordSuccess clears the username key only. The IP key is left alone so an
// attacker with one valid account cannot reset their guessing budget.
func (l *loginLimiter) recordSuccess(keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		if strings.HasPrefix(key, "user:") {
			delete(l.entries, key)
		}
	}
}

func (l *loginLimiter) sweep(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.windowStart) > loginWindow && !entry.lockedUntil.After(now) {
			delete(l.entries, key)
		}
	}
}
