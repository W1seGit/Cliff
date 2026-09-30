package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

// fakeDaemon answers the local-only endpoints the update relies on.
func fakeDaemon(t *testing.T, running []serverRef, results string) (port int, dataDir string, seen *[]string) {
	t.Helper()
	dataDir = t.TempDir()
	if err := writeShutdownToken(dataDir, "tok"); err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Cliff-Token") != "tok" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/internal/running-servers":
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": running})
		case "/api/internal/resume-servers":
			var body struct {
				ServerIDs []string `json:"serverIds"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, "ids="+strings.Join(body.ServerIDs, ","))
			_, _ = w.Write([]byte(results))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ = strconv.Atoi(portText)
	return port, dataDir, &calls
}

func TestFetchRunningServersAsksTheDaemon(t *testing.T) {
	port, dataDir, _ := fakeDaemon(t, []serverRef{{ID: "srv_1", Name: "Survival World", Lifecycle: "running"}}, "")
	servers := fetchRunningServers(port, dataDir)
	if len(servers) != 1 || servers[0].Name != "Survival World" {
		t.Fatalf("expected the running server, got %#v", servers)
	}
	if got := serverNames(servers); got != "'Survival World'" {
		t.Fatalf("unexpected names %q", got)
	}
	if ids := serverIDs(servers); len(ids) != 1 || ids[0] != "srv_1" {
		t.Fatalf("unexpected ids %v", ids)
	}
}

func TestFetchRunningServersIsQuietWithoutAToken(t *testing.T) {
	port, _, _ := fakeDaemon(t, []serverRef{{ID: "srv_1", Name: "X"}}, "")
	if servers := fetchRunningServers(port, t.TempDir()); len(servers) != 0 {
		t.Fatalf("without the token file nothing can be asked, got %#v", servers)
	}
}

func TestResumeServersReportsWhatCameBack(t *testing.T) {
	results := `{"results":[
		{"id":"srv_1","name":"Survival World","status":"started"},
		{"id":"srv_2","name":"Creative","status":"skipped","message":"the Minecraft EULA is not accepted"},
		{"id":"srv_3","name":"Old","status":"failed","message":"the server no longer exists"}]}`
	port, dataDir, seen := fakeDaemon(t, nil, results)
	restarted, notRestarted := resumeServers(port, dataDir, []string{"srv_1", "srv_2", "srv_3"})

	if len(restarted) != 1 || restarted[0] != "Survival World" {
		t.Fatalf("unexpected restarted list %v", restarted)
	}
	if len(notRestarted) != 2 || !strings.Contains(notRestarted[0], "EULA") || !strings.Contains(notRestarted[1], "no longer exists") {
		t.Fatalf("unexpected not-restarted list %v", notRestarted)
	}
	if joined := strings.Join(*seen, " "); !strings.Contains(joined, "ids=srv_1,srv_2,srv_3") {
		t.Fatalf("the daemon should be told which servers to start, saw %q", joined)
	}
	summary := resumeSummary(restarted, notRestarted)
	if !strings.Contains(summary, "Started again: Survival World.") || !strings.Contains(summary, "Could not start again:") {
		t.Fatalf("unexpected summary %q", summary)
	}
}

func TestResumeServersWithNothingToDoDoesNotCallTheDaemon(t *testing.T) {
	port, dataDir, seen := fakeDaemon(t, nil, `{"results":[]}`)
	if restarted, notRestarted := resumeServers(port, dataDir, nil); restarted != nil || notRestarted != nil {
		t.Fatalf("nothing to resume, got %v %v", restarted, notRestarted)
	}
	if len(*seen) != 0 {
		t.Fatalf("the daemon should not be called, saw %v", *seen)
	}
}

func TestResumeServersReportsAnUnreachableDaemon(t *testing.T) {
	dataDir := t.TempDir()
	if err := writeShutdownToken(dataDir, "tok"); err != nil {
		t.Fatal(err)
	}
	restarted, notRestarted := resumeServers(1, dataDir, []string{"srv_1"})
	if len(restarted) != 0 || len(notRestarted) != 1 || !strings.Contains(notRestarted[0], "srv_1") {
		t.Fatalf("a failed call must be reported per server, got %v %v", restarted, notRestarted)
	}
}

func TestWatchdogCarriesTheServersToBringBack(t *testing.T) {
	args := updater.WatchdogParams{Port: 8080, ResumeServerIDs: []string{"srv_a", "srv_b"}}.Args("/bin/cliff")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--resume-servers srv_a,srv_b") {
		t.Fatalf("the watchdog must be told which servers to start again, got %q", joined)
	}
}
