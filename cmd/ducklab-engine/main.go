package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jrullan/ducklab/internal/bus"
	"github.com/jrullan/ducklab/internal/config"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/engineapi"
	"github.com/jrullan/ducklab/internal/service"
	"github.com/jrullan/ducklab/internal/xplat"
)

var version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("ducklab-engine %s (%s, go1.24+, %s/%s)\n", version, "dev", "linux", "amd64")
		os.Exit(0)
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
	cfg, err := config.LoadGlobal(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(3)
	}

	// Create bus
	b := bus.New(256)

	// Create service
	svc, err := service.New(cfg, service.Options{Bus: b})
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

	// Find port
	port := cfg.Engine.Port
	if port == 0 {
		// Ephemeral port
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: listen: %v\n", err)
			os.Exit(1)
		}
		port = listener.Addr().(*net.TCPAddr).Port
		listener.Close()
	}

	// Write engine.json
	stateDir, _ := daemon.StateDir()
	info := &daemon.EngineInfo{
		PID:       os.Getpid(),
		Port:      port,
		Token:     token,
		Version:   version,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		StateDir:  stateDir,
	}
	if err := daemon.WriteEngineJSON(info); err != nil {
		fmt.Fprintf(os.Stderr, "error: write engine.json: %v\n", err)
		os.Exit(1)
	}
	defer daemon.DeleteEngineJSON()

	// Create server
	server := engineapi.New(svc, b, token, version)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: server,
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		fmt.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}()

	fmt.Printf("ducklab-engine %s listening on 127.0.0.1:%d\n", version, port)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "error: serve: %v\n", err)
		os.Exit(1)
	}
}
