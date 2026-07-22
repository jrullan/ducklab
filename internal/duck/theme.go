// Package duck is ducklab's identity: the rubber-duck character, palette, and
// Lipgloss styles that every surface shares. The solo developer's rubber duck
// listens — ducklab's ducks answer, and sometimes two of them argue.
package duck

import "github.com/charmbracelet/lipgloss"

// Palette — a pond at dusk. Duck yellow leads; the rest support it.
var (
	Yellow = lipgloss.Color("#FFD43B") // rubber-duck yellow — the brand
	Beak   = lipgloss.Color("#FF9F1C") // beak orange — accents, prompts
	Pond   = lipgloss.Color("#3DB2FF") // pond blue — keys, links
	Reed   = lipgloss.Color("#4CAF50") // reed green — success
	Warn   = lipgloss.Color("#F4C95D") // muted amber — warnings
	Splash = lipgloss.Color("#E63946") // red — errors, blocking findings
	Mist   = lipgloss.Color("#8A94A6") // dim — secondary text
)

// Styles used across CLI and TUI.
var (
	Title = lipgloss.NewStyle().Bold(true).Foreground(Yellow)
	Key   = lipgloss.NewStyle().Bold(true).Foreground(Pond)
	Value = lipgloss.NewStyle().Foreground(lipgloss.NoColor{})
	OK    = lipgloss.NewStyle().Bold(true).Foreground(Reed)
	Bad   = lipgloss.NewStyle().Bold(true).Foreground(Splash)
	Warns = lipgloss.NewStyle().Foreground(Warn)
	Dim   = lipgloss.NewStyle().Foreground(Mist)
	Quack = lipgloss.NewStyle().Italic(true).Foreground(Beak)

	prompt = lipgloss.NewStyle().Bold(true).Foreground(Beak)
)

// Duck is the mascot, small enough to sit above a prompt.
const Duck = `   __
 <(o )___
  ( ._> /
   ` + "`" + `---'`

// Banner renders the mascot beside the wordmark and a tagline.
func Banner(tagline string) string {
	art := lipgloss.NewStyle().Foreground(Yellow).Render(Duck)
	word := Title.Render("ducklab")
	sub := Dim.Render(tagline)
	right := lipgloss.JoinVertical(lipgloss.Left, "", word, sub)
	return lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", right)
}

// Prompt is the input marker for the REPL.
func Prompt() string { return prompt.Render("duck ❯ ") }

// Quips are what a rubber duck might say — rotated for a little character.
var Quips = []string{
	"Talk it through. I'm listening.",
	"Two ducks are better than one.",
	"Tests are the only vote that counts.",
	"Say it out loud — what are you really trying to do?",
	"A green test beats a confident opinion.",
}

// Quip returns a deterministic quip by index (callers vary it; no global rand).
func Quip(i int) string {
	if len(Quips) == 0 {
		return ""
	}
	if i < 0 {
		i = -i
	}
	return Quips[i%len(Quips)]
}
