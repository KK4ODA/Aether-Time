// SPDX-License-Identifier: GPL-3.0-or-later

// Package version holds build metadata injected at link time.
package version

import (
	"fmt"
	"runtime"
)

// These are overridden with -ldflags "-X github.com/KK4ODA/Aether-Time/internal/version.Version=1.2.3 ...".
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// AppName is the product name shown in the UI and logs.
const AppName = "Aether Time"

// BinaryName is the executable name (without extension).
const BinaryName = "aether-time"

// Notice is the license notice shown by "aether-time version".
const Notice = `Copyright (C) 2026 KK4ODA and contributors.
License GPL-3.0-or-later <https://www.gnu.org/licenses/gpl-3.0.html>.
This is free software: you are free to change and redistribute it.
There is NO WARRANTY, to the extent permitted by law.`

// String returns a human readable version line.
func String() string {
	return fmt.Sprintf("%s %s (%s, %s) %s/%s", AppName, Version, Commit, Date, runtime.GOOS, runtime.GOARCH)
}
