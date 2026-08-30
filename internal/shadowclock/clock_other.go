//go:build !darwin

package shadowclock

func (System) NowNS() (uint64, error) {
	return 0, ErrUnsupported
}
