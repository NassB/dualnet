//go:build !windows

package wifi

// CurrentSSID is not available outside Windows.
func CurrentSSID() string { return "" }
