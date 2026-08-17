package cryptoutil

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodedTestImage(t *testing.T, format string) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 2, 2))
	value.Set(0, 0, color.RGBA{R: 0x32, G: 0x64, B: 0x96, A: 0xff})
	var output bytes.Buffer
	var err error
	switch format {
	case "jpg":
		err = jpeg.Encode(&output, value, nil)
	case "png":
		err = png.Encode(&output, value)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestValidateImageStructureDecodesCompleteImages(t *testing.T) {
	for _, format := range []string{"jpg", "png"} {
		data := encodedTestImage(t, format)
		validation, err := ValidateImageStructure(data)
		if err != nil || validation.Format != format || validation.Method != "full_decode" || validation.Width != 2 || validation.Height != 2 {
			t.Fatalf("完整图片验证异常：format=%s validation=%+v err=%v", format, validation, err)
		}
		if _, err := ValidateImageStructure(data[:len(data)/2]); err == nil {
			t.Fatalf("截断图片未被拒绝：%s", format)
		}
	}
}

func TestValidateImageStructureChecksWebPChunkBounds(t *testing.T) {
	data := make([]byte, 22)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8L")
	binary.LittleEndian.PutUint32(data[16:20], 2)
	data[20], data[21] = 1, 2
	validation, err := validateWebP(data)
	if err != nil || validation.Method != "riff_chunk_structure" {
		t.Fatalf("WebP 分块验证异常：validation=%+v err=%v", validation, err)
	}
	data[4]++
	if _, err := ValidateImageStructure(data); err == nil {
		t.Fatal("WebP RIFF 长度错误未被拒绝")
	}
}

func TestValidateImageStructureDecodesCompleteGIF(t *testing.T) {
	value := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var output bytes.Buffer
	if err := gif.Encode(&output, value, nil); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	validation, err := ValidateImageStructure(data)
	if err != nil || validation.Method != "all_frames_decode_and_trailer" {
		t.Fatalf("GIF 完整验证异常：validation=%+v err=%v", validation, err)
	}
	if _, err := ValidateImageStructure(data[:len(data)-1]); err == nil {
		t.Fatal("截断 GIF 未被拒绝")
	}
}

func TestValidateImageStructureRejectsTrailingData(t *testing.T) {
	for _, format := range []string{"jpg", "png"} {
		data := append(encodedTestImage(t, format), 0)
		if _, err := ValidateImageStructure(data); err == nil {
			t.Fatalf("带尾随数据的图片未被拒绝：%s", format)
		}
	}
}
