package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jrullan/ducklab/internal/build"
	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineapi"
	"github.com/jrullan/ducklab/internal/service"
	"github.com/jrullan/ducklab/internal/xplat"
)

func main() {
	allowOrigin := flag.String("allow-origin", "", "allow this browser origin for local development (opt-in)")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag || len(os.Args) > 1 && (os.Args[1] == "version") {
		fmt.Printf("ducklab-engine %s (%s)\n", build.Semver(), build.Provenance())
		return
	}

	// Load config
	configDir, err := xplat.ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: config dir: %v\n", err)
		os.Exit(3)
	}
	configPath := filepath.Join(configDir, "config.toml")
	if envPath := os.Getenv("DUCKLAB_CONFIG"); envPath != "" {
		configPath = envPath
	}
	cfg, created, err := config.EnsureGlobal(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(3)
	}

	if created {
		fmt.Printf("wrote a starter config to %s — edit it to point at your models\n", configPath)
	}

	// Create bus
	b := bus.New(256)

	// Create service
	svc, err := service.New(cfg, service.Options{Bus: b, ConfigPath: configPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: create service: %v\n", err)
		os.Exit(1)
	}

	// Generate token
	token, err := daemon.GenerateToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generate token: %v\n", err)
		os.Exit(1)
	}

	// Bind the port once and keep it: the same socket serves below. Probing
	// an ephemeral port and closing it left a window in which nothing
	// listened on the port engine.json advertised.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Engine.Port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listen: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Rehydrate runs from disk and repair anything a dead engine left
	// mid-flight. This runs BEFORE the listener accepts connections, so no
	// client can observe a half-recovered state.
	if err := svc.RecoverRuns(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: recover runs: %v\n", err)
	}

	// Write engine.json only now, when the next thing that happens is
	// Serve on the bound socket: a caller that finds engine.json can trust
	// /v1/health to answer. Written earlier, it named a port that refused
	// connections for as long as recovery took, and `ducklab engine restart`
	// reported "did not become ready within 15s" about an engine that came
	// up fine (B-298).
	stateDir, _ := daemon.StateDir()
	info := &daemon.EngineInfo{
		PID:        os.Getpid(),
		Port:       port,
		Token:      token,
		Version:    build.Semver(),
		Provenance: build.Provenance(),
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
		StateDir:   stateDir,
	}
	if err := daemon.WriteEngineJSON(info); err != nil {
		fmt.Fprintf(os.Stderr, "error: write engine.json: %v\n", err)
		os.Exit(1)
	}
	defer daemon.DeleteEngineJSON()

	// Create server
	server := engineapi.New(svc, b, token, build.Semver(), build.Provenance(), *allowOrigin)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: server,
	}

	grace := time.Duration(cfg.Engine.ShutdownGraceS) * time.Second
	if grace <= 0 {
		grace = 30 * time.Second
	}

	shutdown := func(reason string) {
		fmt.Printf("shutting down (%s)...\n", reason)
		ctx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		// Checkpoint in-flight work first: once the listener closes, a run
		// that is still going has no way to report itself, and on the next
		// start it would look like an orphan.
		if err := svc.PauseAllRuns(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pause runs: %v\n", err)
		}
		httpServer.Shutdown(ctx)
	}
	server.OnShutdown = func() { go shutdown("api request") }

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		shutdown("signal")
	}()

	fmt.Printf("ducklab-engine %s listening on 127.0.0.1:%d\n", build.Version, port)
	if build.Dirty() {
		fmt.Println("WARNING: built from a working tree that differed from HEAD (-dirty) — " +
			"what this engine serves may not match any commit; rebuild from a clean checkout before trusting run results")
	}
	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "error: serve: %v\n", err)
		os.Exit(1)
	}
}
