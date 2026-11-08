package notify

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/NassB/dualnet/internal/winexec"
)

// The text never goes into the script: it is read from environment variables,
// so a link name containing quotes or "$(…)" cannot be interpreted as code.
// The toast is shown under Windows PowerShell's registered app identity.
const toastScript = `$ErrorActionPreference='Stop'; ` +
	`[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]; ` +
	`[void][Windows.UI.Notifications.ToastTemplateType,Windows.UI.Notifications,ContentType=WindowsRuntime]; ` +
	`[void][Windows.UI.Notifications.ToastNotification,Windows.UI.Notifications,ContentType=WindowsRuntime]; ` +
	`[void][Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom.XmlDocument,ContentType=WindowsRuntime]; ` +
	`$x=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); ` +
	`$t=$x.GetElementsByTagName('text'); ` +
	`[void]$t.Item(0).AppendChild($x.CreateTextNode($env:DUALNET_TITLE)); ` +
	`[void]$t.Item(1).AppendChild($x.CreateTextNode($env:DUALNET_BODY)); ` +
	`$n=[Windows.UI.Notifications.ToastNotification]::new($x); ` +
	`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($n)`

func command(title, body string) *exec.Cmd {
	c := winexec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", toastScript)
	c.Env = append(os.Environ(), "DUALNET_TITLE="+title, "DUALNET_BODY="+body)
	return c
}

// Send shows a Windows notification.
func Send(title, body string) error {
	out, err := command(title, body).CombinedOutput()
	if err != nil {
		return fmt.Errorf("notification: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
