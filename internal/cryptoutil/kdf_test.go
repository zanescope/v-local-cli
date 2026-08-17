package cryptoutil

import (
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

// 密钥派生一旦改变，所有已保存的候选密钥都会失效、现存快照无法重建。
// 这个固定向量把 KDF 的算法、哈希、轮数和输出长度一起钉死：手写实现换成
// crypto/pbkdf2 时逐字节比对过，此处的值即当时的输出。
func TestKeyDerivationVectorIsStable(t *testing.T) {
	const want = "21e434ed21eebe7296f28aef021407d560846c60e61708abcc2396f948e225cb"
	got, err := pbkdf2.Key(sha512.New, "v-local-cli-kdf-vector", []byte("0123456789abcdef"), SQLCipherKDFRuns, 32)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != want {
		t.Fatalf("密钥派生结果已改变，现存快照与已保存候选将全部失效\n  期望 %s\n  实际 %s",
			want, hex.EncodeToString(got))
	}
	if SQLCipherKDFRuns != 256000 {
		t.Fatalf("KDF 轮数被改成 %d；改动会使所有已保存候选失效", SQLCipherKDFRuns)
	}
}
