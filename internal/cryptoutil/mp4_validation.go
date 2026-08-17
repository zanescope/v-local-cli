package cryptoutil

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

type MP4Validation struct {
	Method     string
	MajorBrand string
	BoxCount   int
}

func validMP4BoxType(value []byte) bool {
	if len(value) != 4 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func readMP4Bytes(reader io.ReaderAt, offset int64, size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(io.NewSectionReader(reader, offset, int64(size)), value); err != nil {
		return nil, err
	}
	return value, nil
}

// ValidateMP4Reader 流式验证完整的 ISO BMFF 顶层盒边界及播放所需的核心盒。
func ValidateMP4Reader(reader io.ReaderAt, totalBytes int64) (MP4Validation, error) {
	if reader == nil || totalBytes < 24 {
		return MP4Validation{}, errors.New("MP4 数据过短")
	}
	offset := int64(0)
	boxCount := 0
	foundMovie := false
	foundMediaData := false
	majorBrand := ""
	for offset < totalBytes {
		remaining := totalBytes - offset
		if remaining < 8 {
			return MP4Validation{}, errors.New("MP4 顶层盒头不完整")
		}
		header, err := readMP4Bytes(reader, offset, 8)
		if err != nil {
			return MP4Validation{}, errors.New("MP4 顶层盒头读取失败")
		}
		size := uint64(binary.BigEndian.Uint32(header[:4]))
		boxType := header[4:8]
		if !validMP4BoxType(boxType) {
			return MP4Validation{}, errors.New("MP4 顶层盒类型无效")
		}
		headerBytes := uint64(8)
		if size == 1 {
			if remaining < 16 {
				return MP4Validation{}, errors.New("MP4 扩展盒头不完整")
			}
			extended, err := readMP4Bytes(reader, offset+8, 8)
			if err != nil {
				return MP4Validation{}, errors.New("MP4 扩展盒头读取失败")
			}
			size = binary.BigEndian.Uint64(extended)
			headerBytes = 16
		} else if size == 0 {
			size = uint64(remaining)
		}
		if size < headerBytes || size > uint64(remaining) {
			return MP4Validation{}, errors.New("MP4 顶层盒长度越界")
		}
		name := string(boxType)
		if boxCount == 0 {
			if name != "ftyp" || size < headerBytes+8 || (size-headerBytes-8)%4 != 0 {
				return MP4Validation{}, errors.New("MP4 缺少有效的首个 ftyp 盒")
			}
			brand, err := readMP4Bytes(reader, offset+int64(headerBytes), 4)
			if err != nil || !validMP4BoxType(brand) {
				return MP4Validation{}, errors.New("MP4 主品牌无效")
			}
			majorBrand = string(brand)
		}
		if name == "moov" || name == "moof" {
			foundMovie = true
		}
		if name == "mdat" {
			foundMediaData = true
		}
		boxCount++
		offset += int64(size)
		if size == uint64(remaining) {
			break
		}
	}
	if offset != totalBytes || !foundMovie || !foundMediaData {
		return MP4Validation{}, errors.New("MP4 缺少完整的媒体或电影盒")
	}
	return MP4Validation{Method: "iso_bmff_top_level_boxes", MajorBrand: majorBrand, BoxCount: boxCount}, nil
}

// ValidateMP4Structure 验证内存中的完整 MP4 数据。
func ValidateMP4Structure(data []byte) (MP4Validation, error) {
	return ValidateMP4Reader(bytes.NewReader(data), int64(len(data)))
}
