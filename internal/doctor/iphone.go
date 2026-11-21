package doctor

import "strings"

// USBDevice is one present Plug and Play device, as much as the iPhone check needs.
type USBDevice struct {
	HardwareIDs []string // e.g. USB\VID_05AC&PID_12A8&REV_0F04
	Class       string   // Windows device class: "Net", "USB", "WPD"…
	Service     string   // driver service, "" when no driver is bound
}

// IPhoneState sums up what Windows sees of an iPhone plugged in over USB.
type IPhoneState struct {
	Plugged     bool // an iPhone/iPad is connected
	AppleDriver bool // Apple's USB driver (iTunes / Apple Devices) is bound to it
	Adapter     bool // Windows created a network adapter for it (USB tethering possible)
}

// iPhoneState reads the device list. iPhones, iPads and iPods touch use Apple's
// vendor ID 05AC with product IDs 12xx (Apple keyboards and mice use others).
// Apple's drivers are the services usbaapl/usbaapl64 (USB) and netaapl/netaapl64
// (network); without them Windows only sees the phone as a camera/media device.
func iPhoneState(devs []USBDevice) IPhoneState {
	var s IPhoneState
	for _, d := range devs {
		apple, phone := false, false
		for _, id := range d.HardwareIDs {
			id = strings.ToUpper(id)
			if strings.Contains(id, "VID_05AC&") {
				apple = true
				if strings.Contains(id, "VID_05AC&PID_12") {
					phone = true
				}
			}
		}
		if !apple {
			continue
		}
		svc := strings.ToLower(d.Service)
		if phone {
			s.Plugged = true
		}
		if strings.Contains(svc, "aapl") {
			s.AppleDriver = true
		}
		if strings.EqualFold(d.Class, "Net") || strings.HasPrefix(svc, "netaapl") {
			s.Adapter = true
		}
	}
	return s
}
