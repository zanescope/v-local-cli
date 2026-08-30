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
