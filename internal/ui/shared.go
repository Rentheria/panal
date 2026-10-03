package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

// What other views outside this package (panal pet) share with the
// dashboard, so a status looks the same everywhere: one glyph and one color
// per status, the agents' accents and the active theme's dim color.

// Icon is the status glyph (● ✔ ✖ ◐ ⊘ ○).
func Icon(e state.Status) string { return icon(e) }

// StatusColor is the status color in the active theme.
func StatusColor(e state.Status) lipgloss.TerminalColor { return statusColor(e) }

// AgentColor is the agent's accent (its mascot's color); dim if unknown.
func AgentColor(agent string) lipgloss.TerminalColor { return agentColor(agent) }

// DimColor is the active theme's dim text color (it changes with -theme
// contrast, so it is read on every call).
func DimColor() lipgloss.TerminalColor { return cDim }

// NoColor: NO_COLOR is set; mascots are not drawn.
func NoColor() bool { return noColor() }

// DisabledAgents are the agents turned off by -off or panal.conf.
func DisabledAgents() map[string]bool { return currentDisabled() }

// AtRest: a disabled agent in this status is left out of one-line summaries.
func AtRest(e state.Status) bool { return atRest(e) }
