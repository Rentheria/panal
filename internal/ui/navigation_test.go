package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/AlbertoVasquezR/panal/internal/state"
)

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func TestNavigationSequences(t *testing.T) {
	fixedTime := time.Date(2026, 9, 26, 15, 4, 5, 0, time.Local)
	oldNow := nowFn
	nowFn = func() time.Time { return fixedTime }
	defer func() { nowFn = oldNow }()

	fakeReaders := setupTestEnv(t, fixedTime)

	sizes := []struct {
		width, height int
	}{
		{80, 24},
		{100, 30},
		{120, 35},
		{160, 45},
	}

	sequence := strings.Fields("t enter down down esc t enter right right esc 2 enter right esc 3 1 ? esc")

	for _, size := range sizes {
		size := size
		tc := fmt.Sprintf("%dx%d", size.width, size.height)
		t.Run(tc, func(t *testing.T) {
			m := New(fakeReaders, 2*time.Second)
			for _, c := range m.runs {
				if c.Agent == "codex" {
					m.checkChanges(c)
					break
				}
			}
			m.refresh()
			mod, _ := m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = mod.(Model)

			checkFrame := func(step string) {
				output := m.View()
				h := lipgloss.Height(output)
				if h > size.height {
					t.Errorf("%s (step %s): height = %d, max = %d", tc, step, h, size.height)
				}

				lines := strings.Split(reANSI.ReplaceAllString(output, ""), "\n")
				for i, l := range lines {
					if w := lipgloss.Width(l); w > size.width {
						t.Errorf("%s (step %s): line %d width = %d, max = %d: %q", tc, step, i+1, w, size.width, l)
					}
				}

				// With a detail panel open, its bottom border ("╰" or "┗") is on screen
				if m.detail || (m.inHistory && m.historyDetail) {
					if !strings.Contains(output, "╰") && !strings.Contains(output, "┗") {
						t.Errorf("%s (step %s): detail panel without a bottom border on screen", tc, step)
					}
				}

				// On the cards the footer says "←→" and on the table "↑↓"
				if !m.inHelp && !m.inReport && !m.inHistory && !m.detail && m.reg == nil {
					if m.compact {
						if !strings.Contains(output, "↑↓") {
							t.Errorf("%s (step %s): on the table the footer should say ↑↓", tc, step)
						}
					} else {
						if strings.Contains(output, "small terminal: table") {
							if !strings.Contains(output, "↑↓") {
								t.Errorf("%s (step %s): on the fallback table the footer should say ↑↓", tc, step)
							}
						} else {
							if !strings.Contains(output, "←→") {
								t.Errorf("%s (step %s): on the cards the footer should say ←→", tc, step)
							}
						}
					}
				}
			}

			// Initial frame
			checkFrame("start")

			// Walk through the key sequence
			for i, k := range sequence {
				mod, _ := m.Update(keyMsg(k))
				m = mod.(Model)
				checkFrame(fmt.Sprintf("%d-%s", i+1, k))
			}
		})
	}
}

// With the detail open from the table, moving between agents doesn't change
// the number of lines at the top (the corral doesn't come and go).
func TestDetailTableCorralStable(t *testing.T) {
	fixedTime := time.Date(2026, 9, 26, 15, 4, 5, 0, time.Local)
	oldNow := nowFn
	nowFn = func() time.Time { return fixedTime }
	defer func() { nowFn = oldNow }()

	fakeReaders := setupTestEnv(t, fixedTime)

	m := New(fakeReaders, 2*time.Second)
	m.refresh()
	mod, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = mod.(Model)

	// Switch to the table and open the detail
	mod, _ = m.Update(keyMsg("t"))
	m = mod.(Model)
	mod, _ = m.Update(keyMsg("enter"))
	m = mod.(Model)

	if !m.detail {
		t.Fatal("enter on the table did not open the detail")
	}

	linesAbove := -1
	for step := 0; step < len(m.rows); step++ {
		output := m.View()
		boxIdx := strings.Index(output, "╭")
		if boxIdx < 0 {
			t.Fatalf("no detail box found for agent %d", step)
		}
		numRows := strings.Count(output[:boxIdx], "\n")
		if linesAbove == -1 {
			linesAbove = numRows
		} else if numRows != linesAbove {
			t.Fatalf("the number of lines above the box changed: before %d, now %d",
				linesAbove, numRows)
		}

		// The corral must NOT show on the agent detail screen
		if strings.Contains(output, "░") && strings.Contains(output, state.OutOfQuota.String()) && strings.Contains(output, "claude") {
			t.Fatal("the mascot corral should not show on the agent detail")
		}

		mod, _ = m.Update(keyMsg("right"))
		m = mod.(Model)
	}
}
