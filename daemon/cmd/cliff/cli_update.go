package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/updater"
)

// restartPlan is everything needed to bring Cliff back up after an update, or
// to put the previous version back if the new one does not start.
type restartPlan struct {
	Binary        string
	Host          string
	Port          int
	DataDir       string
	ServerRoot    string
	WebDir        string
	ExpectVersion string
	FromVersion   string
	// ResumeIDs are the servers that were running before the update.
	ResumeIDs []string
}

func (p restartPlan) startArgs() []string {
	host := p.Host
	if host == "" {
		host = "0.0.0.0"
	}
	return []string{
		"start", "--host", host, "--port", fmt.Sprint(p.Port),
		"--data-dir", p.DataDir, "--server-root", p.ServerRoot, "--web-dir", p.WebDir,
	}
}

func sameVersion(a string, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

// waitForVersion polls the daemon until it reports the wanted version.
func waitForVersion(port int, version string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if health := readDaemonHealth(port); health != nil && sameVersion(health.Build.Version, version) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// startAndVerify starts the daemon from plan.Binary and confirms it reports the expected version.
func startAndVerify(plan restartPlan, expected string, out *os.File) bool {
	cmd := exec.Command(plan.Binary, plan.startArgs()...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return false
	}
	return waitForVersion(plan.Port, expected, 20*time.Second)
}

// rollBackAndRestart stops whatever is left of the new version, puts the
// previous program files back and starts them again.
func rollBackAndRestart(plan restartPlan, out *os.File) error {
	if leftover := findDaemon(plan.DataDir); leftover != nil {
		if err := stopDaemon(leftover, plan.DataDir); err != nil {
			return fmt.Errorf("could not stop the new version: %w", err)
		}
	}
	if _, err := updater.Rollback(plan.Binary, plan.WebDir); err != nil {
		return fmt.Errorf("could not restore the previous version: %w", err)
	}
	if !startAndVerify(plan, plan.FromVersion, out) {
		return fmt.Errorf("the previous version was restored but did not start; run 'cliff start' and check the logs")
	}
	return nil
}

// ---- hidden: cliff __update-watchdog ----

// runUpdateWatchdog is run by the previous version after an in-app update. It
// watches the restarted daemon; if the new version does not report itself
// healthy in time, it restores the previous version and starts it again.
func runUpdateWatchdog(args []string) {
	fs := flag.NewFlagSet(updater.WatchdogCommand, flag.ExitOnError)
	var plan restartPlan
	var timeoutSeconds int
	var resume string
	fs.StringVar(&resume, "resume-servers", "", "")
	fs.StringVar(&plan.Binary, "binary", "", "")
	fs.StringVar(&plan.Host, "host", "", "")
	fs.IntVar(&plan.Port, "port", 8080, "")
	fs.StringVar(&plan.DataDir, "data-dir", "", "")
	fs.StringVar(&plan.ServerRoot, "server-root", "", "")
	fs.StringVar(&plan.WebDir, "web-dir", "", "")
	fs.StringVar(&plan.ExpectVersion, "expect-version", "", "")
	fs.StringVar(&plan.FromVersion, "from-version", "", "")
	fs.IntVar(&timeoutSeconds, "timeout-seconds", 60, "")
	fs.Parse(args)
	for _, id := range strings.Split(resume, ",") {
		if id = strings.TrimSpace(id); id != "" {
			plan.ResumeIDs = append(plan.ResumeIDs, id)
		}
	}

	logPath := filepath.Join(plan.DataDir, "logs", "update-watchdog.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile = os.Stderr
	}
	defer logFile.Close()
	logf := func(format string, values ...any) {
		fmt.Fprintf(logFile, "%s "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, values...)...)
	}

	logf("watching the restart: expecting v%s on port %d (rolling back to v%s if it does not come up)", plan.ExpectVersion, plan.Port, plan.FromVersion)
	if waitForVersion(plan.Port, plan.ExpectVersion, time.Duration(timeoutSeconds)*time.Second) {
		logf("v%s is up and healthy", plan.ExpectVersion)
		restarted, notRestarted := resumeServers(plan.Port, plan.DataDir, plan.ResumeIDs)
		logf("servers started again: %v; not started: %v", restarted, notRestarted)
		message := fmt.Sprintf("Cliff was updated to v%s and is running normally.", strings.TrimPrefix(plan.ExpectVersion, "v"))
		if extra := resumeSummary(restarted, notRestarted); extra != "" {
			message += " " + extra
		}
		_ = updater.WriteUpdateResult(plan.DataDir, updater.UpdateResult{
			Status: "updated", From: plan.FromVersion, To: plan.ExpectVersion,
			Message: message, Restarted: restarted, NotRestarted: notRestarted,
		})
		return
	}

	logf("v%s did not become healthy within %ds; going back to v%s", plan.ExpectVersion, timeoutSeconds, plan.FromVersion)
	if err := rollBackAndRestart(plan, logFile); err != nil {
		logf("rollback problem: %s", err)
		_ = updater.WriteUpdateResult(plan.DataDir, updater.UpdateResult{
			Status:  "failed",
			From:    plan.FromVersion,
			To:      plan.ExpectVersion,
			Message: fmt.Sprintf("The update to v%s did not start and the automatic rollback failed: %s. Run 'cliff rollback', then 'cliff start'.", strings.TrimPrefix(plan.ExpectVersion, "v"), err),
		})
		os.Exit(1)
	}
	logf("v%s restored and running", plan.FromVersion)
	restarted, notRestarted := resumeServers(plan.Port, plan.DataDir, plan.ResumeIDs)
	logf("servers started again: %v; not started: %v", restarted, notRestarted)
	message := fmt.Sprintf("Cliff v%s did not start correctly, so v%s was restored automatically. Your worlds and settings were not changed.", strings.TrimPrefix(plan.ExpectVersion, "v"), strings.TrimPrefix(plan.FromVersion, "v"))
	if extra := resumeSummary(restarted, notRestarted); extra != "" {
		message += " " + extra
	}
	_ = updater.WriteUpdateResult(plan.DataDir, updater.UpdateResult{
		Status: "rolled-back", From: plan.FromVersion, To: plan.ExpectVersion,
		Message: message, Restarted: restarted, NotRestarted: notRestarted,
	})
}

// ---- cliff update ----

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	var checkOnly bool
	var yes bool
	var dataDir string
	var webDir string
	fs.BoolVar(&yes, "yes", false, "skip the confirmation when a server is running")
	fs.BoolVar(&yes, "y", false, "skip the confirmation when a server is running (shorthand)")
	fs.BoolVar(&checkOnly, "check", false, "only check for updates, don't apply")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()
	dataDirProvided := dataDir != ""
	webDirProvided := webDir != ""
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))
	info := findDaemon(dataDir)
	var state *cliffState
	if info != nil {
		state = info.State
	}
	if state != nil {
		if !dataDirProvided {
			dataDir = resolveCLIPath(state.DataDir, dataDir)
		}
		if !webDirProvided {
			webDir = resolveCLIPath(state.WebDir, webDir)
		}
	}

	self, _ := os.Executable()
	mgr := updater.NewManager(self, webDir, dataDir)

	fmt.Println("Checking for updates...")
	result := mgr.CheckNow(context.Background())

	fmt.Printf("  Current version: %s\n", result.CurrentVersion)
	fmt.Printf("  Latest version:  %s\n", result.LatestVersion)

	if result.Error != "" {
		fmt.Fprintf(os.Stderr, "  Error: %s\n", result.Error)
		os.Exit(1)
	}

	if !result.UpdateAvailable {
		fmt.Println("Cliff is up to date.")
		return
	}

	fmt.Printf("  Released:        %s\n", result.BuiltAt)
	if result.ArchiveSize > 0 {
		fmt.Printf("  Download size:   %s\n", formatBytes(result.ArchiveSize))
	}
	if result.SafetyCopyBytes > 0 {
		fmt.Printf("  Kept for undo:   about %s (the current version, plus a small copy of Cliff's database)\n", formatBytes(result.SafetyCopyBytes))
	}

	if checkOnly {
		fmt.Println("\nUpdate available. Run 'cliff update' to install it.")
		return
	}

	wasRunning := info != nil
	plan := restartPlan{
		Binary:      self,
		Host:        "0.0.0.0",
		Port:        8080,
		DataDir:     dataDir,
		ServerRoot:  resolveCLIPath(os.Getenv("CLIFF_SERVER_ROOT"), filepath.Join(root, "servers")),
		WebDir:      webDir,
		FromVersion: buildinfo.Current().Version,
	}
	if state != nil {
		if state.Host != "" {
			plan.Host = state.Host
		}
		if state.ServerRoot != "" {
			plan.ServerRoot = resolveCLIPath(state.ServerRoot, plan.ServerRoot)
		}
	}
	if info != nil && info.Port > 0 {
		plan.Port = info.Port
	}

	// A running server is stopped by the update. Say so and ask first.
	var running []serverRef
	if wasRunning {
		running = fetchRunningServers(plan.Port, dataDir)
	}
	if len(running) > 0 {
		plan.ResumeIDs = serverIDs(running)
		fmt.Printf("\nMinecraft server %s is running.\n", serverNames(running))
		fmt.Println("The update stops it (the world is saved first) and starts it again afterwards.")
		fmt.Println("Players are disconnected for about a minute.")
		if !yes && !confirmPrompt("Type 'yes' to update now: ") {
			fmt.Println("Update cancelled. Nothing was changed.")
			return
		}
		fmt.Println()
	}

	// Say what is happening at each step.
	step := 0
	mgr.SetStageHook(func(stage string, message string) {
		if stage == updater.StageFailed {
			return
		}
		step++
		fmt.Printf("  [%d] %s\n", step, message)
	})

	stopped := false
	applyResult, err := mgr.Apply(context.Background(), updater.ApplyHooks{
		BeforeSwap: func(context.Context) error {
			backup := updater.PreUpdateBackupPath(dataDir, plan.FromVersion)
			mgr.SetStage(updater.StageBackup, "Backing up Cliff's settings and server list (a small database copy). Your worlds are not copied.")
			if wasRunning {
				mgr.SetStage(updater.StageStopping, "Stopping Cliff and any running Minecraft server so worlds are saved...")
				if err := stopDaemon(info, dataDir); err != nil {
					return fmt.Errorf("could not stop the daemon (PID %d): %w", info.PID, err)
				}
				stopped = true
			}
			if err := updater.CopyDatabase(filepath.Join(dataDir, "dashboard.sqlite"), backup); err != nil {
				return fmt.Errorf("could not back up the database: %w", err)
			}
			updater.PruneBackups(updater.PreUpdateBackupDir(dataDir), 3)
			return nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", err)
		if stopped {
			fmt.Fprintln(os.Stderr, "Starting the current version again...")
			if !startAndVerify(plan, plan.FromVersion, os.Stdout) {
				fmt.Fprintln(os.Stderr, "Could not restart Cliff. Run 'cliff start' and check 'cliff logs'.")
			}
		} else {
			fmt.Fprintln(os.Stderr, "Nothing was changed.")
		}
		os.Exit(1)
	}
	if !applyResult.Success {
		fmt.Fprintf(os.Stderr, "Update failed: %s\n", applyResult.Message)
		os.Exit(1)
	}
	plan.ExpectVersion = applyResult.NewVersion

	if wasRunning {
		fmt.Println("  [+] Starting the new version and checking that it runs...")
		if startAndVerify(plan, plan.ExpectVersion, os.Stdout) {
			restarted, notRestarted := bringServersBack(plan)
			message := fmt.Sprintf("Cliff was updated to v%s and is running normally.", strings.TrimPrefix(plan.ExpectVersion, "v"))
			if extra := resumeSummary(restarted, notRestarted); extra != "" {
				message += " " + extra
			}
			_ = updater.WriteUpdateResult(dataDir, updater.UpdateResult{
				Status: "updated", From: plan.FromVersion, To: plan.ExpectVersion,
				Message: message, Restarted: restarted, NotRestarted: notRestarted,
			})
		} else {
			fmt.Fprintf(os.Stderr, "\nThe new version (v%s) did not start correctly. Going back to v%s...\n", strings.TrimPrefix(plan.ExpectVersion, "v"), strings.TrimPrefix(plan.FromVersion, "v"))
			if err := rollBackAndRestart(plan, os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "Automatic rollback failed: %s\n", err)
				fmt.Fprintln(os.Stderr, "Run 'cliff rollback', then 'cliff start'.")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "v%s is running again. Your worlds and settings were not changed.\n", strings.TrimPrefix(plan.FromVersion, "v"))
			bringServersBack(plan)
			os.Exit(1)
		}
	}

	fmt.Printf("\nUpdated to v%s.\n", strings.TrimPrefix(plan.ExpectVersion, "v"))
	if !wasRunning {
		fmt.Println("Cliff was not running before the update; start it with 'cliff start'.")
	}
	printSafetyNote(mgr)
}

