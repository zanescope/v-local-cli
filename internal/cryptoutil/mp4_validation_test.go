package cryptoutil

import (
	"encoding/binary"
	"testing"
)

func testMP4Box(name string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], name)
	copy(result[8:], payload)
	return result
}

func testMP4() []byte {
	ftyp := testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00isommp41"))
	moov := testMP4Box("moov", nil)
	mdat := testMP4Box("mdat", []byte{1, 2, 3, 4})
	return append(append(ftyp, moov...), mdat...)
}

func TestValidateMP4StructureAcceptsCompleteTopLevelBoxes(t *testing.T) {
	validation, err := ValidateMP4Structure(testMP4())
	if err != nil || validation.Method != "iso_bmff_top_level_boxes" || validation.MajorBrand != "isom" || validation.BoxCount != 3 {
		t.Fatalf("MP4 结构验真异常：validation=%+v err=%v", validation, err)
	}
}

func TestValidateMP4StructureRejectsTruncationAndMissingCoreBoxes(t *testing.T) {
	complete := testMP4()
	for _, invalid := range [][]byte{
		complete[:len(complete)-1],
		append(testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00")), testMP4Box("mdat", nil)...),
		append(append(testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00")), testMP4Box("moov", nil)...), 0),
	} {
		if _, err := ValidateMP4Structure(invalid); err == nil {
			t.Fatal("无效 MP4 未被拒绝")
		}
	}
}

func TestValidateMP4StructureAcceptsExtendedMediaBox(t *testing.T) {
	ftyp := testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00"))
	moof := testMP4Box("moof", nil)
	mdat := make([]byte, 20)
	binary.BigEndian.PutUint32(mdat[:4], 1)
	copy(mdat[4:8], "mdat")
	binary.BigEndian.PutUint64(mdat[8:16], uint64(len(mdat)))
	validation, err := ValidateMP4Structure(append(append(ftyp, moof...), mdat...))
	if err != nil || validation.BoxCount != 3 {
		t.Fatalf("扩展长度 MP4 盒验真异常：validation=%+v err=%v", validation, err)
	}
}
