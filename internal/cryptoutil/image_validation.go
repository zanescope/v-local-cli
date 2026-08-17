package cryptoutil

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/gif"
	"image/jpeg"
	"image/png"
)

const maxDecodedImagePixels int64 = 40 * 1000 * 1000

type ImageValidation struct {
	Format string
	Method string
	Width  int
	Height int
}

func validImageDimensions(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	return int64(width) <= maxDecodedImagePixels/int64(height)
}

func validateJPEG(data []byte) (ImageValidation, error) {
	if len(data) < 4 || data[len(data)-2] != 0xff || data[len(data)-1] != 0xd9 {
		return ImageValidation{}, errors.New("JPEG 缺少结束标记")
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validImageDimensions(config.Width, config.Height) {
		return ImageValidation{}, errors.New("JPEG 尺寸或结构无效")
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return ImageValidation{}, errors.New("JPEG 数据不完整")
	}
	return ImageValidation{Format: "jpg", Method: "full_decode", Width: config.Width, Height: config.Height}, nil
}

func validatePNG(data []byte) (ImageValidation, error) {
	if !validPNGChunkStructure(data) {
		return ImageValidation{}, errors.New("PNG 分块结构无效")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validImageDimensions(config.Width, config.Height) {
		return ImageValidation{}, errors.New("PNG 尺寸或结构无效")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return ImageValidation{}, errors.New("PNG 数据不完整")
	}
	return ImageValidation{Format: "png", Method: "full_decode", Width: config.Width, Height: config.Height}, nil
}

func validPNGChunkStructure(data []byte) bool {
	if len(data) < 20 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	offset := 8
	chunkIndex := 0
	foundImageData := false
	for offset < len(data) {
		if len(data)-offset < 12 {
			return false
		}
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkType := string(data[offset+4 : offset+8])
		if size > uint64(len(data)-offset-12) {
			return false
		}
		next := offset + 12 + int(size)
		if chunkIndex == 0 && (chunkType != "IHDR" || size != 13) {
			return false
		}
		if chunkType == "IDAT" {
			foundImageData = true
		}
		if chunkType == "IEND" {
			return size == 0 && foundImageData && next == len(data)
		}
		offset = next
		chunkIndex++
	}
	return false
}

func validateGIF(data []byte) (ImageValidation, error) {
	config, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validImageDimensions(config.Width, config.Height) {
		return ImageValidation{}, errors.New("GIF 尺寸或结构无效")
	}
	if len(data) == 0 || data[len(data)-1] != 0x3b || !validGIFBlockStructure(data) {
		return ImageValidation{}, errors.New("GIF 缺少结束标记")
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(decoded.Image) == 0 {
		return ImageValidation{}, errors.New("GIF 帧数据不完整")
	}
	return ImageValidation{Format: "gif", Method: "all_frames_decode_and_trailer", Width: config.Width, Height: config.Height}, nil
}

func skipGIFSubBlocks(data []byte, offset int) (int, bool) {
	for offset < len(data) {
		size := int(data[offset])
		offset++
		if size == 0 {
			return offset, true
		}
		if size > len(data)-offset {
			return 0, false
		}
		offset += size
	}
	return 0, false
}

func validGIFBlockStructure(data []byte) bool {
	if len(data) < 14 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return false
	}
	offset := 13
	if data[10]&0x80 != 0 {
		colorTableBytes := 3 * (1 << ((data[10] & 0x07) + 1))
		if colorTableBytes > len(data)-offset {
			return false
		}
		offset += colorTableBytes
	}
	var totalPixels int64
	for offset < len(data) {
		switch data[offset] {
		case 0x3b:
			return offset == len(data)-1 && totalPixels > 0
		case 0x21:
			if len(data)-offset < 2 {
				return false
			}
			var ok bool
			offset, ok = skipGIFSubBlocks(data, offset+2)
			if !ok {
				return false
			}
		case 0x2c:
			if len(data)-offset < 10 {
				return false
			}
			width := int(binary.LittleEndian.Uint16(data[offset+5 : offset+7]))
			height := int(binary.LittleEndian.Uint16(data[offset+7 : offset+9]))
			if !validImageDimensions(width, height) {
				return false
			}
			pixels := int64(width) * int64(height)
			if pixels > maxDecodedImagePixels-totalPixels {
				return false
			}
			totalPixels += pixels
			packed := data[offset+9]
			offset += 10
			if packed&0x80 != 0 {
				colorTableBytes := 3 * (1 << ((packed & 0x07) + 1))
				if colorTableBytes > len(data)-offset {
					return false
				}
				offset += colorTableBytes
			}
			if offset >= len(data) {
				return false
			}
			offset++
			var ok bool
			offset, ok = skipGIFSubBlocks(data, offset)
			if !ok {
				return false
			}
		default:
			return false
		}
	}
	return false
}

func validateWebP(data []byte) (ImageValidation, error) {
	if len(data) < 20 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return ImageValidation{}, errors.New("WebP RIFF 头无效")
	}
	declared := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if declared != uint64(len(data)) {
		return ImageValidation{}, errors.New("WebP RIFF 长度不匹配")
	}
	foundImageChunk := false
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return ImageValidation{}, errors.New("WebP 分块头不完整")
		}
		name := string(data[offset : offset+4])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		padded := size + size%2
		if padded > uint64(len(data)-offset) {
			return ImageValidation{}, errors.New("WebP 分块越界")
		}
		if (name == "VP8 " || name == "VP8L" || name == "VP8X") && size > 0 {
			foundImageChunk = true
		}
		offset += int(padded)
	}
	if !foundImageChunk {
		return ImageValidation{}, errors.New("WebP 缺少图像分块")
	}
	return ImageValidation{Format: "webp", Method: "riff_chunk_structure"}, nil
}

// ValidateImageStructure 验证完整图片结构，不只依赖文件头魔数。
func ValidateImageStructure(data []byte) (ImageValidation, error) {
	switch ImageFormat(data) {
	case "jpg":
		return validateJPEG(data)
	case "png":
		return validatePNG(data)
	case "gif":
		return validateGIF(data)
	case "webp":
		if _, err := validateWebP(data); err != nil {
			return ImageValidation{}, err
		}
		return ImageValidation{}, errors.New("WebP 暂无零依赖的严格解码验证器")
	case "wxgf":
		return ImageValidation{}, errors.New("WXGF 暂无可靠的严格结构验证器")
	default:
		return ImageValidation{}, errors.New("不是已知图片容器")
	}
}
