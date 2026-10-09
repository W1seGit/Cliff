package process

import "testing"

func TestTickProbeCommand(t *testing.T) {
	cases := []struct{ serverType, version, want string }{
		{"vanilla", "1.21.1", "tick query"},
		{"paper", "1.20.3", "tick query"},
		{"fabric", "26.1", "tick query"},
		{"paper", "1.19.4", "tps"},
		{"purpur", "1.18.2", "tps"},
		{"vanilla", "1.19.4", ""},
		{"vanilla", "24w14a", ""},
	}
	for _, tc := range cases {
		if got := tickProbeCommand(tc.serverType, tc.version); got != tc.want {
			t.Errorf("%s %s: got %q, want %q", tc.serverType, tc.version, got, tc.want)
		}
	}
}

func TestParseTickReply(t *testing.T) {
	tps, mspt, ok := parseTickReply("[12:00:00] [Server thread/INFO]: Average time per tick: 2.3ms (Target: 50.0ms)")
	if !ok || mspt == nil || *mspt != 2.3 || tps == nil || *tps != 20 {
		t.Fatalf("tick query reply misread: %v %v %v", tps, mspt, ok)
	}
	tps, mspt, ok = parseTickReply("Average time per tick: 100.0ms")
	if !ok || tps == nil || *tps != 10 || mspt == nil {
		t.Fatalf("a slow server should report TPS below 20: %v %v", tps, mspt)
	}
	tps, _, ok = parseTickReply("[12:00:00] [Server thread/INFO]: TPS from last 1m, 5m, 15m: *20.0, 19.8, 19.9")
	if !ok || tps == nil || *tps != 20 {
		t.Fatalf("paper tps reply misread: %v %v", tps, ok)
	}
	if _, _, ok := parseTickReply("<Alex> hello"); ok {
		t.Fatal("chat is not a probe reply")
	}
}
