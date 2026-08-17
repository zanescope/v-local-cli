package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccountsUsesExplicitDataRoot(t *testing.T) {
	root := t.TempDir()
	account := filepath.Join(root, "nested", "wxid_owner")
	if err := os.MkdirAll(filepath.Join(account, "db_storage"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", "")
	t.Setenv("V_LOCAL_CLI_DATA_ROOT", root)
	accounts := Accounts()
	if len(accounts) != 1 || accounts[0].Name != "wxid_owner" {
		t.Fatalf("账号发现结果不正确：%+v", accounts)
	}
}

func TestSelectDoesNotGuessAmongMultipleAccounts(t *testing.T) {
	accounts := []Account{{Name: "wxid_one"}, {Name: "wxid_two"}}
	_, selected, ambiguous := Select(accounts, "")
	if selected || !ambiguous {
		t.Fatal("多个账号且未指定时不应自动选择")
	}
	account, selected, ambiguous := Select(accounts, "two")
	if !selected || ambiguous || account.Name != "wxid_two" {
		t.Fatalf("唯一子串没有正确选择：%+v", account)
	}
}

func TestDefaultDataRootsDarwinCoversKnownWeChatContainers(t *testing.T) {
	home := filepath.Join("test-home", "owner")
	container := filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data")
	expected := []string{
		filepath.Join(container, "Documents", "xwechat_files"),
		filepath.Join(container, "Documents", "WeChat Files"),
		filepath.Join(container, "Library", "Application Support", "com.tencent.xinWeChat", "xwechat_files"),
		filepath.Join(container, "Library", "Application Support", "com.tencent.xinWeChat", "WeChat Files"),
	}
	actual := defaultDataRoots("darwin", home)
	if len(actual) != len(expected) {
		t.Fatalf("macOS 微信候选根数量变化：actual=%v expected=%v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("macOS 微信候选根不匹配：index=%d actual=%q expected=%q", index, actual[index], expected[index])
		}
	}
}

func TestDefaultDataRootsUnknownPlatformRemainsTestOnly(t *testing.T) {
	home := filepath.Join("test-home", "owner")
	actual := defaultDataRoots("linux", home)
	expected := filepath.Join(home, ".xwechat")
	if len(actual) != 1 || actual[0] != expected {
		t.Fatalf("非桌面微信平台候选根扩大：actual=%v expected=%q", actual, expected)
	}
}
