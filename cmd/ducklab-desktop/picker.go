package main

import (
	"os"
	"path/filepath"
	"reflect"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Picker is the desktop's only native binding.
//
// Everything else the app does goes over HTTP to the engine, because clients
// hold no state (I11). Choosing a folder is the one thing HTTP cannot do: the
// engine has no screen, and asking someone to type an absolute path is the
// friction this exists to remove.
//
// It returns a path and nothing else. It does not create, register or validate
// anything — the engine does all of that, exactly as it does for the CLI.
type Picker struct{}

// ChooseDirectoryFQN is the name the frontend calls this binding by.
//
// Wails keys bound methods on "<package path>.<type>.<method>", so the name
// contains this repository's import path. Derived here rather than written out
// in TypeScript: moving the package would otherwise break the picker at
// runtime, in a way nothing would catch until someone clicked the button.
func ChooseDirectoryFQN() string {
	return reflect.TypeOf(Picker{}).PkgPath() + ".Picker.ChooseDirectory"
}

// ChooseFileFQN is the name the frontend calls the reference-file binding by.
func ChooseFileFQN() string {
	return reflect.TypeOf(Picker{}).PkgPath() + ".Picker.ChooseFile"
}

// ChooseDirectory opens the system folder chooser and returns the chosen path.
//
// An empty string means the person cancelled, which is not an error and must
// not be reported as one.
func (p *Picker) ChooseDirectory(title string) (string, error) {
	if title == "" {
		title = "Choose a project folder"
	}
	// Reached through the running app: the dialog manager is a method on it,
	// not a package-level constructor.
	dialog := application.Get().Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle(title)

	// Start somewhere useful rather than wherever the process happens to be.
	if home, err := os.UserHomeDir(); err == nil {
		dialog.SetDirectory(home)
	}
	return dialog.PromptForSingleSelection()
}

// ChooseFile opens the system file chooser for Markdown and text documents.
// An empty string means the person cancelled, which is not an error.
// referenceFilePattern is every file type a stage accepts as a reference:
// the text the loader reads and the images the engine shows a seeing
// architect (internal/service/refimages.go).
const referenceFilePattern = "*.md;*.txt;*.json;*.png;*.jpg;*.jpeg;*.webp;*.gif"

func (p *Picker) ChooseFile(title string) (string, error) {
	if title == "" {
		title = "Choose a reference document or image"
	}
	dialog := application.Get().Dialog.OpenFile().
		CanChooseDirectories(false).
		CanChooseFiles(true).
		AllowsOtherFileTypes(false).
		// Images are references too (B-457); the first filter is the default,
		// so Browse… can pick either without the person switching filters.
		AddFilter("Documents and images", referenceFilePattern).
		AddFilter("Reference documents (*.md, *.txt, *.json)", "*.md;*.txt;*.json").
		AddFilter("Images (*.png, *.jpg, *.jpeg, *.webp, *.gif)", "*.png;*.jpg;*.jpeg;*.webp;*.gif").
		SetTitle(title)
	if home, err := os.UserHomeDir(); err == nil {
		dialog.SetDirectory(home)
	}
	path, err := dialog.PromptForSingleSelection()
	if err != nil || path == "" {
		return path, err
	}
	return filepath.Abs(path)
}
