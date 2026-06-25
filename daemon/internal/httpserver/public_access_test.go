package httpserver

import "testing"

func TestSelectPlayitAssetPrefersCurrentPlatformBinary(t *testing.T) {
	assets := []githubAssetRecord{
		{Name: "playit_amd64.deb", BrowserDownloadURL: "https://example.invalid/deb"},
		{Name: "playit-linux-amd64", BrowserDownloadURL: "https://example.invalid/linux"},
		{Name: "playit-cli-linux-amd64", BrowserDownloadURL: "https://example.invalid/linux-cli"},
		{Name: "playit-windows-x86_64-signed.exe", BrowserDownloadURL: "https://example.invalid/windows"},
	}

	asset, err := selectPlayitAsset(assets, "linux", "amd64")
	if err != nil {
		t.Fatalf("expected linux asset, got error %v", err)
	}
	if asset.Name != "playit-cli-linux-amd64" {
		t.Fatalf("expected linux CLI binary, got %q", asset.Name)
	}

	asset, err = selectPlayitAsset(assets, "windows", "amd64")
	if err != nil {
		t.Fatalf("expected windows asset, got error %v", err)
	}
	if asset.Name != "playit-windows-x86_64-signed.exe" {
		t.Fatalf("expected signed windows binary, got %q", asset.Name)
	}
}

func TestSelectPlayitAssetFallsBackToLegacyLinuxBinary(t *testing.T) {
	assets := []githubAssetRecord{
		{Name: "playit-linux-amd64", BrowserDownloadURL: "https://example.invalid/linux"},
	}

	asset, err := selectPlayitAsset(assets, "linux", "amd64")
	if err != nil {
		t.Fatalf("expected linux asset, got error %v", err)
	}
	if asset.Name != "playit-linux-amd64" {
		t.Fatalf("expected legacy linux binary fallback, got %q", asset.Name)
	}
}

func TestSelectPlayitAssetRequiresSystemBinaryForMacOS(t *testing.T) {
	_, err := selectPlayitAsset(nil, "darwin", "arm64")
	if err == nil {
		t.Fatal("expected macOS system binary error")
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
