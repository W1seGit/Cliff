package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/config"
	"github.com/W1seGit/Cliff/daemon/internal/store"
	"github.com/W1seGit/Cliff/daemon/internal/totp"
)

type accessFixture struct {
	handler http.Handler
	db      *store.Store
	member  store.User
}

func newAccessFixture(t *testing.T) accessFixture {
	t.Helper()
	dir := t.TempDir()
	db := openTestStoreAt(t, dir, dir)
	ctx := context.Background()
	if _, err := db.CreateUser(ctx, "owner", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateAccount(ctx, "friend", "another-long-password", store.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"srv_a", "srv_b"} {
		if _, err := db.CreateServer(ctx, store.Server{ID: id, Name: id, Path: t.TempDir(), MinMemoryMB: 1024, MaxMemoryMB: 2048, Port: 25565}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetPermissions(ctx, member.ID, map[string][]string{"srv_a": {store.PermConsole}}); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Config: config.Config{DataDir: dir, ServerRoot: dir, WebDir: dir}, Store: db})
	return accessFixture{handler: handler, db: db, member: member}
}

func (f accessFixture) do(method string, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "203.0.113.9:5000"
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f accessFixture) login(t *testing.T, username string, password string) *http.Cookie {
	t.Helper()
	response := f.do("POST", "/api/auth/login", nil, `{"username":"`+username+`","password":"`+password+`"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("login as %s = %d: %s", username, response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	return cookies[0]
}

func TestMemberOnlySeesAndDoesWhatWasGranted(t *testing.T) {
	f := newAccessFixture(t)
	admin := f.login(t, "owner", "correct-horse-battery")
	member := f.login(t, "friend", "another-long-password")

	cases := []struct {
		name   string
		method string
		path   string
		cookie *http.Cookie
		want   int
	}{
		{"anonymous is refused", "GET", "/api/servers", nil, http.StatusUnauthorized},
		{"member sees a granted server", "GET", "/api/servers/srv_a", member, http.StatusOK},
		{"member cannot see another server", "GET", "/api/servers/srv_b", member, http.StatusForbidden},
		{"member lacks files on a granted server", "GET", "/api/servers/srv_a/files", member, http.StatusForbidden},
		{"member cannot start", "POST", "/api/servers/srv_a/start", member, http.StatusForbidden},
		{"member cannot manage users", "GET", "/api/users", member, http.StatusForbidden},
		{"member cannot change settings", "PUT", "/api/settings", member, http.StatusForbidden},
		{"member cannot read webhooks", "GET", "/api/webhooks", member, http.StatusForbidden},
		{"member cannot create servers", "POST", "/api/servers", member, http.StatusForbidden},
		{"member cannot delete servers", "DELETE", "/api/servers/srv_a", member, http.StatusForbidden},
		{"member may read command presets", "GET", "/api/servers/srv_a/command", member, http.StatusOK},
		{"admin reads any server", "GET", "/api/servers/srv_b", admin, http.StatusOK},
		{"admin lists users", "GET", "/api/users", admin, http.StatusOK},
	}
	for _, tc := range cases {
		if got := f.do(tc.method, tc.path, tc.cookie, "").Code; got != tc.want {
			t.Errorf("%s: %s %s = %d, want %d", tc.name, tc.method, tc.path, got, tc.want)
		}
	}

	var listed []store.Server
	if err := json.Unmarshal(f.do("GET", "/api/servers", member, "").Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "srv_a" {
		t.Fatalf("a member must only be listed their own servers, got %+v", listed)
	}

	settings := f.do("GET", "/api/settings", member, "")
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"serverRoot":""`) {
		t.Fatalf("members get a trimmed settings reply: %d %s", settings.Code, settings.Body.String())
	}
}

func TestChangingPermissionsTakesEffectImmediately(t *testing.T) {
	f := newAccessFixture(t)
	member := f.login(t, "friend", "another-long-password")
	if got := f.do("GET", "/api/servers/srv_b", member, "").Code; got != http.StatusForbidden {
		t.Fatalf("before the grant: %d", got)
	}
	if err := f.db.SetPermissions(context.Background(), f.member.ID, map[string][]string{"srv_b": {store.PermFiles}}); err != nil {
		t.Fatal(err)
	}
	if got := f.do("GET", "/api/servers/srv_b", member, "").Code; got != http.StatusOK {
		t.Fatalf("after the grant the same session should work: %d", got)
	}
	if err := f.db.DeleteAccount(context.Background(), f.member.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.do("GET", "/api/servers/srv_b", member, "").Code; got != http.StatusUnauthorized {
		t.Fatalf("a deleted account's session must stop working: %d", got)
	}
}

func TestTwoFactorLogin(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	owner := f.login(t, "owner", "correct-horse-battery")

	// Enrol through the API: password required, then a real code.
	if got := f.do("POST", "/api/auth/2fa/setup", owner, `{"password":"wrong"}`).Code; got != http.StatusBadRequest {
		t.Fatalf("setup with a wrong password = %d", got)
	}
	var setup struct{ Secret string }
	response := f.do("POST", "/api/auth/2fa/setup", owner, `{"password":"correct-horse-battery"}`)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &setup) != nil || setup.Secret == "" {
		t.Fatalf("setup failed: %d %s", response.Code, response.Body.String())
	}
	if got := f.do("POST", "/api/auth/2fa/enable", owner, `{"code":"000000"}`).Code; got != http.StatusBadRequest {
		t.Fatalf("enabling with a bad code = %d", got)
	}
	code, _ := totp.Code(setup.Secret, totp.Step(time.Now()))
	response = f.do("POST", "/api/auth/2fa/enable", owner, `{"code":"`+code+`"}`)
	var enabled struct{ RecoveryCodes []string }
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &enabled) != nil || len(enabled.RecoveryCodes) != 8 {
		t.Fatalf("enable failed: %d %s", response.Code, response.Body.String())
	}

	loginBody := func(code string) string {
		return `{"username":"owner","password":"correct-horse-battery","code":"` + code + `"}`
	}
	if got := f.do("POST", "/api/auth/login", nil, loginBody("")); got.Code != http.StatusUnauthorized || !strings.Contains(got.Body.String(), `"totpRequired":true`) {
		t.Fatalf("login without a code = %d %s", got.Code, got.Body.String())
	}
	if got := f.do("POST", "/api/auth/login", nil, loginBody("123456")).Code; got != http.StatusUnauthorized {
		t.Fatalf("login with a wrong code = %d", got)
	}

	// The code used to enable two-factor cannot be replayed to sign in. Use
	// a code from the next step, which the verifier allows as clock skew.
	next, _ := totp.Code(setup.Secret, totp.Step(time.Now())+1)
	if got := f.do("POST", "/api/auth/login", nil, loginBody(next)).Code; got != http.StatusOK {
		t.Fatalf("login with a fresh code = %d", got)
	}
	if got := f.do("POST", "/api/auth/login", nil, loginBody(next)).Code; got != http.StatusUnauthorized {
		t.Fatalf("a code must work only once, second use = %d", got)
	}

	recovery := enabled.RecoveryCodes[0]
	if got := f.do("POST", "/api/auth/login", nil, loginBody(recovery)).Code; got != http.StatusOK {
		t.Fatalf("login with a recovery code = %d", got)
	}
	if got := f.do("POST", "/api/auth/login", nil, loginBody(recovery)).Code; got != http.StatusUnauthorized {
		t.Fatalf("a recovery code must work only once, second use = %d", got)
	}

	// An admin can switch two-factor off for someone locked out.
	owners, _ := f.db.ListAccounts(ctx)
	if len(owners) == 0 {
		t.Fatal("no accounts")
	}
	if err := f.db.DisableTOTP(ctx, owners[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := f.do("POST", "/api/auth/login", nil, loginBody("")).Code; got != http.StatusOK {
		t.Fatalf("login after two-factor was reset = %d", got)
	}
}
