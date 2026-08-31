package shadowclock

import (
	"errors"
	"time"
)

var ErrUnsupported = errors.New("cross-process monotonic clock is unavailable")

type Clock interface {
	NowNS() (uint64, error)
}

type System struct{}

func timespecNS(seconds, nanoseconds int64) (uint64, error) {
	if seconds < 0 || nanoseconds < 0 || nanoseconds >= 1_000_000_000 {
		return 0, ErrUnsupported
	}
	sec := uint64(seconds)
	nsec := uint64(nanoseconds)
	if sec > (^uint64(0)-nsec)/1_000_000_000 {
		return 0, ErrUnsupported
	}
	return sec*1_000_000_000 + nsec, nil
}

func Remaining(clock Clock, absoluteNS uint64) (time.Duration, error) {
	now, err := clock.NowNS()
	if err != nil {
		return 0, err
	}
	if now >= absoluteNS {
		return 0, nil
	}
	remaining := absoluteNS - now
	if remaining > uint64((1<<63)-1) {
		return 0, errors.New("absolute monotonic deadline is out of range")
	}
	return time.Duration(remaining), nil
}

func Before(clock Clock, absoluteNS uint64) (bool, error) {
	remaining, err := Remaining(clock, absoluteNS)
	return remaining > 0, err
}
