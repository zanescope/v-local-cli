package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	SQLCipherPageSize = 4096
	SQLCipherReserve  = 80
	SQLCipherKDFRuns  = 256000
)

var sqliteHeader = []byte("SQLite format 3\x00")
var reserveCandidates = []int{80, 48, 64}
var validPageSizes = map[uint16]bool{
	512: true, 1024: true, 2048: true, 4096: true, 8192: true,
	16384: true, 32768: true, 65535: true,
}

type WALInfo struct {
	Present         bool   `json:"present"`
	Status          string `json:"status"`
	ValidFrames     int    `json:"valid_frames"`
	CommittedFrames int    `json:"committed_frames"`
	DatabasePages   uint32 `json:"database_pages,omitempty"`
	AppliedPages    int    `json:"applied_pages"`
	TrailingBytes   int    `json:"trailing_bytes"`
}

type solvedKey struct {
	key     []byte
	reserve int
}

func normalizeHexKey(value string) ([]byte, error) {
	normalized := strings.NewReplacer(" ", "", ":", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(strings.ToLower(normalized), "x'") && strings.HasSuffix(normalized, "'") {
		normalized = normalized[2 : len(normalized)-1]
	}
	if len(normalized) == 96 {
		normalized = normalized[:64]
	}
	if len(normalized) != 64 {
		return nil, errors.New("数据库密钥必须是 32 字节十六进制值")
	}
	key, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, errors.New("数据库密钥包含非十六进制字符")
	}
	return key, nil
}

func decryptCBC(ciphertext, key, iv []byte) ([]byte, error) {
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度不是 AES 块大小的整数倍")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	return plain, nil
}

func pageOnePlain(page, key []byte, reserve int) ([]byte, error) {
	if len(page) < SQLCipherPageSize || reserve < 16 || SQLCipherPageSize-reserve <= 16 {
		return nil, errors.New("SQLCipher 首页长度或 reserve 无效")
	}
	return decryptCBC(
		page[16:SQLCipherPageSize-reserve], key,
		page[SQLCipherPageSize-reserve:SQLCipherPageSize-reserve+16],
	)
}

func headerOK(plain []byte, reserve int) bool {
	if len(plain) < 8 {
		return false
	}
	pageSize := binary.BigEndian.Uint16(plain[:2])
	if pageSize == 1 {
		pageSize = 65535
	}
	return validPageSizes[pageSize] && int(plain[4]) == reserve &&
		plain[5] == 64 && plain[6] == 32 && plain[7] == 32
}

func solveKey(data, passphrase []byte) (*solvedKey, error) {
	if len(data) < SQLCipherPageSize {
		return nil, errors.New("数据库首页不足 4096 字节")
	}
	// 两种布局都要试：有版本直接把 32 字节原始密钥当页密钥用，也有版本用首页盐做
	// PBKDF2 派生。先按原始密钥试——只是几次 AES，几乎免费；key-provider 的多数路径
	// 本就直接产出派生后的 enc_key，命中即可跳过 256000 轮 PBKDF2。
	// normalizeHexKey 已保证 passphrase 恒为 32 字节，无需再判长度。
	if solved := trySQLCipherPageKey(data, passphrase); solved != nil {
		return solved, nil
	}
	// 原始密钥不命中：候选是口令，派生 enc_key 再试。对单个已知候选串行派生一次、无需
	// 中途取消，标准库 crypto/pbkdf2 已够用。（只有在大量候选上并发尝试、命中即取消的
	// 场景，才需要标准库不提供的取消钩子而改用手写实现；判断依据始终是调用场景需不需要
	// 取消，而非是否手写。）
	derived, err := pbkdf2.Key(sha512.New, string(passphrase), data[:16], SQLCipherKDFRuns, 32)
	if err != nil {
		return nil, err
	}
	if solved := trySQLCipherPageKey(data, derived); solved != nil {
		return solved, nil
	}
	return nil, errors.New("候选密钥未通过 SQLCipher 首页验真")
}

// trySQLCipherPageKey 用给定的 32 字节页密钥逐个 reserve 布局尝试解开首页，命中则返回
// 对应布局的 solvedKey，否则返回 nil。
func trySQLCipherPageKey(data, key []byte) *solvedKey {
	for _, reserve := range reserveCandidates {
		plain, err := pageOnePlain(data[:SQLCipherPageSize], key, reserve)
		if err == nil && headerOK(plain, reserve) {
			return &solvedKey{key: append([]byte(nil), key...), reserve: reserve}
		}
	}
	return nil
}

func decryptPage(page, key []byte, reserve int, pageNumber uint32) ([]byte, error) {
	if len(page) != SQLCipherPageSize {
		return nil, fmt.Errorf("页 %d 长度不是 %d", pageNumber, SQLCipherPageSize)
	}
	offset := 0
	if pageNumber == 1 {
		offset = 16
	}
	plain, err := decryptCBC(
		page[offset:SQLCipherPageSize-reserve], key,
		page[SQLCipherPageSize-reserve:SQLCipherPageSize-reserve+16],
	)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, SQLCipherPageSize)
	if pageNumber == 1 {
		result = append(result, sqliteHeader...)
	}
	result = append(result, plain...)
	result = append(result, page[SQLCipherPageSize-reserve:]...)
	return result, nil
}

func walChecksum(data []byte, bigEndian bool, first, second uint32) (uint32, uint32, error) {
	if len(data)%8 != 0 {
		return 0, 0, errors.New("WAL 校验输入长度无效")
	}
	var order binary.ByteOrder = binary.LittleEndian
	if bigEndian {
		order = binary.BigEndian
	}
	for offset := 0; offset < len(data); offset += 8 {
		first += order.Uint32(data[offset:offset+4]) + second
		second += order.Uint32(data[offset+4:offset+8]) + first
	}
	return first, second, nil
}
