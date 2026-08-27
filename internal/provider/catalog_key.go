package provider

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const catalogKeyFileName = "catalog-key-v1"

func clearCatalogKeyBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func readCatalogKey(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	unsafePath, safetyErr := providerPathIsLinkOrReparse(path, info.Mode())
	if safetyErr != nil || unsafePath || !info.Mode().IsRegular() || info.Size() > 128 ||
		runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("catalog key 文件无效")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	defer clearCatalogKeyBytes(payload)
	value := strings.TrimSpace(string(payload))
	decoded, err := hex.DecodeString(value)
	defer clearCatalogKeyBytes(decoded)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("catalog key 内容无效")
	}
	return strings.ToLower(value), nil
}

func readCatalogKeyAfterConcurrentCreate(path string) (string, error) {
	var last error
	for attempt := 0; attempt < 8; attempt++ {
		value, err := readCatalogKey(path)
		if err == nil || os.IsNotExist(err) {
			return value, err
		}
		last = err
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	return "", last
}

func catalogKeyForPrivateRoot(privateRoot string) (string, error) {
	if strings.TrimSpace(privateRoot) == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", errors.New("无法生成一次性 catalog key")
		}
		defer clearCatalogKeyBytes(key)
		return hex.EncodeToString(key), nil
	}
	absolute, err := filepath.Abs(privateRoot)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(absolute)
	unsafeRoot := false
	if err == nil {
		unsafeRoot, err = providerPathIsLinkOrReparse(absolute, rootInfo.Mode())
	}
	if err != nil || !rootInfo.IsDir() || unsafeRoot {
		return "", errors.New("acquisition 私有目录无效")
	}
	path := filepath.Join(absolute, catalogKeyFileName)
	if value, err := readCatalogKeyAfterConcurrentCreate(path); err == nil {
		return value, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", errors.New("无法生成机器 catalog key")
	}
	defer clearCatalogKeyBytes(key)
	value := hex.EncodeToString(key)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return readCatalogKeyAfterConcurrentCreate(path)
	}
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.WriteString(value + "\n"); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remove = false
	return value, nil
}
