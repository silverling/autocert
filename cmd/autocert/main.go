package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"autocert/internal/certmgr"
	"autocert/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	command, commandArgs := parseCommand(args)

	switch command {
	case "run":
		return runOnce(commandArgs)
	case "daemon":
		return runDaemonCommand(commandArgs)
	case "help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printUsage(os.Stderr)
		return 2
	}
}

func parseCommand(args []string) (string, []string) {
	if len(args) == 0 {
		return "run", nil
	}

	first := args[0]
	if strings.HasPrefix(first, "-") {
		return "run", args
	}

	switch first {
	case "run", "daemon", "help":
		return first, args[1:]
	default:
		return "run", args
	}
}

func runOnce(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "./config.yaml", "Path to YAML configuration file")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger := newLogger()
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "path", *configPath, "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := certmgr.New(cfg, logger)
	if err := manager.Reconcile(ctx); err != nil {
		logger.Error("certificate run failed", "error", err)
		return 1
	}

	logger.Info("certificate run completed successfully")
	return 0
}

func runDaemonCommand(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "./config.yaml", "Path to YAML configuration file")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger := newLogger()
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", "path", *configPath, "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := certmgr.New(cfg, logger)
	interval := cfg.ACME.CheckInterval.Duration
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("daemon started", "interval", interval)

	for {
		if err := manager.Reconcile(ctx); err != nil {
			logger.Error("certificate run failed", "error", err)
		}

		select {
		case <-ctx.Done():
			logger.Info("daemon stopped")
			return 0
		case <-ticker.C:
		}
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  autocert run --config ./config.yaml")
	fmt.Fprintln(out, "  autocert daemon --config ./config.yaml")
}
