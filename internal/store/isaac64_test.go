package store

import (
	"encoding/binary"
	"testing"
)

func TestISAAC64MatchesReferenceVector(t *testing.T) {
	state := newISAAC64(0)
	expected := []uint64{
		0x9D39247E33776D41,
		0x2AF7398005AAA5C7,
		0x44DB015024623547,
		0x9C15F73E62A76AE2,
	}
	for index, wanted := range expected {
		if actual := state.next(); actual != wanted {
			t.Fatalf("第 %d 个结果不一致：got=%016x want=%016x", index, actual, wanted)
		}
	}
}

func TestISAAC64KeystreamUsesBigEndianAndSupportsTail(t *testing.T) {
	stream := isaac64Keystream(0, 11)
	if len(stream) != 11 {
		t.Fatalf("密钥流长度异常：%d", len(stream))
	}
	if actual := binary.BigEndian.Uint64(stream[:8]); actual != 0x9D39247E33776D41 {
		t.Fatalf("首个大端序块异常：%016x", actual)
	}
	var tail [8]byte
	binary.BigEndian.PutUint64(tail[:], 0x2AF7398005AAA5C7)
	for index := 0; index < 3; index++ {
		if stream[8+index] != tail[index] {
			t.Fatalf("尾部第 %d 字节不一致", index)
		}
	}
}
