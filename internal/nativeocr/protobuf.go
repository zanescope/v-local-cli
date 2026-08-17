package nativeocr

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

func appendVarint(target []byte, value uint64) []byte {
	for value >= 0x80 {
		target = append(target, byte(value)|0x80)
		value >>= 7
	}
	return append(target, byte(value))
}

func appendVarintField(target []byte, field int, value uint64) []byte {
	target = appendVarint(target, uint64(field<<3))
	return appendVarint(target, value)
}

func appendBytesField(target []byte, field int, value []byte) []byte {
	target = appendVarint(target, uint64(field<<3|2))
	target = appendVarint(target, uint64(len(value)))
	return append(target, value...)
}

func ocrRequest(taskID uint64, imagePath string) []byte {
	requestType := appendVarintField(nil, 1, 1)
	requestType = appendVarintField(requestType, 2, 1)
	result := appendVarintField(nil, 1, taskID)
	result = appendBytesField(result, 2, []byte(imagePath))
	return appendBytesField(result, 6, requestType)
}

func readVarint(payload []byte, offset *int) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *offset >= len(payload) {
			return 0, errors.New("protobuf 截断")
		}
		current := payload[*offset]
		*offset++
		value |= uint64(current&0x7f) << shift
		if current < 0x80 {
			return value, nil
		}
	}
	return 0, errors.New("protobuf varint 无效")
}

func readField(payload []byte, offset *int) (int, int, []byte, uint64, error) {
	key, err := readVarint(payload, offset)
	if err != nil {
		return 0, 0, nil, 0, err
	}
	field, wire := int(key>>3), int(key&7)
	if field <= 0 {
		return 0, 0, nil, 0, errors.New("protobuf 字段无效")
	}
	switch wire {
	case 0:
		value, valueErr := readVarint(payload, offset)
		return field, wire, nil, value, valueErr
	case 2:
		length, lengthErr := readVarint(payload, offset)
		if lengthErr != nil || length > uint64(len(payload)-*offset) {
			return 0, 0, nil, 0, errors.New("protobuf 长度无效")
		}
		value := payload[*offset : *offset+int(length)]
		*offset += int(length)
		return field, wire, value, 0, nil
	case 5:
		if len(payload)-*offset < 4 {
			return 0, 0, nil, 0, errors.New("protobuf fixed32 截断")
		}
		value := uint64(binary.LittleEndian.Uint32(payload[*offset : *offset+4]))
		*offset += 4
		return field, wire, nil, value, nil
	case 1:
		if len(payload)-*offset < 8 {
			return 0, 0, nil, 0, errors.New("protobuf fixed64 截断")
		}
		value := binary.LittleEndian.Uint64(payload[*offset : *offset+8])
		*offset += 8
		return field, wire, nil, value, nil
	default:
		return 0, 0, nil, 0, errors.New("protobuf wire type 不受支持")
	}
}

func parseOCRLine(payload []byte) (Line, error) {
	line := Line{}
	for offset := 0; offset < len(payload); {
		field, wire, bytesValue, number, err := readField(payload, &offset)
		if err != nil {
			return Line{}, err
		}
		switch {
		case field == 2 && wire == 2:
			line.Text = strings.ToValidUTF8(string(bytesValue), "�")
		case field == 3 && wire == 5:
			line.Confidence = math.Float32frombits(uint32(number))
		case field == 5 && wire == 5:
			line.Left = math.Float32frombits(uint32(number))
		case field == 6 && wire == 5:
			line.Top = math.Float32frombits(uint32(number))
		case field == 7 && wire == 5:
			line.Right = math.Float32frombits(uint32(number))
		case field == 8 && wire == 5:
			line.Bottom = math.Float32frombits(uint32(number))
		}
	}
	line.Text = strings.TrimSpace(line.Text)
	return line, nil
}

func parseOCRInfo(payload []byte) (Result, error) {
	result := Result{Lines: []Line{}}
	for offset := 0; offset < len(payload); {
		field, wire, bytesValue, number, err := readField(payload, &offset)
		if err != nil {
			return Result{}, err
		}
		switch {
		case field == 3 && wire == 2:
			line, lineErr := parseOCRLine(bytesValue)
			if lineErr != nil {
				return Result{}, lineErr
			}
			if line.Text != "" {
				result.Lines = append(result.Lines, line)
			}
		case field == 4 && wire == 0:
			result.Width = uint32(number)
		case field == 5 && wire == 0:
			result.Height = uint32(number)
		}
	}
	texts := make([]string, 0, len(result.Lines))
	for _, line := range result.Lines {
		texts = append(texts, line.Text)
	}
	result.Text = strings.Join(texts, "\n")
	return result, nil
}

func parseOCRResponse(payload []byte) (uint64, int32, Result, error) {
	var taskID uint64
	var errorCode int32
	result := Result{}
	for offset := 0; offset < len(payload); {
		field, wire, bytesValue, number, err := readField(payload, &offset)
		if err != nil {
			return 0, 0, Result{}, err
		}
		switch {
		case field == 1 && wire == 0:
			taskID = number
		case field == 2 && wire == 0:
			errorCode = int32(number)
		case field == 3 && wire == 2:
			result, err = parseOCRInfo(bytesValue)
			if err != nil {
				return 0, 0, Result{}, err
			}
		}
	}
	return taskID, errorCode, result, nil
}
