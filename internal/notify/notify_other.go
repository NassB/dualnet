//go:build !windows

package notify

import "errors"

// Send is not available outside Windows.
func Send(title, body string) error { return errors.New("notifications: Windows only") }
