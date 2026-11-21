package doctor

import "golang.org/x/sys/windows"

// usbDevices lists the present devices enumerated by the USB bus.
func usbDevices() []USBDevice {
	set, err := windows.SetupDiGetClassDevsEx(nil, "USB", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil
	}
	defer set.Close()
	var out []USBDevice
	for i := 0; ; i++ {
		data, err := set.EnumDeviceInfo(i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			continue
		}
		var d USBDevice
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_HARDWAREID); err == nil {
			d.HardwareIDs, _ = v.([]string)
		}
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_CLASS); err == nil {
			d.Class, _ = v.(string)
		}
		if v, err := set.DeviceRegistryProperty(data, windows.SPDRP_SERVICE); err == nil {
			d.Service, _ = v.(string)
		}
		out = append(out, d)
	}
	return out
}

func iPhoneProbe() IPhoneState { return iPhoneState(usbDevices()) }
