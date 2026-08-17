package snapshot

import (
	"crypto/aes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/zanescope/v-local-cli/internal/provider"
)

func writeV2Sample(t *testing.T, path, key string) {
	t.Helper()
	plain := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4}
	blockData := make([]byte, aes.BlockSize)
	copy(blockData, plain)
	for index := len(plain); index < len(blockData); index++ {
		blockData[index] = byte(aes.BlockSize - len(plain))
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, aes.BlockSize)
	block.Encrypt(ciphertext, blockData)
	data := make([]byte, 15)
	copy(data, mediaV2Magic)
	binary.LittleEndian.PutUint32(data[6:10], uint32(len(plain)))
	data = append(data, ciphertext...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMediaRequiresAESAndXOREvidence(t *testing.T) {
	root := t.TempDir()
	key := "0123456789abcdef"
	writeV2Sample(t, filepath.Join(root, "aes.dat"), key)
	xorPlain := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}
	xorCipher := make([]byte, len(xorPlain))
	for index, value := range xorPlain {
		xorCipher[index] = value ^ 0x5a
	}
	if err := os.WriteFile(filepath.Join(root, "xor.dat"), xorCipher, 0o600); err != nil {
		t.Fatal(err)
	}
	result := ValidateMedia(root, &provider.ImageKeys{AES: key, XOR: 0x5a})
	if result.Status != "verified" || !result.AESVerified || !result.XORVerified {
		t.Fatalf("图片候选验真结果异常：%+v", result)
	}
}
