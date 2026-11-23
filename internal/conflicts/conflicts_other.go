//go:build !linux && !darwin && !windows

package conflicts

func detect() []Finding { return nil }
