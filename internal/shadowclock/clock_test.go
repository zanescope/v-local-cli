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

func TestTimespecNSRejectsInvalidOrOverflowingValues(t *testing.T) {
	value, err := timespecNS(12, 345)
	if err != nil || value != 12_000_000_345 {
		t.Fatalf("value=%d err=%v", value, err)
	}
	for _, input := range [][2]int64{{-1, 0}, {0, -1}, {0, 1_000_000_000}, {int64(^uint64(0) / 1_000_000_000), 999_999_999}} {
		if _, err := timespecNS(input[0], input[1]); err == nil {
			t.Fatalf("invalid timespec was accepted: %+v", input)
		}
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
