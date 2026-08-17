package platform

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const maxWalkEntries = 50000

type Account struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	DBDir string `json:"db_dir"`
}

func Accounts() []Account {
	if explicit := os.Getenv("V_LOCAL_CLI_ACCOUNT_DIR"); explicit != "" {
		if account, ok := accountAt(explicit); ok {
			return []Account{account}
		}
		return nil
	}
	seen := map[string]bool{}
	var accounts []Account
	for _, root := range dataRoots() {
		entries := 0
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			entries++
			if entries > maxWalkEntries {
				return fs.SkipAll
			}
			if !entry.IsDir() || !strings.EqualFold(entry.Name(), "db_storage") {
				return nil
			}
			parent := filepath.Dir(path)
			identity := strings.ToLower(parent)
			if !seen[identity] {
				seen[identity] = true
				accounts = append(accounts, Account{
					Name: filepath.Base(parent), Path: parent, DBDir: path,
				})
			}
			return fs.SkipDir
		})
	}
	sort.Slice(accounts, func(left, right int) bool {
		return strings.ToLower(accounts[left].Name) < strings.ToLower(accounts[right].Name)
	})
	return accounts
}

func Select(accounts []Account, selector string) (Account, bool, bool) {
	if selector == "" {
		if len(accounts) == 1 {
			return accounts[0], true, false
		}
		return Account{}, false, len(accounts) > 1
	}
	for _, account := range accounts {
		if strings.EqualFold(account.Name, selector) {
			return account, true, false
		}
	}
	var matches []Account
	needle := strings.ToLower(selector)
	for _, account := range accounts {
		if strings.Contains(strings.ToLower(account.Name), needle) {
			matches = append(matches, account)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, false
	}
	return Account{}, false, len(matches) > 1
}

func accountAt(path string) (Account, bool) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Account{}, false
	}
	db := filepath.Join(absolute, "db_storage")
	info, err := os.Stat(db)
	if err != nil || !info.IsDir() {
		return Account{}, false
	}
	return Account{Name: filepath.Base(absolute), Path: absolute, DBDir: db}, true
}

func dataRoots() []string {
	if explicit := os.Getenv("V_LOCAL_CLI_DATA_ROOT"); explicit != "" {
		return []string{explicit}
	}
	home, _ := os.UserHomeDir()
	return defaultDataRoots(runtime.GOOS, home)
}

func defaultDataRoots(goos, home string) []string {
	if goos == "windows" {
		return []string{
			filepath.Join(home, "Documents", "xwechat_files"),
			filepath.Join(home, "Documents", "WeChat Files"),
			filepath.Join(home, "AppData", "Roaming", "Tencent", "xwechat", "xwechat_files"),
			filepath.Join(home, "AppData", "Roaming", "Tencent", "WeChat", "xwechat_files"),
		}
	}
	if goos == "darwin" {
		container := filepath.Join(home, "Library", "Containers", "com.tencent.xinWeChat", "Data")
		return []string{
			filepath.Join(container, "Documents", "xwechat_files"),
			filepath.Join(container, "Documents", "WeChat Files"),
			filepath.Join(container, "Library", "Application Support", "com.tencent.xinWeChat", "xwechat_files"),
			filepath.Join(container, "Library", "Application Support", "com.tencent.xinWeChat", "WeChat Files"),
		}
	}
	return []string{filepath.Join(home, ".xwechat")}
}
