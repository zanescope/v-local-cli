//go:build darwin

package shadowclock

import "golang.org/x/sys/unix"

func (System) NowNS() (uint64, error) {
	var value unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &value); err != nil {
		return 0, err
	}
	return timespecNS(value.Sec, value.Nsec)
}
