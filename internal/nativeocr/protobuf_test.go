package nativeocr

import (
	"encoding/binary"
	"math"
	"testing"
)

func fixed32Field(field int, value float32) []byte {
	result := appendVarint(nil, uint64(field<<3|5))
	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, math.Float32bits(value))
	return append(result, raw...)
}

func TestOCRProtobufRequestAndResponse(t *testing.T) {
	request := ocrRequest(2, `C:\图片.png`)
	if len(request) == 0 {
		t.Fatal("OCR 请求为空")
	}
	line := appendBytesField(nil, 2, []byte("识别文字"))
	line = append(line, fixed32Field(3, 0.98)...)
	line = append(line, fixed32Field(5, 1)...)
	line = append(line, fixed32Field(6, 2)...)
	line = append(line, fixed32Field(7, 30)...)
	line = append(line, fixed32Field(8, 40)...)
	info := appendBytesField(nil, 3, line)
	info = appendVarintField(info, 4, 100)
	info = appendVarintField(info, 5, 50)
	response := appendVarintField(nil, 1, 2)
	response = appendVarintField(response, 2, 0)
	response = appendBytesField(response, 3, info)
	taskID, code, result, err := parseOCRResponse(response)
	if err != nil || taskID != 2 || code != 0 || result.Text != "识别文字" || result.Width != 100 || result.Height != 50 || len(result.Lines) != 1 {
		t.Fatalf("OCR 响应解析异常：task=%d code=%d result=%+v err=%v", taskID, code, result, err)
	}
}
