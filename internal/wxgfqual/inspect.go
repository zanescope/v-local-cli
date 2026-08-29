// Package wxgfqual contains qualification-only WXGF experiments. Nothing in
// this package is wired into the public CLI or allowed to turn WXGF into a
// successful chat-image export until the separate promotion blockers are
// closed by real-device acceptance.
package wxgfqual

import (
	"bytes"
	"errors"
	"fmt"
)

const (
	MaxWXGFBytes       = 64 * 1024 * 1024
	maxWXGFPrefixBytes = 1024 * 1024
	maxHEVCNALUnits    = 4096
)

var (
	ErrNotWXGF              = errors.New("输入不是 WXGF 容器")
	ErrWXGFTooLarge         = errors.New("WXGF 输入超过资格验证上限")
	ErrNoHEVCCandidate      = errors.New("WXGF 中没有 HEVC Annex-B 候选")
	ErrInvalidHEVCCandidate = errors.New("WXGF 中的 HEVC Annex-B 候选结构无效")
	ErrMissingParameterSets = errors.New("HEVC 候选缺少前置 VPS/SPS/PPS")
	ErrMultiplePictures     = errors.New("HEVC 候选包含多张图片")
	ErrNotIndependentStill  = errors.New("HEVC 候选不是独立单图")
)

// Inspection only qualifies a bounded, layer-zero, single-picture Annex-B
// candidate. It deliberately does not claim to validate the undocumented WXGF
// container layout or the complete HEVC bitstream syntax.
type Inspection struct {
	Method              string
	HEVCOffset          int
	HEVCBytes           int
	NALUnitCount        int
	PictureCount        int
	FirstPictureNALType int
	HasVPS              bool
	HasSPS              bool
	HasPPS              bool
}

type annexBStart struct {
	offset int
	length int
}

func startCodeAt(data []byte, offset int) int {
	if offset < 0 || offset+3 > len(data) {
		return 0
	}
	if offset+4 <= len(data) && data[offset] == 0 && data[offset+1] == 0 && data[offset+2] == 0 && data[offset+3] == 1 {
		return 4
	}
	if data[offset] == 0 && data[offset+1] == 0 && data[offset+2] == 1 {
		return 3
	}
	return 0
}

func findStartCode(data []byte, from int) annexBStart {
	if from < 0 {
		from = 0
	}
	for offset := from; offset+3 <= len(data); offset++ {
		if length := startCodeAt(data, offset); length != 0 {
			return annexBStart{offset: offset, length: length}
		}
	}
	return annexBStart{offset: -1}
}

func validVCLType(nalType int) bool {
	return (nalType >= 0 && nalType <= 9) || (nalType >= 16 && nalType <= 21)
}

func independentPictureType(nalType int) bool {
	return nalType >= 16 && nalType <= 21
}

func invalidCandidate(reason string) error {
	return fmt.Errorf("%w：%s", ErrInvalidHEVCCandidate, reason)
}

func inspectWXGF(data []byte, maxBytes, maxPrefixBytes, maxNALUnits int) (Inspection, error) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte("wxgf")) {
		return Inspection{}, ErrNotWXGF
	}
	if len(data) > maxBytes {
		return Inspection{}, ErrWXGFTooLarge
	}
	first := findStartCode(data, 4)
	if first.offset < 0 || first.offset > maxPrefixBytes {
		return Inspection{}, ErrNoHEVCCandidate
	}

	inspection := Inspection{
		Method:     "wxgf_magic+bounded_prefix+annex_b_nal_headers+single_irap_picture",
		HEVCOffset: first.offset,
		HEVCBytes:  len(data) - first.offset,
	}
	current := first
	seenVCL := false
	terminal := false

	for current.offset >= 0 {
		if inspection.NALUnitCount >= maxNALUnits {
			return Inspection{}, invalidCandidate("NAL 单元数量超过上限")
		}
		payloadStart := current.offset + current.length
		next := findStartCode(data, payloadStart)
		payloadEnd := len(data)
		if next.offset >= 0 {
			payloadEnd = next.offset
		}
		for payloadEnd > payloadStart && data[payloadEnd-1] == 0 {
			payloadEnd--
		}
		if payloadEnd-payloadStart < 3 {
			return Inspection{}, invalidCandidate("NAL 单元为空或被截断")
		}

		header0, header1 := data[payloadStart], data[payloadStart+1]
		if header0&0x80 != 0 {
			return Inspection{}, invalidCandidate("forbidden_zero_bit 非零")
		}
		nalType := int((header0 >> 1) & 0x3f)
		layerID := int(header0&1)<<5 | int(header1>>3)
		temporalIDPlus1 := int(header1 & 0x07)
		if layerID != 0 || temporalIDPlus1 == 0 || nalType > 40 {
			return Inspection{}, invalidCandidate("NAL 头的 layer/type/temporal 字段不受支持")
		}
		inspection.NALUnitCount++

		switch nalType {
		case 32:
			if inspection.HasSPS || inspection.HasPPS || seenVCL || terminal {
				return Inspection{}, invalidCandidate("VPS 顺序无效或出现在图片数据之后")
			}
			inspection.HasVPS = true
		case 33:
			if !inspection.HasVPS || inspection.HasPPS || seenVCL || terminal {
				return Inspection{}, invalidCandidate("SPS 顺序无效或出现在图片数据之后")
			}
			inspection.HasSPS = true
		case 34:
			if !inspection.HasVPS || !inspection.HasSPS || seenVCL || terminal {
				return Inspection{}, invalidCandidate("PPS 顺序无效或出现在图片数据之后")
			}
			inspection.HasPPS = true
		case 36, 37:
			if !seenVCL {
				return Inspection{}, invalidCandidate("序列结束标记出现在图片之前")
			}
			terminal = true
		default:
			if nalType <= 31 {
				if terminal || !validVCLType(nalType) {
					return Inspection{}, invalidCandidate("包含终止后或保留类型的 VCL 单元")
				}
				if !inspection.HasVPS || !inspection.HasSPS || !inspection.HasPPS {
					return Inspection{}, ErrMissingParameterSets
				}
				firstSlice := data[payloadStart+2]&0x80 != 0
				if !seenVCL {
					if !firstSlice || !independentPictureType(nalType) {
						return Inspection{}, ErrNotIndependentStill
					}
					inspection.FirstPictureNALType = nalType
				} else if nalType != inspection.FirstPictureNALType {
					return Inspection{}, invalidCandidate("同一图片的 VCL 类型不一致")
				}
				if firstSlice {
					inspection.PictureCount++
					if inspection.PictureCount > 1 {
						return Inspection{}, ErrMultiplePictures
					}
				}
				seenVCL = true
			}
		}

		current = next
	}
	if !inspection.HasVPS || !inspection.HasSPS || !inspection.HasPPS {
		return Inspection{}, ErrMissingParameterSets
	}
	if !seenVCL || inspection.PictureCount != 1 {
		return Inspection{}, ErrNotIndependentStill
	}
	return inspection, nil
}

// Inspect identifies a conservative HEVC candidate inside WXGF for an
// isolated decoder trial. A successful result is not sufficient for public
// image export: the decoded output must still pass full image validation, and
// the provider process needs trust, network, and resource isolation.
func Inspect(data []byte) (Inspection, error) {
	return inspectWXGF(data, MaxWXGFBytes, maxWXGFPrefixBytes, maxHEVCNALUnits)
}
