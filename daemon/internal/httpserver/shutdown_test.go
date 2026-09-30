package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shutdownRequest(remote string, token string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/internal/shutdown", nil)
	request.RemoteAddr = remote
	if token != "" {
		request.Header.Set(ShutdownTokenHeader, token)
	}
	return request
}

func TestShutdownDaemonRequiresLoopbackAndToken(t *testing.T) {
	called := make(chan struct{}, 1)
	handler := apiHandler{shutdown: func() { called <- struct{}{} }, shutdownToken: "secret"}

	cases := []struct {
		name    string
		remote  string
		token   string
		status  int
		expects bool
	}{
		{"remote host", "203.0.113.5:4000", "secret", http.StatusNotFound, false},
		{"missing token", "127.0.0.1:4000", "", http.StatusForbidden, false},
		{"wrong token", "127.0.0.1:4000", "nope", http.StatusForbidden, false},
		{"loopback with token", "127.0.0.1:4000", "secret", http.StatusAccepted, true},
		{"ipv6 loopback with token", "[::1]:4000", "secret", http.StatusAccepted, true},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		handler.shutdownDaemon(recorder, shutdownRequest(tc.remote, tc.token))
		if recorder.Code != tc.status {
			t.Fatalf("%s: expected %d, got %d", tc.name, tc.status, recorder.Code)
		}
		select {
		case <-called:
			if !tc.expects {
				t.Fatalf("%s: shutdown must not run", tc.name)
			}
		case <-time.After(400 * time.Millisecond):
			if tc.expects {
				t.Fatalf("%s: shutdown did not run", tc.name)
			}
		}
	}
}

func TestShutdownDaemonRejectsProxiedRequests(t *testing.T) {
	handler := apiHandler{shutdown: func() { t.Error("shutdown must not run") }, shutdownToken: "secret"}
	request := shutdownRequest("127.0.0.1:4000", "secret")
	request.Header.Set("X-Forwarded-For", "203.0.113.5")
	recorder := httptest.NewRecorder()
	handler.shutdownDaemon(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a proxied request, got %d", recorder.Code)
	}
}

func TestShutdownDaemonDisabledWithoutToken(t *testing.T) {
	handler := apiHandler{}
	recorder := httptest.NewRecorder()
	handler.shutdownDaemon(recorder, shutdownRequest("127.0.0.1:4000", "anything"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when shutdown is not configured, got %d", recorder.Code)
	}
}

func TestInternalServerEndpointsNeedTheTokenAndALocalCaller(t *testing.T) {
	handler := apiHandler{shutdownToken: "secret"}
	cases := []struct {
		name   string
		remote string
		token  string
		status int
	}{
		{"remote caller", "203.0.113.5:4000", "secret", http.StatusNotFound},
		{"missing token", "127.0.0.1:4000", "", http.StatusForbidden},
		{"wrong token", "127.0.0.1:4000", "nope", http.StatusForbidden},
		{"local caller with the token", "127.0.0.1:4000", "secret", http.StatusOK},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/internal/running-servers", nil)
		request.RemoteAddr = tc.remote
		if tc.token != "" {
			request.Header.Set(ShutdownTokenHeader, tc.token)
		}
		recorder := httptest.NewRecorder()
		handler.internalRunningServers(recorder, request)
		if recorder.Code != tc.status {
			t.Fatalf("running-servers, %s: expected %d, got %d", tc.name, tc.status, recorder.Code)
		}

		post := httptest.NewRequest(http.MethodPost, "/api/internal/resume-servers", strings.NewReader(`{"serverIds":[]}`))
		post.RemoteAddr = tc.remote
		if tc.token != "" {
			post.Header.Set(ShutdownTokenHeader, tc.token)
		}
		recorder = httptest.NewRecorder()
		handler.internalResumeServers(recorder, post)
		if recorder.Code != tc.status {
			t.Fatalf("resume-servers, %s: expected %d, got %d", tc.name, tc.status, recorder.Code)
		}
	}
}