// bringServersBack starts the servers that were running before the update and says how that went.
func bringServersBack(plan restartPlan) (restarted []string, notRestarted []string) {
	if len(plan.ResumeIDs) == 0 {
		return nil, nil
	}
	fmt.Println("  [+] Starting your Minecraft server again...")
	restarted, notRestarted = resumeServers(plan.Port, plan.DataDir, plan.ResumeIDs)
	if len(restarted) > 0 {
		fmt.Printf("      Started again: %s\n", strings.Join(restarted, ", "))
	}
	for _, problem := range notRestarted {
		fmt.Fprintf(os.Stderr, "      Could not start again: %s. Start it from the dashboard.\n", problem)
	}
	return restarted, notRestarted
}

// printSafetyNote tells the user what was kept for undoing an update and how to remove it.
func printSafetyNote(mgr *updater.Manager) {
	safety := mgr.SafetyInfo()
	fmt.Println("\nKept so this update can be undone:")
	fmt.Printf("  Previous version:  %s\n", formatBytes(safety.PreviousVersionBytes))
	fmt.Printf("  Database copies:   %d (%s) in %s\n", safety.BackupCount, formatBytes(safety.BackupBytes), safety.BackupDir)
	fmt.Println("  Undo the update:   cliff rollback")
	fmt.Println("  Free this space:   cliff cleanup   (you can no longer roll back afterwards)")
}

