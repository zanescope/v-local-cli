package cryptoutil

import "testing"

func FuzzValidateImageStructure(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
	f.Add([]byte("RIFF\x04\x00\x00\x00WEBP"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 2*1024*1024 {
			t.Skip()
		}
		_, _ = ValidateImageStructure(payload)
	})
}

func FuzzValidateMP4Structure(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("\x00\x00\x00\x10ftypisom\x00\x00\x00\x00\x00\x00\x00\x08moov\x00\x00\x00\x08mdat"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 2*1024*1024 {
			t.Skip()
		}
		_, _ = ValidateMP4Structure(payload)
	})
}
