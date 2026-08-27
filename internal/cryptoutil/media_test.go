package cryptoutil

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func syntheticImage(t *testing.T, format string) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			canvas.Set(x, y, color.RGBA{R: uint8(40 + x*50), G: uint8(80 + y*50), B: 180, A: 255})
		}
	}
	var output bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&output, canvas)
	} else {
		err = jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encryptV2DAT(t *testing.T, plain []byte, aesKey string, xorKey byte, aesSize int) []byte {
	t.Helper()
	if aesSize <= 0 || aesSize > len(plain) {
		t.Fatalf("invalid synthetic AES size %d for %d-byte image", aesSize, len(plain))
	}
	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		t.Fatal(err)
	}
	aesPlain := append([]byte(nil), plain[:aesSize]...)
	padding := aes.BlockSize - len(aesPlain)%aes.BlockSize
	if padding == 0 {
		padding = aes.BlockSize
	}
	aesPlain = append(aesPlain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	aesCipher := make([]byte, len(aesPlain))
	for offset := 0; offset < len(aesPlain); offset += aes.BlockSize {
		block.Encrypt(aesCipher[offset:offset+aes.BlockSize], aesPlain[offset:offset+aes.BlockSize])
	}
	xorPlain := plain[aesSize:]
	xorCipher := make([]byte, len(xorPlain))
	for index, value := range xorPlain {
		xorCipher[index] = value ^ xorKey
	}
	header := make([]byte, 15)
	copy(header, v2Magic)
	binary.LittleEndian.PutUint32(header[6:10], uint32(aesSize))
	binary.LittleEndian.PutUint32(header[10:14], uint32(len(xorCipher)))
	return append(header, append(aesCipher, xorCipher...)...)
}

func TestDecryptImageDATV3(t *testing.T) {
	plain := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}
	ciphertext := make([]byte, len(plain))
	for index, value := range plain {
		ciphertext[index] = value ^ 0x5a
	}
	got, format, err := DecryptImageDAT(ciphertext, "", 0x5a)
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpg" || string(got) != string(plain) {
		t.Fatalf("解密结果异常：format=%s data=%x", format, got)
	}
}

func TestDecryptImageDATV1UsesFixedAESKey(t *testing.T) {
	plain := syntheticImage(t, "png")
	data := encryptV2DAT(t, plain, string(v1FixedAESKey), 0x5a, 32)
	copy(data, v1Magic)

	got, format, err := DecryptImageDAT(data, "账号 AES 参数不应参与 V1 解密", 0x5a)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || !bytes.Equal(got, plain) {
		t.Fatalf("V1 固定 AES key 往返异常：format=%s got=%d want=%d", format, len(got), len(plain))
	}
}

func TestDecryptImageDATRejectsWrongXOR(t *testing.T) {
	plain := syntheticImage(t, "jpg")
	data := encryptV2DAT(t, plain, "0123456789abcdef", 0x5a, 2)
	if _, _, err := DecryptImageDAT(data, "0123456789abcdef", 0x5b); err == nil {
		t.Fatal("预期错误 XOR 候选被拒绝")
	}
	if _, _, err := DecryptImageDAT([]byte{1, 2, 3, 4}, "", 1); err == nil {
		t.Fatal("预期未知容器校验失败")
	}
}

func TestDecryptImageDATV2RoundTripPNGAndExactAESBlock(t *testing.T) {
	plain := syntheticImage(t, "png")
	key := "0123456789abcdef"
	data := encryptV2DAT(t, plain, key, 0x5a, 32)
	got, format, err := DecryptImageDAT(data, key, 0x5a)
	if err != nil {
		t.Fatal(err)
	}
	if format != "png" || !bytes.Equal(got, plain) {
		t.Fatalf("V2 round-trip mismatch: format=%s got=%d want=%d", format, len(got), len(plain))
	}
	if _, _, err := DecryptImageDAT(data, "fedcba9876543210", 0x5a); err == nil {
		t.Fatal("预期错误 AES 候选被拒绝")
	}
}

func TestDecryptImageDATRejectsTruncatedAndInvalidSizes(t *testing.T) {
	plain := syntheticImage(t, "jpg")
	key := "0123456789abcdef"
	data := encryptV2DAT(t, plain, key, 0x5a, 2)
	for name, input := range map[string][]byte{
		"truncated":     data[:len(data)-1],
		"short-payload": data[:15],
	} {
		if _, _, err := DecryptImageDAT(input, key, 0x5a); err == nil {
			t.Errorf("%s DAT should be rejected", name)
		}
	}
	invalidSize := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(invalidSize[6:10], uint32(len(plain)+100))
	if _, _, err := DecryptImageDAT(invalidSize, key, 0x5a); err == nil {
		t.Fatal("invalid AES size should be rejected")
	}
}
