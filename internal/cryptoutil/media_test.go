package cryptoutil

import "testing"

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

func TestDecryptImageDATRejectsWrongXOR(t *testing.T) {
	if _, _, err := DecryptImageDAT([]byte{1, 2, 3, 4}, "", 1); err == nil {
		t.Fatal("预期未知容器校验失败")
	}
}
