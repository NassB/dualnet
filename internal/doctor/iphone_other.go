//go:build !windows

package doctor

// iPhoneProbe is nil outside Windows: macOS and Linux bring their own drivers.
var iPhoneProbe func() IPhoneState
