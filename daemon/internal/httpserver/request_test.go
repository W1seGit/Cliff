package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeBoundedRequestJSONAcceptsSmallBody(t *testing.T) {
	var payload struct {
		Name string `json:"name"`
	}
	err := decodeBoundedRequestJSON(strings.NewReader(`{"name":"server"}`), &payload)
	if err != nil {
		t.Fatalf("expected small JSON body to decode, got %v", err)
	}
	if payload.Name != "server" {
		t.Fatalf("unexpected decoded payload: %#v", payload)
	}
}

func TestDecodeBoundedRequestJSONRejectsOversizedBody(t *testing.T) {
	var payload map[string]string
	oversized := `{"value":"` + strings.Repeat("x", int(maxJSONRequestBytes)+1) + `"}`
	err := decodeBoundedRequestJSON(strings.NewReader(oversized), &payload)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized JSON error, got %v", err)
	}
}

func TestRequestForceUsesBoundedJSONDecoder(t *testing.T) {
	oversized := `{"force":false,"padding":"` + strings.Repeat("x", int(maxJSONRequestBytes)+1) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/servers/srv/stop", strings.NewReader(oversized))
	request.Header.Set("Content-Type", "application/json")

	_, err := requestForce(request)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized force body error, got %v", err)
	}
}
