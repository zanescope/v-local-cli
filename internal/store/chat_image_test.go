package store

import (
	"strings"
	"testing"
)

func TestChatImageStemParsesNestedResourceIdentifier(t *testing.T) {
	wanted := strings.Repeat("a1", 16)
	inner := append([]byte{0x0a, 0x20}, []byte(wanted)...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	actual, err := chatImageStem(packed)
	if err != nil || actual != wanted {
		t.Fatalf("图片资源标识解析异常：actual=%q err=%v", actual, err)
	}
}

func TestChatImageStemRejectsNonHexIdentifier(t *testing.T) {
	inner := append([]byte{0x0a, 0x20}, []byte(strings.Repeat("z", 32))...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	if _, err := chatImageStem(packed); err == nil {
		t.Fatal("非十六进制图片资源标识不应通过")
	}
}
