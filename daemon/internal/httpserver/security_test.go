package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/config"
	"github.com/W1seGit/Cliff/daemon/internal/store"
)

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		origin  string
		allowed []string
		want    bool
	}{
		{"no origin", "cliff.lan:8080", "", nil, true},
		{"same origin", "cliff.lan:8080", "http://cliff.lan:8080", nil, true},
		{"same origin case", "Cliff.LAN:8080", "http://cliff.lan:8080", nil, true},
		{"other host", "cliff.lan:8080", "https://evil.example", nil, false},
		{"other port same host", "localhost:8080", "http://localhost:3000", nil, false},
		{"allowlisted", "localhost:8080", "http://localhost:3000", []string{"http://localhost:3000"}, true},
		{"malformed", "localhost:8080", "://bad", nil, false},
		{"null origin", "localhost:8080", "null", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/api/health", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := originAllowed(r, tc.allowed); got != tc.want {
				t.Fatalf("originAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCommonHeadersCORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := withCommonHeaders([]string{"http://localhost:3000"}, ok)

	do := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://cliff.lan:8080/api/servers", nil)
		r.Host = "cliff.lan:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if w := do("POST", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST status = %d, want 403", w.Code)
	}
	if w := do("OPTIONS", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site preflight status = %d, want 403", w.Code)
	}
	w := do("GET", "https://evil.example")
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("cross-site GET must not receive CORS grant: %v", w.Header())
	}
	w = do("POST", "http://cliff.lan:8080")
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "http://cliff.lan:8080" {
		t.Fatalf("same-origin POST = %d %v", w.Code, w.Header())
	}
	w = do("OPTIONS", "http://localhost:3000")
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("allowlisted preflight = %d %v", w.Code, w.Header())
	}
	if w := do("POST", ""); w.Code != http.StatusOK {
		t.Fatalf("non-browser POST status = %d, want 200", w.Code)
	}
}

func TestRequestIsSecure(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if requestIsSecure(r) {
		t.Fatal("plain HTTP must not be secure")
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	if !requestIsSecure(r) {
		t.Fatal("X-Forwarded-Proto https must be secure")
	}
	r.Header.Set("X-Forwarded-Proto", "http, https")
	if requestIsSecure(r) {
		t.Fatal("only the first forwarded proto counts")
	}
	tlsReq := httptest.NewRequest("GET", "https://example.com/", nil)
	if !requestIsSecure(tlsReq) {
		t.Fatal("direct TLS must be secure")
	}
}

func TestLoginLimiterLocksAndExpires(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter()
	limiter.now = func() time.Time { return now }
	keys := []string{"ip:1.2.3.4", "user:admin"}

	for i := 0; i < loginMaxFailures-1; i++ {
		limiter.recordFailure(keys)
		if limiter.blocked(keys) > 0 {
			t.Fatalf("blocked after %d failures", i+1)
		}
	}
	limiter.recordFailure(keys)
	if limiter.blocked(keys) <= 0 {
		t.Fatal("expected lockout after max failures")
	}
	if limiter.blocked([]string{"ip:9.9.9.9", "user:other"}) > 0 {
		t.Fatal("unrelated client must not be blocked")
	}
	if limiter.blocked([]string{"ip:9.9.9.9", "user:admin"}) <= 0 {
		t.Fatal("username lockout must apply across IPs")
	}
	now = now.Add(loginLockout + time.Second)
	if limiter.blocked(keys) > 0 {
		t.Fatal("lockout should expire")
	}
}

func TestLoginLimiterSuccessKeepsIPBudget(t *testing.T) {
	limiter := newLoginLimiter()
	keys := []string{"ip:1.2.3.4", "user:admin"}
	for i := 0; i < loginMaxFailures-1; i++ {
		limiter.recordFailure(keys)
	}
	limiter.recordSuccess(keys)
	limiter.recordFailure(keys)
	if limiter.blocked([]string{"ip:1.2.3.4", "user:someone-else"}) <= 0 {
		t.Fatal("a success must not reset the IP failure budget")
	}
}

func newAuthTestHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir+"/test.sqlite", dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.CreateUser(context.Background(), "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	return New(Options{Config: config.Config{DataDir: dir, ServerRoot: dir, WebDir: dir}, Store: db})
}

func postLogin(handler http.Handler, password string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	r.RemoteAddr = "203.0.113.5:4444"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestLoginRateLimitEndToEnd(t *testing.T) {
	handler := newAuthTestHandler(t)
	for i := 0; i < loginMaxFailures; i++ {
		if w := postLogin(handler, "wrong-password", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, w.Code)
		}
	}
	w := postLogin(handler, "correct-horse-battery", nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("locked-out login = %d retry-after=%q, want 429", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestLoginSessionCookieSecureBehindHTTPSProxy(t *testing.T) {
	handler := newAuthTestHandler(t)
	w := postLogin(handler, "correct-horse-battery", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", w.Code, w.Body.String())
	}
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Secure || !c[0].HttpOnly {
		t.Fatalf("plain HTTP cookie should be HttpOnly and not Secure: %+v", c)
	}
	w = postLogin(handler, "correct-horse-battery", map[string]string{"X-Forwarded-Proto": "https"})
	if c := w.Result().Cookies(); len(c) != 1 || !c[0].Secure {
		t.Fatalf("proxied HTTPS cookie must be Secure: %+v", c)
	}
}
