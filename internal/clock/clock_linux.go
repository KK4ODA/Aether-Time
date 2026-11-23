//go:build linux

package clock

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	adjSetOffset = 0x0100 // ADJ_SETOFFSET: add Time to the clock atomically
	capSysTime   = 25     // CAP_SYS_TIME
)

func stepClock(d time.Duration) error {
	// ADJ_SETOFFSET adds the offset inside the kernel, so there is no race
	// between reading the time and writing it back.
	// NsecToTimeval normalizes negative offsets (usec in [0, 1e6)) and uses
	// the right field widths on 32-bit ARM as well as 64-bit systems.
	tx := setOffsetTimex(d)
	if _, err := unix.Adjtimex(&tx); err == nil {
		return nil
	} else if errors.Is(err, unix.EPERM) {
		return ErrNotPermitted
	}
	// Old kernels: fall back to read-modify-write.
	ts := unix.NsecToTimespec(time.Now().Add(d).UnixNano())
	if err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts); err != nil {
		if errors.Is(err, unix.EPERM) {
			return ErrNotPermitted
		}
		return err
	}
	return nil
}

// setOffsetTimex builds the adjtimex request that adds d to the clock.
func setOffsetTimex(d time.Duration) unix.Timex {
	return unix.Timex{Modes: adjSetOffset, Time: unix.NsecToTimeval(d.Nanoseconds())}
}

func canAdjust() (bool, string) {
	if os.Geteuid() == 0 {
		return true, ""
	}
	if hasCapability(capSysTime) {
		return true, ""
	}
	return false, "run as root, install the service (sudo aether-time service install), or grant CAP_SYS_TIME (sudo setcap cap_sys_time+ep aether-time)"
}

func hasCapability(bit uint) bool {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		if err != nil {
			return false
		}
		return v&(1<<bit) != 0
	}
	return false
}
