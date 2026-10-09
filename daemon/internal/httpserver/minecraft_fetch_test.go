package httpserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExternalHTTPTransportsHaveBoundedHeaderTimeout(t *testing.T) {
	for name, transport := range map[string]*http.Transport{
		"default": externalHTTPTransport(false),
		"ipv4":    externalHTTPTransport(true),
	} {
		if transport.ResponseHeaderTimeout != externalResponseHeaderTimeout {
			t.Fatalf("%s transport header timeout = %s, want %s", name, transport.ResponseHeaderTimeout, externalResponseHeaderTimeout)
		}
		if transport.ResponseHeaderTimeout <= 0 || transport.ResponseHeaderTimeout > 30*time.Second {
			t.Fatalf("%s transport header timeout should be bounded, got %s", name, transport.ResponseHeaderTimeout)
		}
	}
}

func TestFetchTextRejectsOversizedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(maxExternalResponseBytes)+1)))
	}))
	defer server.Close()

	_, err := fetchText(httptest.NewRequest(http.MethodGet, "/", nil), server.URL)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestDecodeBoundedJSONRejectsOversizedResponses(t *testing.T) {
	var target map[string]string
	oversized := `{"value":"` + strings.Repeat("x", int(maxExternalResponseBytes)+1) + `"}`

	err := decodeBoundedJSON(strings.NewReader(oversized), &target)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized JSON error, got %v", err)
	}
}

func TestCopyBoundedDownload(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"exactly at the limit", "12345", false},
		{"one byte over the limit", "123456", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := copyBoundedDownload(&output, strings.NewReader(tc.input), 5)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "too large") {
					t.Fatalf("copying %q: error = %v, want one containing \"too large\"", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("copying %q: unexpected error %v", tc.input, err)
			}
			if output.String() != tc.input {
				t.Fatalf("output = %q, want %q", output.String(), tc.input)
			}
		})
	}
}
