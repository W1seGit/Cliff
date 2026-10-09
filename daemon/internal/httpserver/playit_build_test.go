package httpserver

import (
	"strings"
	"testing"
)

func TestPlayitBuildManagerParsesMarkers(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		check func(t *testing.T, job *playitSubprocessJob)
	}{
		{"step", "[cliff:step] cloning playit-agent", func(t *testing.T, job *playitSubprocessJob) {
			if job.step != "cloning playit-agent" {
				t.Fatalf("step = %q, want %q", job.step, "cloning playit-agent")
			}
			if len(job.logs) != 1 {
				t.Fatalf("expected the marker to be logged once, got %d lines", len(job.logs))
			}
		}},
		{"done", "[cliff:done]", func(t *testing.T, job *playitSubprocessJob) {
			if !job.done {
				t.Fatal("expected job.done to be true after the done marker")
			}
		}},
		{"error", "[cliff:error] cargo not found", func(t *testing.T, job *playitSubprocessJob) {
			if job.lastError != "cargo not found" {
				t.Fatalf("lastError = %q, want %q", job.lastError, "cargo not found")
			}
		}},
		{"dep", "[cliff:dep] rust installing", func(t *testing.T, job *playitSubprocessJob) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newPlayitBuildManager()
			job := &playitSubprocessJob{}

			mgr.mu.Lock()
			handled := mgr.parseMarkersLocked(job, tc.line)
			mgr.mu.Unlock()

			if !handled {
				t.Fatalf("expected %q to be handled as a marker", tc.line)
			}
			tc.check(t, job)
		})
	}
}

func TestPlayitBuildManagerNonMarkerLogged(t *testing.T) {
	mgr := newPlayitBuildManager()
	job := &playitSubprocessJob{}

	mgr.mu.Lock()
	handled := mgr.parseMarkersLocked(job, "    Compiling playit-agent v0.17.1")
	mgr.mu.Unlock()
	if handled {
		t.Fatal("expected plain log line to not be handled as marker")
	}
	mgr.mu.Lock()
	mgr.appendJobLogLocked(job, "    Compiling playit-agent v0.17.1")
	mgr.mu.Unlock()
	if len(job.logs) != 1 || !strings.Contains(job.logs[0], "Compiling") {
		t.Fatalf("expected plain line in logs, got %#v", job.logs)
	}
}

func TestDepsMissingFiltersUninstalled(t *testing.T) {
	deps := []playitDepStatus{
		{Name: "git", Installed: true},
		{Name: "rust", Installed: false},
		{Name: "xcode-clt", Installed: false},
	}
	missing := depsMissing(deps)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing deps, got %d", len(missing))
	}
	for _, dep := range missing {
		if dep.Installed {
			t.Fatalf("missing dep %q should not be installed", dep.Name)
		}
	}
}

func TestMergeDepsStateIncludesPlatform(t *testing.T) {
	if !isMacOSPlayitBuildSupported() {
		t.Skip("mergeDepsState is darwin-gated; skipping on non-darwin")
	}
	mgr := newPlayitBuildManager()
	mgr.checkPlayitDeps()
	status := mgr.mergeDepsState(playitStatus{})
	if status.Platform == "" {
		t.Fatal("expected platform to be set by mergeDepsState")
	}
	if len(status.Deps) == 0 {
		t.Fatal("expected deps to be populated by mergeDepsState")
	}
	if !status.DepsChecked {
		t.Fatal("expected depsChecked to be true after checkPlayitDeps")
	}
}
