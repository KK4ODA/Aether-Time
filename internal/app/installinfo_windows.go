// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package app

import (
	"golang.org/x/sys/windows/registry"

	"github.com/KK4ODA/Aether-Time/internal/version"
)

// noteInstalledVersion keeps Settings → Apps showing the running version after
// the built-in updater has replaced the program installed by the installer.
func noteInstalledVersion() {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`Software\Microsoft\Windows\CurrentVersion\Uninstall\AetherTime`,
		registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return // not installed with the installer
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("DisplayVersion"); err == nil && v != version.Version {
		_ = k.SetStringValue("DisplayVersion", version.Version)
	}
}
