// Package styles defines the lipgloss theme used by every TUI screen.
//
// Centralising colour and layout decisions here keeps the look coherent and
// makes future re-skinning a one-file change.
package styles

import "github.com/charmbracelet/lipgloss"

// Palette — a small, deliberate set of colours. Hex codes are used so the
// theme looks the same across terminals that support truecolour.
var (
	ColorBase    = lipgloss.Color("#1a1b26") // deep background
	ColorSurface = lipgloss.Color("#24283b") // panel background
	ColorText    = lipgloss.Color("#c0caf5") // primary text
	ColorDim     = lipgloss.Color("#565f89") // secondary / hints
	ColorAccent  = lipgloss.Color("#7aa2f7") // selection / focus
	ColorAccent2 = lipgloss.Color("#bb9af7") // alternate accent
	ColorOK      = lipgloss.Color("#9ece6a")
	ColorWarn    = lipgloss.Color("#e0af68")
	ColorErr     = lipgloss.Color("#f7768e")
)

// PodPalette is a rotating colour set used to tint per-pod log line prefixes.
var PodPalette = []lipgloss.Color{
	"#7aa2f7", // blue
	"#bb9af7", // purple
	"#9ece6a", // green
	"#e0af68", // yellow
	"#f7768e", // red/pink
	"#7dcfff", // cyan
	"#ff9e64", // orange
	"#73daca", // teal
}

// Reusable style snippets.
var (
	Title = lipgloss.NewStyle().
		Foreground(ColorBase).
		Background(ColorAccent).
		Bold(true).
		Padding(0, 1)

	Subtitle = lipgloss.NewStyle().
			Foreground(ColorDim).
			Padding(0, 1)

	StatusBar = lipgloss.NewStyle().
			Foreground(ColorDim).
			Background(ColorSurface).
			Padding(0, 1)

	StatusKey = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)

	StatusValue = lipgloss.NewStyle().
			Foreground(ColorText)

	Error = lipgloss.NewStyle().
		Foreground(ColorErr).
		Bold(true)

	Hint = lipgloss.NewStyle().
		Foreground(ColorDim).
		Italic(true)
)

// PodColor returns a stable colour for a given pod index.
func PodColor(i int) lipgloss.Color {
	return PodPalette[i%len(PodPalette)]
}
