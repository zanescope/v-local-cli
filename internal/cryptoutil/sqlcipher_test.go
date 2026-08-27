package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"os"
	"path/filepath"
	"testing"
)

func encryptTestPage(plain, key, salt []byte, pageNumber int) []byte {
	page := make([]byte, SQLCipherPageSize)
	reserve := SQLCipherReserve
	start := 0
	if pageNumber == 1 {
		copy(page[:16], salt)
		start = 16
	}
	iv := []byte("0123456789abcdef")
	copy(page[SQLCipherPageSize-reserve:], iv)
	block, _ := aes.NewCipher(key)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(
		page[start:SQLCipherPageSize-reserve],
		plain[start:SQLCipherPageSize-reserve],
	)
	mac, err := pageHMAC(page, key, salt, uint32(pageNumber))
	if err != nil {
		panic(err)
	}
	copy(page[SQLCipherPageSize-len(mac):], mac)
	return page
}

func TestEncryptedHeaderMustMatchSelectedProfilePageSize(t *testing.T) {
	header := make([]byte, 8)
	header[0], header[1] = 0x20, 0x00
	header[4], header[5], header[6], header[7] = SQLCipherReserve, 64, 32, 32
	if headerOK(header, SQLCipherReserve) {
		t.Fatal("8 KiB encrypted header was accepted by the 4 KiB profile")
	}
}

func TestDecryptSQLCipherSnapshotMainDatabase(t *testing.T) {
	passphrase := make([]byte, 32)
	for index := range passphrase {
		passphrase[index] = byte(index + 1)
	}
	salt := []byte("abcdefghijklmnop")
	key, err := pbkdf2.Key(sha512.New, string(passphrase), salt, SQLCipherKDFRuns, 32)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, SQLCipherPageSize*2)
	copy(plain, sqliteHeader)
	plain[16], plain[17] = 0x10, 0x00
	plain[18], plain[19], plain[20] = 1, 1, SQLCipherReserve
	plain[21], plain[22], plain[23] = 64, 32, 32
	plain[SQLCipherPageSize+123] = 0x7f
	encrypted := append(
		encryptTestPage(plain[:SQLCipherPageSize], key, salt, 1),
		encryptTestPage(plain[SQLCipherPageSize:], key, salt, 2)...,
	)
	keyHex := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

	root := t.TempDir()
	source := filepath.Join(root, "encrypted.db")
	if err := os.WriteFile(source, encrypted, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "plain.db")
	info, size, err := DecryptSQLCipherSnapshotFiles(source, "", destination, keyHex)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != "absent" || size != int64(len(encrypted)) {
		t.Fatalf("解密结果异常：wal=%+v size=%d", info, size)
	}
	if string(got[:len(sqliteHeader)]) != string(sqliteHeader) {
		t.Fatal("解密后的主库缺少 SQLite 明文头")
	}
	if got[SQLCipherPageSize+123] != 0x7f {
		t.Fatalf("第二页正文解密错误：byte=%x", got[SQLCipherPageSize+123])
	}
}

// 错误候选必须在首页验真处被拒，且不得留下半个快照文件。
func TestDecryptRejectsWrongCandidateKey(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "encrypted.db")
	if err := os.WriteFile(source, make([]byte, SQLCipherPageSize), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "plain.db")
	wrongKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, _, err := DecryptSQLCipherSnapshotFiles(source, "", destination, wrongKey); err == nil {
		t.Fatal("错误候选不应通过验真")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("验真失败后仍留下了输出文件")
	}
}
