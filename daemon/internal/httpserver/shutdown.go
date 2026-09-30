package httpserver

import (
	"crypto/subtle"
	"net"
	"net/http"
	"time"
)

// ShutdownTokenHeader carries the secret the CLI reads from <data-dir>/cliff.token.
const ShutdownTokenHeader = "X-Cliff-Token"

// requestFromLoopback reports whether the connection came from this machine
// and was not relayed by a proxy.
func requestFromLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// shutdownDaemon lets `cliff stop` ask the daemon to shut down cleanly (saving
// worlds and stopping Minecraft servers) on every platform. It only answers a
// local caller that holds the token file written when the daemon started.
func (h apiHandler) shutdownDaemon(w http.ResponseWriter, r *http.Request) {
	if h.shutdown == nil || h.shutdownToken == "" || !requestFromLoopback(r) {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	given := r.Header.Get(ShutdownTokenHeader)
	if subtle.ConstantTimeCompare([]byte(given), []byte(h.shutdownToken)) != 1 {
		writeError(w, http.StatusForbidden, "Forbidden")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "Shutting down"})
	go func() {
		// Let the response reach the client first.
		time.Sleep(100 * time.Millisecond)
		h.shutdown()
	}()
}