// ---- cliff rollback ----

func runRollback(args []string) {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	var yes bool
	var dataDir string
	var webDir string
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompt")
	fs.BoolVar(&yes, "y", false, "skip confirmation prompt (shorthand)")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))
	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	info := findDaemon(dataDir)
	if info != nil && info.State != nil && info.State.WebDir != "" {
		webDir = resolveCLIPath(info.State.WebDir, webDir)
	}

	if !yes {
		fmt.Println("This puts back the version of Cliff you had before the last update.")
		fmt.Println("Your servers, worlds and settings are not changed.")
		if !confirmPrompt("Type 'yes' to continue: ") {
			fmt.Println("Rollback cancelled. Nothing was changed.")
			return
		}
	}

	if info != nil {
		fmt.Printf("Stopping Cliff (PID %d)...\n", info.PID)
		if err := stopDaemon(info, dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stop Cliff: %s. Nothing was changed.\n", err)
			os.Exit(1)
		}
	}

	restored, err := updater.Rollback(self, webDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Rollback failed: %s\n", err)
		os.Exit(1)
	}
	for _, path := range restored {
		fmt.Printf("Restored: %s\n", path)
	}
	fmt.Printf("Database copies from before updates are kept in %s.\n", updater.PreUpdateBackupDir(dataDir))
	if info != nil {
		fmt.Println("Start Cliff again with: cliff start")
	}
}

