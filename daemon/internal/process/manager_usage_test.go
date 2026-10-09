package process

import (
	"strconv"
	"testing"
)

func TestRememberUsageSampleIsBounded(t *testing.T) {
	proc := &managedProcess{}
	for i := 0; i < maxUsageSamples+7; i++ {
		rememberUsageSample(proc, UsageSample{At: strconv.Itoa(i)})
	}

	if len(proc.usageSamples) != maxUsageSamples {
		t.Fatalf("expected %d usage samples, got %d", maxUsageSamples, len(proc.usageSamples))
	}
	if proc.usageSamples[0].At != "7" {
		t.Fatalf("expected oldest retained sample to be 7, got %q", proc.usageSamples[0].At)
	}
	if proc.usageSamples[len(proc.usageSamples)-1].At != "42" {
		t.Fatalf("expected newest retained sample to be 42, got %q", proc.usageSamples[len(proc.usageSamples)-1].At)
	}
}

func TestCollectUsageFromRowsIncludesProcessTree(t *testing.T) {
	usage := collectUsageFromRows(10, []processUsageRow{
		{pid: 1, parentPID: 0, rssKB: 500, cpuPercent: 99},
		{pid: 10, parentPID: 1, rssKB: 100, cpuPercent: 1.5},
		{pid: 11, parentPID: 10, rssKB: 200, cpuPercent: 2.5},
		{pid: 12, parentPID: 11, rssKB: 300, cpuPercent: 3.0},
		{pid: 20, parentPID: 1, rssKB: 400, cpuPercent: 4.0},
	})

	if usage.cpuPercent == nil || *usage.cpuPercent != 7.0 {
		t.Fatalf("expected tree CPU 7.0, got %#v", usage.cpuPercent)
	}
	if usage.memoryBytes == nil || *usage.memoryBytes != 600*1024 {
		t.Fatalf("expected tree memory 600 KiB, got %#v", usage.memoryBytes)
	}
}

func TestParseUnixProcessTable(t *testing.T) {
	rows := parseUnixProcessTable([]byte(`
    10     1  100  1.5
    11    10  200  2.5
    bad line
    12    11  300  3.0
  `))
	if len(rows) != 3 {
		t.Fatalf("expected 3 parsed rows, got %#v", rows)
	}
	if rows[1].pid != 11 || rows[1].parentPID != 10 || rows[1].rssKB != 200 || rows[1].cpuPercent != 2.5 {
		t.Fatalf("unexpected parsed row: %#v", rows[1])
	}
}

func TestParseWindowsUsageJSON(t *testing.T) {
	usage := parseWindowsUsageJSON([]byte(`{"CPU":12.5,"WorkingSet64":4096,"ProcessCount":3}`))
	if usage.cpuSeconds == nil || *usage.cpuSeconds != 12.5 {
		t.Fatalf("expected CPU seconds 12.5, got %#v", usage.cpuSeconds)
	}
	if usage.memoryBytes == nil || *usage.memoryBytes != 4096 {
		t.Fatalf("expected memory 4096, got %#v", usage.memoryBytes)
	}
}
