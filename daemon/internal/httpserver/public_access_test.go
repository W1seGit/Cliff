package httpserver

import (
	"strings"
	"testing"
)

func TestSelectPlayitAssetPrefersCurrentPlatformBinary(t *testing.T) {
	assets := []githubAssetRecord{
		{Name: "playit_amd64.deb", BrowserDownloadURL: "https://example.invalid/deb"},
		{Name: "playit-linux-amd64", BrowserDownloadURL: "https://example.invalid/linux"},
		{Name: "playit-windows-x86_64-signed.exe", BrowserDownloadURL: "https://example.invalid/windows"},
	}

	asset, err := selectPlayitAsset(assets, "linux", "amd64")
	if err != nil {
		t.Fatalf("expected linux asset, got error %v", err)
	}
	if asset.Name != "playit-linux-amd64" {
		t.Fatalf("expected linux binary, got %q", asset.Name)
	}

	asset, err = selectPlayitAsset(assets, "windows", "amd64")
	if err != nil {
		t.Fatalf("expected windows asset, got error %v", err)
	}
	if asset.Name != "playit-windows-x86_64-signed.exe" {
		t.Fatalf("expected signed windows binary, got %q", asset.Name)
	}
}

func TestSelectPlayitAssetSupportsMacOS(t *testing.T) {
	assets := []githubAssetRecord{
		{Name: "playit-darwin-aarch64", BrowserDownloadURL: "https://example.invalid/darwin"},
	}

	asset, err := selectPlayitAsset(assets, "darwin", "arm64")
	if err != nil {
		t.Fatalf("expected macOS asset, got error %v", err)
	}
	if asset.Name != "playit-darwin-aarch64" {
		t.Fatalf("expected macOS binary, got %q", asset.Name)
	}
}

func TestSelectPlayitAssetRejectsUnsupportedPlatform(t *testing.T) {
	_, err := selectPlayitAsset(nil, "freebsd", "arm64")
	if err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestPlayitAgentManagerCapturesClaimURL(t *testing.T) {
	manager := newPlayitAgentManager()
	manager.pushLog("Visit https://playit.gg/claim/abc123 to claim the agent")

	status := manager.mergeStatus(playitStatus{Installed: true, Path: "/tmp/playit"})
	if status.ClaimURL != "https://playit.gg/claim/abc123" {
		t.Fatalf("unexpected claim URL %q", status.ClaimURL)
	}
	if len(status.Logs) != 1 {
		t.Fatalf("expected retained log line, got %d", len(status.Logs))
	}
}

func TestParsePlayitClaimCodeFromANSIOutput(t *testing.T) {
	output := "\x1b[?1049h\x1b[38;5;15;49mf081d4c894\x1b[?1049l"
	code := parsePlayitClaimCode(stripANSI(output))
	if code != "f081d4c894" {
		t.Fatalf("unexpected claim code %q", code)
	}
}

func TestParsePlayitSecretFromExchangeOutput(t *testing.T) {
	output := "\x1b[?1049hProgram approved :). Secret code being setup.\n0123456789abcdef0123456789abcdef0123456789abcdef\x1b[?1049l"
	secret := parsePlayitSecret(output)
	if secret != "0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected secret %q", secret)
	}
}

func TestParsePlayitManagedSecretFromToml(t *testing.T) {
	secret := parsePlayitManagedSecret(`secret_key = "0123456789abcdef"`)
	if secret != "0123456789abcdef" {
		t.Fatalf("unexpected secret %q", secret)
	}
}

func TestParsePlayitTunnelListExtractsMinecraftAddress(t *testing.T) {
	payload := []byte(`{
		"status": "success",
		"data": {
			"tunnels": [{
				"id": "6bbf4551-a158-4497-8e56-d847d795d755",
				"tunnel_type": "minecraft-java",
				"name": "test",
				"port_type": "tcp",
				"port_count": 1,
				"alloc": {
					"status": "allocated",
					"data": {
						"ip_hostname": "26.ip.gl.ply.gg",
						"assigned_domain": "european-immigrants.gl.joinmc.link",
						"assigned_srv": "european-immigrants.gl.joinmc.link",
						"port_start": 36202
					}
				},
				"origin": {
					"type": "agent",
					"data": {
						"local_ip": "127.0.0.1",
						"local_port": 25565
					}
				},
				"active": true
			}]
		}
	}`)

	tunnels, err := parsePlayitTunnelList(payload)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(tunnels) != 1 {
		t.Fatalf("expected one tunnel, got %#v", tunnels)
	}
	if tunnels[0].PublicAddress != "european-immigrants.gl.joinmc.link" {
		t.Fatalf("unexpected public address %q", tunnels[0].PublicAddress)
	}
	if tunnels[0].LocalPort != 25565 {
		t.Fatalf("unexpected local port %d", tunnels[0].LocalPort)
	}
}

func TestPlayitBuildManagerParsesStepMarker(t *testing.T) {
	mgr := newPlayitBuildManager()
	job := &playitSubprocessJob{}

	// Step marker should set job.step and be logged.
	mgr.mu.Lock()
	handled := mgr.parseMarkersLocked(job, "[cliff:step] cloning playit-agent")
	mgr.mu.Unlock()
	if !handled {
		t.Fatal("expected step marker to be handled")
	}
	if job.step != "cloning playit-agent" {
		t.Fatalf("unexpected step %q", job.step)
	}
	if len(job.logs) != 1 {
		t.Fatalf("expected marker logged, got %d lines", len(job.logs))
	}
}

func TestPlayitBuildManagerParsesDoneMarker(t *testing.T) {
	mgr := newPlayitBuildManager()
	job := &playitSubprocessJob{}

	mgr.mu.Lock()
	handled := mgr.parseMarkersLocked(job, "[cliff:done]")
	mgr.mu.Unlock()
	if !handled {
		t.Fatal("expected done marker to be handled")
	}
	if !job.done {
		t.Fatal("expected job.done to be true after done marker")
	}
}

func TestPlayitBuildManagerParsesErrorMarker(t *testing.T) {
	mgr := newPlayitBuildManager()
	job := &playitSubprocessJob{}

	mgr.mu.Lock()
	handled := mgr.parseMarkersLocked(job, "[cliff:error] cargo not found")
	mgr.mu.Unlock()
	if !handled {
		t.Fatal("expected error marker to be handled")
	}
	if job.lastError != "cargo not found" {
		t.Fatalf("unexpected lastError %q", job.lastError)
	}
}

func TestPlayitBuildManagerParsesDepMarker(t *testing.T) {
	mgr := newPlayitBuildManager()
	job := &playitSubprocessJob{}

	mgr.mu.Lock()
	handled := mgr.parseMarkersLocked(job, "[cliff:dep] rust installing")
	mgr.mu.Unlock()
	if !handled {
		t.Fatal("expected dep marker to be handled")
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