// ---- cliff cleanup ----

func runCleanup(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	var yes bool
	var dataDir string
	var webDir string
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompt")
	fs.BoolVar(&yes, "y", false, "skip confirmation prompt (shorthand)")
	fs.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory (default: <install-dir>/data)")
	fs.StringVar(&webDir, "web-dir", os.Getenv("CLIFF_WEB_DIR"), "static dashboard directory")
	fs.Parse(args)

	root := installRoot()
	dataDir = resolveCLIPath(dataDir, filepath.Join(root, "data"))
	webDir = resolveCLIPath(webDir, filepath.Join(root, "web"))
	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	mgr := updater.NewManager(self, webDir, dataDir)

	safety := mgr.SafetyInfo()
	if safety.TotalBytes == 0 && safety.BackupCount == 0 && !safety.CanRollback {
		fmt.Println("Nothing to clean up. Cliff keeps the previous version and a database copy only after an update.")
		return
	}
	fmt.Println("Cliff keeps these so an update can be undone:")
	fmt.Printf("  Previous version:  %s\n", formatBytes(safety.PreviousVersionBytes))
	fmt.Printf("  Database copies:   %d (%s) in %s\n", safety.BackupCount, formatBytes(safety.BackupBytes), safety.BackupDir)
	fmt.Printf("  Total:             %s\n", formatBytes(safety.TotalBytes))
	fmt.Println("Deleting them frees the space, but 'cliff rollback' will have nothing to go back to.")
	fmt.Println("Your servers, worlds and settings are not touched.")

	if !yes && !confirmPrompt("Type 'yes' to delete them: ") {
		fmt.Println("Nothing was deleted.")
		return
	}
	freed, err := mgr.ClearSafetyCopies()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		if freed > 0 {
			fmt.Printf("Freed %s.\n", formatBytes(freed))
		}
		os.Exit(1)
	}
	fmt.Printf("Freed %s.\n", formatBytes(freed))
}
