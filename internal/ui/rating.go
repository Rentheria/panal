package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AlbertoVasquezR/panal/internal/feedback"
)

// Rating runs from History: + (or =) good, - bad, the same key again clears
// it. It is the same as `panal feedback RUN good|bad`, and the router learns
// from it. The file written is panal's own (~/.panal/feedback.json), from a
// tea.Cmd.

// ratedMsg: the rating was written (or not).
type ratedMsg struct {
	key, rating string
	err         error
}

// FeedbackPath is where ratings are written ("" = feedback.Path()).
var FeedbackPath string

func rateCmd(key, rating string) tea.Cmd {
	return func() tea.Msg {
		path := FeedbackPath
		if path == "" {
			path = feedback.Path()
		}
		return ratedMsg{key, rating, feedback.Set(path, key, rating, "", nowFn())}
	}
}

// rateSelected rates the selected run in History (list or detail).
func (m *Model) rateSelected(k string) tea.Cmd {
	cs := m.filteredRuns()
	if m.historyCursor < 0 || m.historyCursor >= len(cs) {
		return nil
	}
	c := cs[m.historyCursor]
	if c.Status == runRunning {
		m.alert, m.alertTime = "the run is still going: rate it when it ends", m.now
		return nil
	}
	want := feedback.Good
	if k == "-" {
		want = feedback.Bad
	}
	if c.Rating == want {
		want = ""
	}
	// Shown at once; the next refresh reads it back from the file.
	for i := range m.runs {
		if m.runs[i].Key() == c.Key() {
			m.runs[i].Rating, m.runs[i].RatingNote = want, ""
		}
	}
	what := c.Agent + " · " + taskTitle(c.FullTask, c.Task)
	switch want {
	case feedback.Good:
		m.alert = "▲ rated good: " + what
	case feedback.Bad:
		m.alert = "▼ rated bad: " + what
	default:
		m.alert = "rating cleared: " + what
	}
	m.alertTime = m.now
	return rateCmd(c.Key(), want)
}
