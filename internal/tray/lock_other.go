// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix && !windows

package tray

import "os"

func lockFile(*os.File) bool { return true }
