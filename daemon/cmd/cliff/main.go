package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/W1seGit/Cliff/daemon/internal/buildinfo"
	"github.com/W1seGit/Cliff/daemon/internal/config"
	"github.com/W1seGit/Cliff/daemon/internal/httpserver"
	"github.com/W1seGit/Cliff/daemon/internal/logbuf"
	"github.com/W1seGit/Cliff/daemon/internal/process"
	"github.com/W1seGit/Cliff/daemon/internal/store"
	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	startedAt := time.Now().UTC()
	var port int
	var host string
	var dataDir string
	var serverRoot string
	var webDir string
	var logFile string
	var logLevel string
	var showVersion bool

	flag.StringVar(&host, "host", getenv("CLIFF_HOST", "0.0.0.0"), "host interface to bind")
	flag.IntVar(&port, "port", getenvInt("CLIFF_PORT", 8080), "HTTP port to bind")
	flag.StringVar(&dataDir, "data-dir", os.Getenv("CLIFF_DATA_DIR"), "panel data directory")
	flag.StringVar(&serverRoot, "server-root", getenv("CLIFF_SERVER_ROOT", os.Getenv("CLIFF_SERVER_ROOT")), "Minecraft server storage root")
	flag.StringVar(&webDir, "web-dir", getenv("CLIFF_WEB_DIR", "web"), "static dashboard directory")
	flag.StringVar(&logFile, "log-file", os.Getenv("CLIFF_LOG_FILE"), "daemon log file path (defaults to <data-dir>/logs/daemon.log)")
	flag.StringVar(&logLevel, "log-level", getenv("CLIFF_LOG_LEVEL", "info"), "log level: debug, info, warn, or error")
	flag.BoolVar(&showVersion, "version", false, "print daemon version and exit")
	flag.Parse()
	if showVersion {
		info := buildinfo.Current()
		fmt.Printf("cliff %s commit %s built %s\n", info.Version, info.Commit, info.BuiltAt)
		return
	}

	resolvedDataDir := dataDir
	if resolvedDataDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to get working directory:", err)
			os.Exit(1)
		}
		resolvedDataDir = filepath.Join(cwd, ".cliff")
	}

	if logFile == "" {
		logFile = filepath.Join(resolvedDataDir, "logs", "daemon.log")
	}

	logBuffer := logbuf.New(logbuf.DefaultCapacity)
	closeLog, err := configureLogging(logFile, logLevel, logBuffer)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to configure logging:", err)
		os.Exit(1)
	}
	defer closeLog()

	slog.Info("daemon starting", "logFile", logFile, "logLevel", logLevel)

	cfg, err := config.Load(config.Options{
		DataDir:    dataDir,
		ServerRoot: serverRoot,
		WebDir:     webDir,
		Host:       host,
		Port:       port,
	})
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	db, err := store.Open(cfg.DatabasePath, cfg.ServerRoot)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	manager := process.NewManager(cfg.DataDir)
	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	defer daemonCancel()
	handler := httpserver.New(httpserver.Options{
		Config:           cfg,
		Store:            db,
		Process:          manager,
		StartedAt:        startedAt,
		SchedulerContext: daemonCtx,
		LogBuffer:        logBuffer,
	})

	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("cliff daemon listening", "local", cfg.LocalURL(), "lan", cfg.LANURLs())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("daemon stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	slog.Info("daemon shutting down")
	daemonCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("daemon shutdown failed", "error", err)
		os.Exit(1)
	}
	manager.Shutdown(25 * time.Second)
	slog.Info("daemon stopped")
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func configureLogging(logFile string, level string, logBuffer *logbuf.Buffer) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	rotator := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     30,
		Compress:   true,
	}

	writer := io.MultiWriter(os.Stderr, logBuffer.Writer(), rotator)
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: parseLogLevel(level),
	})
	slog.SetDefault(slog.New(handler))

	return func() {
		_ = rotator.Close()
	}, nil
}
