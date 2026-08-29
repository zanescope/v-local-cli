package wxgfqual

import (
	"errors"
	"testing"
)

func appendNAL(stream []byte, startLength, nalType int, firstSlice bool, body ...byte) []byte {
	if startLength == 3 {
		stream = append(stream, 0, 0, 1)
	} else {
		stream = append(stream, 0, 0, 0, 1)
	}
	stream = append(stream, byte(nalType<<1), 1)
	firstByte := byte(0x40)
	if firstSlice {
		firstByte = 0x80
	}
	stream = append(stream, firstByte)
	return append(stream, body...)
}

func singlePictureFixture() []byte {
	data := append([]byte("wxgf"), []byte{0x10, 0x20, 0x30, 0x40}...)
	data = appendNAL(data, 4, 32, false, 0x11)
	data = appendNAL(data, 3, 33, false, 0x22)
	data = appendNAL(data, 4, 34, false, 0x33)
	data = appendNAL(data, 3, 39, false, 0x44)
	data = appendNAL(data, 4, 19, true, 0x55, 0x66)
	data = appendNAL(data, 3, 19, false, 0x77)
	data = appendNAL(data, 4, 40, false, 0x88)
	return data
}

func TestInspectQualifiesConservativeSinglePictureCandidate(t *testing.T) {
	data := singlePictureFixture()
	inspection, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.HEVCOffset != 8 || inspection.HEVCBytes != len(data)-8 || inspection.NALUnitCount != 7 ||
		inspection.PictureCount != 1 || inspection.FirstPictureNALType != 19 ||
		!inspection.HasVPS || !inspection.HasSPS || !inspection.HasPPS {
		t.Fatalf("WXGF 单图资格检查结果异常：%+v", inspection)
	}
}

func TestInspectRejectsMissingOrLateParameterSets(t *testing.T) {
	missing := append([]byte("wxgf"), []byte{0x10, 0x20}...)
	missing = appendNAL(missing, 4, 32, false, 1)
	missing = appendNAL(missing, 4, 19, true, 3)
	if _, err := Inspect(missing); !errors.Is(err, ErrMissingParameterSets) {
		t.Fatalf("缺 SPS 的候选未按预期拒绝：%v", err)
	}

	late := singlePictureFixture()
	late = appendNAL(late, 4, 33, false, 9)
	if _, err := Inspect(late); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("图片之后重复参数集未被拒绝：%v", err)
	}

	outOfOrder := append([]byte("wxgf"), 1, 2)
	outOfOrder = appendNAL(outOfOrder, 4, 33, false, 1)
	outOfOrder = appendNAL(outOfOrder, 4, 32, false, 2)
	outOfOrder = appendNAL(outOfOrder, 4, 34, false, 3)
	outOfOrder = appendNAL(outOfOrder, 4, 19, true, 4)
	if _, err := Inspect(outOfOrder); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("乱序参数集未被拒绝：%v", err)
	}
}

func TestInspectRejectsMultipleOrDependentPictures(t *testing.T) {
	multiple := singlePictureFixture()
	multiple = appendNAL(multiple, 4, 19, true, 0x99)
	if _, err := Inspect(multiple); !errors.Is(err, ErrMultiplePictures) {
		t.Fatalf("多图候选未按预期拒绝：%v", err)
	}

	dependent := append([]byte("wxgf"), 1, 2)
	dependent = appendNAL(dependent, 4, 32, false, 1)
	dependent = appendNAL(dependent, 4, 33, false, 2)
	dependent = appendNAL(dependent, 4, 34, false, 3)
	dependent = appendNAL(dependent, 4, 1, true, 4)
	if _, err := Inspect(dependent); !errors.Is(err, ErrNotIndependentStill) {
		t.Fatalf("依赖参考帧的首图未被拒绝：%v", err)
	}
}

func TestInspectRejectsTruncationAndInvalidNALHeaders(t *testing.T) {
	truncated := append([]byte("wxgf"), 0, 0, 0, 1, 0x40, 0x01)
	if _, err := Inspect(truncated); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("截断 NAL 未被拒绝：%v", err)
	}

	forbidden := singlePictureFixture()
	inspection, err := Inspect(forbidden)
	if err != nil {
		t.Fatal(err)
	}
	forbidden[inspection.HEVCOffset+4] |= 0x80
	if _, err := Inspect(forbidden); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("forbidden_zero_bit 未被拒绝：%v", err)
	}

	temporalZero := singlePictureFixture()
	inspection, err = Inspect(temporalZero)
	if err != nil {
		t.Fatal(err)
	}
	temporalZero[inspection.HEVCOffset+5] = 0
	if _, err := Inspect(temporalZero); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("temporal_id_plus1=0 未被拒绝：%v", err)
	}
}

func TestInspectFailsClosedOnEarlierPolyglotStartCode(t *testing.T) {
	valid := singlePictureFixture()
	polyglot := append([]byte("wxgf-prefix"), 0, 0, 0, 1, 0xff, 0xff, 0xff)
	polyglot = append(polyglot, valid[8:]...)
	if _, err := Inspect(polyglot); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("更早的伪造起始码被跳过：%v", err)
	}
}

func TestInspectEnforcesBoundsWithoutLargeAllocations(t *testing.T) {
	data := singlePictureFixture()
	if _, err := inspectWXGF(data, len(data)-1, maxWXGFPrefixBytes, maxHEVCNALUnits); !errors.Is(err, ErrWXGFTooLarge) {
		t.Fatalf("输入大小上限未生效：%v", err)
	}
	if _, err := inspectWXGF(data, MaxWXGFBytes, 7, maxHEVCNALUnits); !errors.Is(err, ErrNoHEVCCandidate) {
		t.Fatalf("容器前缀上限未生效：%v", err)
	}
	if _, err := inspectWXGF(data, MaxWXGFBytes, maxWXGFPrefixBytes, 2); !errors.Is(err, ErrInvalidHEVCCandidate) {
		t.Fatalf("NAL 数量上限未生效：%v", err)
	}
}

func TestInspectRejectsMagicOnlyAndUnknownInput(t *testing.T) {
	if _, err := Inspect([]byte("wxgf")); !errors.Is(err, ErrNoHEVCCandidate) {
		t.Fatalf("只有魔数的 WXGF 未按预期拒绝：%v", err)
	}
	if _, err := Inspect([]byte("not-wxgf")); !errors.Is(err, ErrNotWXGF) {
		t.Fatalf("非 WXGF 输入未按预期拒绝：%v", err)
	}
}

func FuzzInspect(f *testing.F) {
	f.Add([]byte("wxgf"))
	f.Add(singlePictureFixture())
	f.Add([]byte("wxgf\x00\x00\x00\x01\x40\x01\x80"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2*1024*1024 {
			t.Skip()
		}
		_, _ = Inspect(data)
	})
}
