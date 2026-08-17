package cryptoutil

import (
	"crypto/aes"
	"encoding/binary"
	"errors"
)

var v1Magic = []byte{0x07, 0x08, 0x56, 0x31, 0x08, 0x07}
var v2Magic = []byte{0x07, 0x08, 0x56, 0x32, 0x08, 0x07}
var v1FixedAESKey = []byte("cfcd208495d565ef")

func hasPrefix(value, prefix []byte) bool {
	if len(value) < len(prefix) {
		return false
	}
	for index := range prefix {
		if value[index] != prefix[index] {
			return false
		}
	}
	return true
}

// ImageFormat 只按已知容器魔数判断格式，不信任扩展名。
func ImageFormat(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "jpg"
	case hasPrefix(data, []byte{0x89, 'P', 'N', 'G'}):
		return "png"
	case hasPrefix(data, []byte("GIF")):
		return "gif"
	case len(data) >= 12 && hasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return "webp"
	case hasPrefix(data, []byte("wxgf")):
		return "wxgf"
	default:
		return "unknown"
	}
}

func decryptECB(data, key []byte) ([]byte, error) {
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("AES 数据没有按 16 字节对齐")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data))
	for offset := 0; offset < len(data); offset += aes.BlockSize {
		block.Decrypt(plain[offset:offset+aes.BlockSize], data[offset:offset+aes.BlockSize])
	}
	return plain, nil
}

func unpadPKCS7(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	size := int(data[len(data)-1])
	if size < 1 || size > aes.BlockSize || size > len(data) {
		return data
	}
	for _, value := range data[len(data)-size:] {
		if int(value) != size {
			return data
		}
	}
	return data[:len(data)-size]
}

// DecryptImageDAT 支持微信 DAT v1/v2 的 AES/XOR 混合布局及 v3 XOR 布局。
func DecryptImageDAT(data []byte, aesKey string, xorKey int) ([]byte, string, error) {
	if xorKey < 0 || xorKey > 255 {
		return nil, "", errors.New("XOR 密钥超出 0..255")
	}
	version := 3
	key := []byte(nil)
	if hasPrefix(data, v1Magic) {
		version = 1
		key = v1FixedAESKey
	} else if hasPrefix(data, v2Magic) {
		version = 2
		key = []byte(aesKey)
	}
	var plain []byte
	if version == 3 {
		plain = make([]byte, len(data))
		for index, value := range data {
			plain[index] = value ^ byte(xorKey)
		}
	} else {
		if len(data) < 15 || len(key) != 16 {
			return nil, "", errors.New("DAT 头或 AES 密钥无效")
		}
		aesSize := int(binary.LittleEndian.Uint32(data[6:10]))
		xorSize := int(binary.LittleEndian.Uint32(data[10:14]))
		payload := data[15:]
		aligned := aesSize + (aes.BlockSize - aesSize%aes.BlockSize)
		if aligned > len(payload) {
			return nil, "", errors.New("DAT AES 区域超过文件长度")
		}
		decrypted, err := decryptECB(payload[:aligned], key)
		if err != nil {
			return nil, "", err
		}
		decrypted = unpadPKCS7(decrypted)
		if aesSize < len(decrypted) {
			decrypted = decrypted[:aesSize]
		}
		remaining := payload[aligned:]
		if xorSize > len(remaining) {
			return nil, "", errors.New("DAT XOR 区域超过文件长度")
		}
		plain = append(plain, decrypted...)
		rawSize := len(remaining) - xorSize
		plain = append(plain, remaining[:rawSize]...)
		for _, value := range remaining[rawSize:] {
			plain = append(plain, value^byte(xorKey))
		}
	}
	format := ImageFormat(plain)
	if format == "unknown" {
		return nil, "", errors.New("解密结果不是已知图片容器")
	}
	return plain, format, nil
}

// ValidateImageCandidate 用真实 DAT 样本验证 AES/XOR 候选。
func ValidateImageCandidate(data []byte, aesKey string, xorKey int) bool {
	_, _, err := DecryptImageDAT(data, aesKey, xorKey)
	return err == nil
}
