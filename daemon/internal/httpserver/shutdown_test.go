package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// internalCallerCases are the callers every token-protected /api/internal
// endpoint must treat the same way. okStatus is what the endpoint answers when
// the caller is let in.
var internalCallerCases = []struct {
	name   string
	remote string
	token  string
	status func(okStatus int) int
}{
	{"remote host", "203.0.113.5:4000", "secret", func(int) int { return http.StatusNotFound }},
	{"missing token", "127.0.0.1:4000", "", func(int) int { return http.StatusForbidden }},
	{"wrong token", "127.0.0.1:4000", "nope", func(int) int { return http.StatusForbidden }},
	{"loopback with token", "127.0.0.1:4000", "secret", func(ok int) int { return ok }},
	{"ipv6 loopback with token", "[::1]:4000", "secret", func(ok int) int { return ok }},
}

func internalRequest(method string, path string, body string, remote string, token string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remote
	if token != "" {
		request.Header.Set(ShutdownTokenHeader, token)
	}
	return request
}

func shutdownRequest(remote string, token string) *http.Request {
	return internalRequest(http.MethodPost, "/api/internal/shutdown", "", remote, token)
}

func TestShutdownDaemonRequiresLoopbackAndToken(t *testing.T) {
	var calls atomic.Int32
	handler := apiHandler{shutdown: func() { calls.Add(1) }, shutdownToken: "secret"}

	wantCalls := 0
	for _, tc := range internalCallerCases {
		recorder := httptest.NewRecorder()
		handler.shutdownDaemon(recorder, shutdownRequest(tc.remote, tc.token))
		want := tc.status(http.StatusAccepted)
		if recorder.Code != want {
			t.Errorf("%s: status = %d, want %d", tc.name, recorder.Code, want)
		}
		if want == http.StatusAccepted {
			wantCalls++
		}
	}

	// The handler runs shutdown on a goroutine after a 100ms grace period, so
	// poll for the accepted calls instead of sleeping a fixed time...
	deadline := time.Now().Add(3 * time.Second)
	for int(calls.Load()) < wantCalls && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// ...then give a wrongly accepted request one more grace period to show up.
	time.Sleep(250 * time.Millisecond)
	if got := int(calls.Load()); got != wantCalls {
		t.Fatalf("shutdown ran %d times, want %d (once per accepted request)", got, wantCalls)
	}
}

func TestShutdownDaemonRejectsProxiedRequests(t *testing.T) {
	handler := apiHandler{shutdown: func() { t.Error("shutdown must not run") }, shutdownToken: "secret"}
	request := shutdownRequest("127.0.0.1:4000", "secret")
	request.Header.Set("X-Forwarded-For", "203.0.113.5")
	recorder := httptest.NewRecorder()
	handler.shutdownDaemon(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status for a proxied request = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestShutdownDaemonDisabledWithoutToken(t *testing.T) {
	handler := apiHandler{}
	recorder := httptest.NewRecorder()
	handler.shutdownDaemon(recorder, shutdownRequest("127.0.0.1:4000", "anything"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status when shutdown is not configured = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestInternalServerEndpointsNeedTheTokenAndALocalCaller(t *testing.T) {
	handler := apiHandler{shutdownToken: "secret"}
	endpoints := []struct {
		name    string
		method  string
		path    string
		body    string
		handler http.HandlerFunc
	}{
		{"running-servers", http.MethodGet, "/api/internal/running-servers", "", handler.internalRunningServers},
		{"resume-servers", http.MethodPost, "/api/internal/resume-servers", `{"serverIds":[]}`, handler.internalResumeServers},
	}
	for _, endpoint := range endpoints {
		for _, tc := range internalCallerCases {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				endpoint.handler(recorder, internalRequest(endpoint.method, endpoint.path, endpoint.body, tc.remote, tc.token))
				if want := tc.status(http.StatusOK); recorder.Code != want {
					t.Fatalf("status = %d, want %d", recorder.Code, want)
				}
			})
		}
	}
}
