package httpserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func states(snapshot createProgressSnapshot) string {
	parts := make([]string, 0, len(snapshot.Steps))
	for _, step := range snapshot.Steps {
		parts = append(parts, step.ID+":"+step.State)
	}
	return strings.Join(parts, " ")
}

func TestCreateStepsMatchWhatEachKindDoes(t *testing.T) {
	ids := func(input serverCreateInput) string {
		out := []string{}
		for _, step := range createSteps(input) {
			out = append(out, step.ID)
		}
		return strings.Join(out, ",")
	}
	cases := []struct {
		name  string
		input serverCreateInput
		want  string
	}{
		{"vanilla create", serverCreateInput{Mode: "create", Type: "vanilla", MinecraftVersion: "1.21.1"}, "prepare,lookup,download,settings,save"},
		{"default mode with paper", serverCreateInput{Mode: "", Type: "paper"}, "prepare,lookup,download,settings,save"},
		{"neoforge create", serverCreateInput{Mode: "create", Type: "neoforge"}, "prepare,download,settings,java,install,save"},
		{"fabric create", serverCreateInput{Mode: "create", Type: "fabric"}, "prepare,lookup,download,settings,save"},
		{"clone", serverCreateInput{Mode: "clone"}, "copy,save,properties"},
		{"staged import", serverCreateInput{Mode: "import-staged"}, "check,copy,detect,save"},
		{"import", serverCreateInput{Mode: "import"}, "check,copy,detect,save"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(tc.input); got != tc.want {
				t.Errorf("%+v: steps %q, want %q", tc.input, got, tc.want)
			}
		})
	}
	if label := createSteps(serverCreateInput{Type: "vanilla", MinecraftVersion: "1.21.1"})[1].Label; !strings.Contains(label, "1.21.1") {
		t.Fatalf("the lookup step should name the Minecraft version, got %q", label)
	}
}

func TestProgressMovesThroughStepsAndFinishes(t *testing.T) {
	store := newCreateProgressStore()
	progress := store.begin("abcdef123456", createSteps(serverCreateInput{Type: "vanilla"}))
	if progress == nil {
		t.Fatal("a valid id should be tracked")
	}
	progress.start("prepare")
	progress.start("download")
	progress.setDetail("10.0 MB of 59.4 MB")
	snapshot, ok := store.snapshot("abcdef123456")
	if !ok || states(snapshot) != "prepare:done lookup:done download:active settings:pending save:pending" || snapshot.Detail != "10.0 MB of 59.4 MB" {
		t.Fatalf("unexpected mid-way snapshot: %s (%q)", states(snapshot), snapshot.Detail)
	}
	progress.start("settings")
	if snapshot, _ := store.snapshot("abcdef123456"); snapshot.Detail != "" {
		t.Fatal("a new step starts without the old step's detail")
	}
	progress.finish(nil)
	snapshot, _ = store.snapshot("abcdef123456")
	if !snapshot.Done || snapshot.Error != "" || strings.Contains(states(snapshot), "pending") || strings.Contains(states(snapshot), "active") {
		t.Fatalf("a finished operation has every step done: %s", states(snapshot))
	}
}

func TestProgressFailureMarksTheRunningStep(t *testing.T) {
	store := newCreateProgressStore()
	progress := store.begin("failure-id-01", createSteps(serverCreateInput{Type: "neoforge"}))
	progress.start("java")
	progress.finish(errors.New("managed Java setup failed"))
	snapshot, _ := store.snapshot("failure-id-01")
	if !snapshot.Done || snapshot.Error != "managed Java setup failed" {
		t.Fatalf("the failure should be recorded: %+v", snapshot)
	}
	if !strings.Contains(states(snapshot), "java:failed") || !strings.Contains(states(snapshot), "download:done") || !strings.Contains(states(snapshot), "install:pending") {
		t.Fatalf("the running step fails, earlier ones stay done, later ones stay pending: %s", states(snapshot))
	}
}

func TestProgressIsSafeWithoutAWatcher(t *testing.T) {
	var none *createProgress
	none.start("anything")
	none.setDetail("x")
	none.finish(errors.New("e"))
	if newCreateProgressStore().begin("", createSteps(serverCreateInput{})) != nil {
		t.Fatal("no id means nobody is watching")
	}
	if newCreateProgressStore().begin("bad id!", createSteps(serverCreateInput{})) != nil {
		t.Fatal("a malformed id is ignored")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if progressFrom(request) != nil {
		t.Fatal("a request without progress gives none")
	}
	progressFrom(request).start("x")
}

func TestProgressHandlerAnswersOnlyForKnownOperations(t *testing.T) {
	handler := apiHandler{createProgress: newCreateProgressStore()}
	handler.createProgress.begin("known-id-0001", createSteps(serverCreateInput{Mode: "clone"}))

	request := httptest.NewRequest(http.MethodGet, "/api/create-progress/known-id-0001", nil)
	request.SetPathValue("id", "known-id-0001")
	recorder := httptest.NewRecorder()
	handler.createProgressHandler(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"copy"`) {
		t.Fatalf("a known operation should answer with its steps, got %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/create-progress/other-id-0002", nil)
	request.SetPathValue("id", "other-id-0002")
	recorder = httptest.NewRecorder()
	handler.createProgressHandler(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("an unknown operation should be 404, got %d", recorder.Code)
	}
}

func TestProgressReaderReportsDownloadSize(t *testing.T) {
	progress := newCreateProgressStore().begin("reader-id-0001", createSteps(serverCreateInput{Type: "vanilla"}))
	reader := &progressReader{reader: strings.NewReader(strings.Repeat("x", 2048)), progress: progress, total: 4096}
	buffer := make([]byte, 1024)
	if _, err := reader.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if snapshot := progress.snapshot(); !strings.Contains(snapshot.Detail, "of") {
		t.Fatalf("the detail should say how much has arrived, got %q", snapshot.Detail)
	}
}

func TestConfirmServersStopAllowsWhenNothingIsRunning(t *testing.T) {
	handler := apiHandler{}
	request := httptest.NewRequest(http.MethodPost, "/api/daemon/restart", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	ids, ok := handler.confirmServersStop(recorder, request)
	if !ok || len(ids) != 0 || recorder.Body.Len() != 0 {
		t.Fatalf("with no server running there is nothing to confirm, got ok=%v ids=%v body=%q", ok, ids, recorder.Body.String())
	}
}
