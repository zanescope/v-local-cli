//go:build darwin

package shadowclock

import "golang.org/x/sys/unix"

func (System) NowNS() (uint64, error) {
	var value unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &value); err != nil {
		return 0, err
	}
	if value.Sec < 0 || value.Nsec < 0 {
		return 0, ErrUnsupported
	}
	return uint64(value.Sec)*1_000_000_000 + uint64(value.Nsec), nil
}
