// Package alerts notifies outside the dashboard: a Windows notification and the
// terminal bell, so you do not have to keep watching.
package alerts

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Mode: which alerts are sent (the -alerts option).
type Mode string

const (
	All  Mode = "all"  // Windows notification and bell
	Bell Mode = "bell" // the bell only
	None Mode = "none"
)

// Modes are the accepted values of -alerts, for help text.
var Modes = []string{string(All), string(Bell), string(None)}

// ParseMode accepts all, bell or none (also no, off); anything else is all.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bell":
		return Bell
	case "none", "no", "off", "":
		return None
	default:
		return All
	}
}

// Send sends the alert according to the mode. It blocks while powershell runs
// (half a second): call it from a tea.Cmd, which runs in its own goroutine.
func Send(m Mode, title, text string) {
	if m == None {
		return
	}
	// The bell goes to stderr: stdout belongs to the UI and writing there at
	// the same time can garble a frame.
	fmt.Fprint(os.Stderr, "\a")
	if m == All && runtime.GOOS == "windows" {
		toast(title, text)
	}
}

// toast uses the Windows notification API from Windows PowerShell 5.1 (it
// ships with Windows; pwsh 7 lacks those classes). The AppID is PowerShell's,
// which is already registered, so nothing has to be installed.
func toast(title, text string) {
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	script := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode('` + esc(title) + `')) > $null
$t.Item(1).AppendChild($x.CreateTextNode('` + esc(text) + `')) > $null
$n = [Windows.UI.Notifications.ToastNotification]::new($x)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($n)`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	hideWindow(cmd)
	cmd.Run()
}
