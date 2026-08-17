package snapshot

import (
	"encoding/binary"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/provider"
	"github.com/zanescope/v-local-cli/internal/state"
)

const maxMediaSamples = 20000
const maxMediaBytes = 128 * 1024 * 1024

var mediaV1Magic = []byte{0x07, 0x08, 0x56, 0x31, 0x08, 0x07}
var mediaV2Magic = []byte{0x07, 0x08, 0x56, 0x32, 0x08, 0x07}

func prefixMatches(value, prefix []byte) bool {
	return len(value) >= len(prefix) && string(value[:len(prefix)]) == string(prefix)
}

// ValidateMedia 为 AES 与 XOR 分别寻找真实 DAT 样本证据。
func ValidateMedia(accountPath string, keys *provider.ImageKeys) state.MediaSummary {
	result := state.MediaSummary{Status: "missing_candidate"}
	if keys == nil {
		return result
	}
	result.Status = "no_sample"
	var bytesRead int64
	_ = filepath.WalkDir(accountPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if result.SamplesScanned >= maxMediaSamples || bytesRead >= maxMediaBytes || (result.AESVerified && result.XORVerified) {
			return fs.SkipAll
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dat") {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil || info.Size() < 4 || info.Size() > 64*1024*1024 {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		header := make([]byte, 64)
		read, _ := io.ReadFull(file, header)
		_ = file.Close()
		header = header[:read]
		result.SamplesScanned++
		data := header
		isV2 := prefixMatches(header, mediaV2Magic)
		isV1 := prefixMatches(header, mediaV1Magic)
		if isV1 || isV2 {
			if bytesRead+info.Size() > maxMediaBytes {
				return nil
			}
			full, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			data = full
			bytesRead += int64(len(full))
		}
		if !cryptoutil.ValidateImageCandidate(data, keys.AES, keys.XOR) {
			return nil
		}
		result.SamplesValid++
		switch {
		case isV2:
			result.AESVerified = true
			if len(data) >= 14 && binary.LittleEndian.Uint32(data[10:14]) > 0 {
				result.XORVerified = true
			}
		case isV1:
			if len(data) >= 14 && binary.LittleEndian.Uint32(data[10:14]) > 0 {
				result.XORVerified = true
			}
		default:
			result.XORVerified = true
		}
		return nil
	})
	switch {
	case result.AESVerified && result.XORVerified:
		result.Status = "verified"
	case result.AESVerified || result.XORVerified:
		result.Status = "partial"
	case result.SamplesScanned > 0:
		result.Status = "rejected"
	}
	return result
}
