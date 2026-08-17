package cryptoutil

import (
	"bytes"
	"testing"
)

// TestSolveKeyRawEncKeyPathUsesCandidateDirectly 验证候选本身就是派生后的 enc_key
// （key-provider 多数路径的输出）时，solveKey 直接用它解开首页并原样返回，走 raw-key
// 快路径——无需先跑 256000 轮 PBKDF2。口令输入的派生路径已由 sqlcipher_test.go 覆盖。
func TestSolveKeyRawEncKeyPathUsesCandidateDirectly(t *testing.T) {
	encKey := make([]byte, 32)
	for index := range encKey {
		encKey[index] = byte(0xA0 + index)
	}
	salt := []byte("abcdefghijklmnop")
	plain := make([]byte, SQLCipherPageSize)
	copy(plain, sqliteHeader)
	plain[16], plain[17] = 0x10, 0x00
	plain[18], plain[19], plain[20] = 1, 1, SQLCipherReserve
	plain[21], plain[22], plain[23] = 64, 32, 32
	page := encryptTestPage(plain, encKey, salt, 1)

	solved, err := solveKey(page, encKey)
	if err != nil {
		t.Fatalf("raw enc_key 应能解开首页：%v", err)
	}
	if !bytes.Equal(solved.key, encKey) {
		t.Fatalf("solveKey 应直接返回候选 enc_key，得到 %x", solved.key)
	}
	if solved.reserve != SQLCipherReserve {
		t.Fatalf("reserve = %d，期望 %d", solved.reserve, SQLCipherReserve)
	}
}
