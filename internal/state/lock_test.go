package state

import (
	"errors"
	"testing"
)

func TestAccountLockRejectsConcurrentTransactionAndReleases(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_HOME", testHome(t))
	accountID := AccountID(t.TempDir())
	first, err := AcquireAccountLock(accountID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AcquireAccountLock(accountID)
	if second != nil || !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("并发账号事务未被拒绝：lock=%v err=%v", second, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireAccountLock(accountID)
	if err != nil {
		t.Fatalf("释放后无法重新取得账号锁：%v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountLockRejectsInvalidAccountID(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_HOME", testHome(t))
	if lock, err := AcquireAccountLock("../outside"); lock != nil || err == nil {
		t.Fatalf("无效账号标识未被拒绝：lock=%v err=%v", lock, err)
	}
}
