// Command ducklab-desktop is the Wails shell.
//
// It is a CLIENT (01 §1.2): it resolves and supervises the engine, hands the
// frontend its connection details, and does nothing the CLI cannot. All the
// work happens in ducklab-engine, which outlives this window — closing the app
// must never kill a six-minute tournament run.
//
// This is the only package that needs cgo. Everything else in ducklab builds
// with CGO_ENABLED=0 for four targets.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/jrullan/ducklab/internal/build"
	"github.com/jrullan/ducklab/internal/daemon"
	"github.com/jrullan/ducklab/internal/desktop"
	"github.com/jrullan/ducklab/internal/engineclt"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Resolve the engine before showing a window: an app that opens and then
	// reports it cannot find its engine is worse than one that says so first.
	enginePath, err := desktop.ResolveEngine(os.Getenv("DUCKLAB_ENGINE"), exec.LookPath)
	if err != nil {
		log.Printf("warning: %v", err)
	}

	info, err := desktop.EnsureEngine(enginePath, 10*time.Second)
	if err != nil {
		log.Printf("warning: engine not available: %v", err)
		info = &daemon.EngineInfo{}
	}

	notifier := notifications.New()
	shell := &Shell{notifier: notifier, enginePath: enginePath, conn: engineclt.New(info)}
	// Env drift: an adopted engine may lack a provider key THIS process has
	// (the engine is a daemon by design; it may predate the unlocked
	// keyring). Names only — never values — so the frontend can open the
	// Restart-engine door with the reason on it.
	missingKeys := []string{}
	if providers, perr := shell.conn.ProviderList(); perr == nil {
		missingKeys = desktop.MissingKeys(providers, os.Getenv)
	}
	missingJSON, _ := json.Marshal(missingKeys)
	app := application.New(application.Options{
		Name:        "ducklab",
		Description: "a multi-model software development harness",
		Assets: application.AssetOptions{
			Handler: application.BundledAssetFileServer(assets),
		},
		// One binding, and only because the engine has no screen. See picker.go.
		Services: []application.Service{
			application.NewService(&Picker{}),
			application.NewService(notifier),
			application.NewService(shell),
		},
	})

	// The frontend reads its connection details from window.ducklab. They are
	// injected rather than fetched so the UI never has to know where the
	// engine's state directory lives.
	// app.Window.NewWithOptions, NOT the package-level application.NewWindow:
	// the latter constructs a window but never registers it with the app, so
	// the process starts, serves assets, and shows nothing.
	// A window can open straight onto a route (08 §1.3): pop-outs need it, and
	// so does anyone who wants to launch the app on the view they care about.
	// Routing is hash-based precisely so this needs no router cooperation.
	route := ""
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "#/") {
		route = os.Args[1]
	}

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "ducklab",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 700,
		JS: fmt.Sprintf(
			`window.ducklab = { baseUrl: %q, token: %q, version: %q, chooseDirectory: %q, chooseFile: %q, notify: %q, setBadge: %q, restartEngine: %q, reconnectEngine: %q, openURL: %q, engineMissingKeys: %s };`+
				`if (%q) location.hash = %q;`,
			fmt.Sprintf("http://127.0.0.1:%d", info.Port), info.Token, build.Semver(),
			ChooseDirectoryFQN(), ChooseFileFQN(), NotifyFQN(), SetBadgeFQN(), RestartEngineFQN(), ReconnectEngineFQN(), OpenURLFQN(), missingJSON,
			route, route,
		),
	})
	shell.win = win

	if err := app.Run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}
