//go:build !unix && !windows

package tray

import "os"

func lockFile(*os.File) bool { return true }
