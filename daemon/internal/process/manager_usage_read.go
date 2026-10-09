package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/winproc"
)

type rawUsage struct {
	cpuSeconds  *float64
	cpuPercent  *float64
	memoryBytes *int64
}

type processUsageRow struct {
	pid        int
	parentPID  int
	rssKB      int64
	cpuPercent float64
}

func readProcessUsage(pid int) rawUsage {
	if runtime.GOOS == "windows" {
		return readWindowsProcessUsage(pid)
	}
	return readUnixProcessUsage(pid)
}

func readWindowsProcessUsage(pid int) rawUsage {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	script := fmt.Sprintf(`
$root = %d
$children = @{}
Get-CimInstance Win32_Process | ForEach-Object {
  if (-not $children.ContainsKey([int]$_.ParentProcessId)) { $children[[int]$_.ParentProcessId] = New-Object System.Collections.Generic.List[int] }
  $children[[int]$_.ParentProcessId].Add([int]$_.ProcessId)
}
$ids = New-Object System.Collections.Generic.HashSet[int]
$queue = New-Object System.Collections.Generic.Queue[int]
[void]$ids.Add($root)
$queue.Enqueue($root)
while ($queue.Count -gt 0) {
  $parent = $queue.Dequeue()
  if ($children.ContainsKey($parent)) {
    foreach ($child in $children[$parent]) {
      if ($ids.Add($child)) { $queue.Enqueue($child) }
    }
  }
}
$cpu = 0.0
$mem = 0
foreach ($id in $ids) {
  try {
    $p = Get-Process -Id $id -ErrorAction Stop
    if ($null -ne $p.CPU) { $cpu += [double]$p.CPU }
    if ($null -ne $p.WorkingSet64) { $mem += [int64]$p.WorkingSet64 }
  } catch {}
}
[Console]::WriteLine((@{ CPU = $cpu; WorkingSet64 = $mem; ProcessCount = $ids.Count } | ConvertTo-Json -Compress))
`, pid)
	probe := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script)
	winproc.Hide(probe)
	output, err := probe.Output()
	if err != nil {
		return rawUsage{}
	}
	return parseWindowsUsageJSON(output)
}

func readUnixProcessUsage(pid int) rawUsage {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-o", "pid=,ppid=,rss=,%cpu=", "-ax").Output()
	if err != nil {
		return rawUsage{}
	}
	return collectUsageFromRows(pid, parseUnixProcessTable(output))
}

func parseWindowsUsageJSON(output []byte) rawUsage {
	var parsed struct {
		CPU          *float64 `json:"CPU"`
		WorkingSet64 *int64   `json:"WorkingSet64"`
		ProcessCount int      `json:"ProcessCount"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return rawUsage{}
	}
	return rawUsage{cpuSeconds: parsed.CPU, memoryBytes: parsed.WorkingSet64}
}

func parseUnixProcessTable(output []byte) []processUsageRow {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	rows := make([]processUsageRow, 0, len(lines))
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		parentPID, _ := strconv.Atoi(parts[1])
		rssKB, _ := strconv.ParseInt(parts[2], 10, 64)
		cpuPercent, _ := strconv.ParseFloat(parts[3], 64)
		rows = append(rows, processUsageRow{pid: pid, parentPID: parentPID, rssKB: rssKB, cpuPercent: cpuPercent})
	}
	return rows
}

func collectUsageFromRows(rootPID int, rows []processUsageRow) rawUsage {
	ids := map[int]struct{}{rootPID: {}}
	changed := true
	for changed {
		changed = false
		for _, row := range rows {
			if _, parentKnown := ids[row.parentPID]; parentKnown {
				if _, known := ids[row.pid]; !known {
					ids[row.pid] = struct{}{}
					changed = true
				}
			}
		}
	}

	var cpu float64
	var memory int64
	for _, row := range rows {
		if _, ok := ids[row.pid]; !ok {
			continue
		}
		cpu += row.cpuPercent
		memory += row.rssKB * 1024
	}
	return rawUsage{cpuPercent: &cpu, memoryBytes: &memory}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
