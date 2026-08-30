package shadowclock

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

type fakeClock struct {
	now uint64
	err error
}

func (value fakeClock) NowNS() (uint64, error) {
	return value.now, value.err
}

func TestRemainingUsesOneAbsoluteMonotonicValue(t *testing.T) {
	remaining, err := Remaining(fakeClock{now: 40}, 100)
	if err != nil || remaining != 60*time.Nanosecond {
		t.Fatalf("remaining=%s err=%v", remaining, err)
	}
	remaining, err = Remaining(fakeClock{now: 100}, 100)
	if err != nil || remaining != 0 {
		t.Fatalf("expired remaining=%s err=%v", remaining, err)
	}
	if _, err := Remaining(fakeClock{err: errors.New("clock failed")}, 100); err == nil {
		t.Fatal("clock failure was ignored")
	}
}

func TestSystemClockIsSharedDarwinRawDomain(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin-only clock domain")
	}
	first, err := (System{}).NowNS()
	if err != nil {
		t.Fatal(err)
	}
	second, err := (System{}).NowNS()
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 || second < first {
		t.Fatalf("invalid monotonic sequence: %d then %d", first, second)
	}
}
