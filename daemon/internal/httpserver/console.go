package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/W1seGit/Cliff/daemon/internal/process"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type consoleMessage struct {
	Type    string         `json:"type"`
	Command string         `json:"command,omitempty"`
	Logs    []string       `json:"logs,omitempty"`
	Event   *process.Event `json:"event,omitempty"`
	Status  process.Status `json:"status,omitempty"`
	Error   string         `json:"error,omitempty"`
}

const (
	consoleMaxMessageBytes   = 16 * 1024
	consoleWriteWait         = 10 * time.Second
	consolePongWait          = 60 * time.Second
	consolePingPeriod        = 25 * time.Second
	consoleUsageStatusPeriod = 5 * time.Second
	consoleLightStatusPeriod = 30 * time.Second
	consoleOutgoingQueueSize = 16
)

func (h apiHandler) console(w http.ResponseWriter, r *http.Request) {
	if _, ok, err := h.currentUser(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	serverID := r.PathValue("id")
	includeUsage := r.URL.Query().Get("usage") == "1"
	includeLogs := consoleIncludesLogs(r)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(consoleMaxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(consolePongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(consolePongWait))
	})
	writeJSON := func(message consoleMessage) error {
		if err := conn.SetWriteDeadline(time.Now().Add(consoleWriteWait)); err != nil {
			return err
		}
		return conn.WriteJSON(message)
	}
	writeMessage := func(messageType int, data []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(consoleWriteWait)); err != nil {
			return err
		}
		return conn.WriteMessage(messageType, data)
	}

	snapshot := consoleMessage{
		Type:   "snapshot",
		Status: h.runtimeStatusForConsole(serverID, includeUsage),
	}
	if includeLogs {
		snapshot.Logs = h.process.Logs(serverID)
	}
	_ = writeJSON(snapshot)

	events, unsubscribe := h.process.SubscribeFor(serverID, includeLogs)
	defer unsubscribe()

	done := make(chan struct{})
	outgoing := make(chan consoleMessage, consoleOutgoingQueueSize)
	go func() {
		defer close(done)
		for {
			var incoming consoleMessage
			if err := conn.ReadJSON(&incoming); err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(consolePongWait))
			if incoming.Type == "command" {
				if err := h.process.Command(serverID, incoming.Command); err != nil {
					select {
					case outgoing <- consoleMessage{Type: "error", Error: err.Error()}:
					default:
					}
				}
			}
		}
	}()

	ticker := time.NewTicker(consolePingPeriod)
	defer ticker.Stop()
	statusTicker := time.NewTicker(consoleStatusInterval(includeUsage))
	defer statusTicker.Stop()

	for {
		select {
		case <-done:
			return
		case event := <-events:
			if event.ServerID != serverID && event.ServerID != "" {
				continue
			}
			if event.Type == "log" && !includeLogs {
				continue
			}
			if err := writeJSON(consoleMessage{Type: "event", Event: &event}); err != nil {
				return
			}
		case message := <-outgoing:
			if err := writeJSON(message); err != nil {
				return
			}
		case <-ticker.C:
			if err := writeMessage(websocket.PingMessage, []byte("ping")); err != nil {
				return
			}
		case <-statusTicker.C:
			if err := writeJSON(consoleMessage{Type: "event", Event: &process.Event{Type: "status", ServerID: serverID, Status: h.runtimeStatusForConsole(serverID, includeUsage)}}); err != nil {
				return
			}
		}
	}
}

func (h apiHandler) runtimeStatusForConsole(serverID string, includeUsage bool) process.Status {
	if includeUsage {
		return h.process.StatusFor(serverID)
	}
	return h.process.StatusForLight(serverID)
}

func consoleStatusInterval(includeUsage bool) time.Duration {
	if includeUsage {
		return consoleUsageStatusPeriod
	}
	return consoleLightStatusPeriod
}

func consoleIncludesLogs(r *http.Request) bool {
	return r.URL.Query().Get("logs") != "0"
}

func writeProcessEvent(w http.ResponseWriter, status int, runtime process.Status) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(runtime)
}
