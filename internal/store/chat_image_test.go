package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatImageStemParsesNestedResourceIdentifier(t *testing.T) {
	wanted := strings.Repeat("a1", 16)
	inner := append([]byte{0x0a, 0x20}, []byte(wanted)...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	actual, err := chatImageStem(packed)
	if err != nil || actual != wanted {
		t.Fatalf("图片资源标识解析异常：actual=%q err=%v", actual, err)
	}
}

func TestChatImageStemRejectsNonHexIdentifier(t *testing.T) {
	inner := append([]byte{0x0a, 0x20}, []byte(strings.Repeat("z", 32))...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	if _, err := chatImageStem(packed); err == nil {
		t.Fatal("非十六进制图片资源标识不应通过")
	}
}

func TestChatImageResolutionErrorDoesNotExposeInternalCause(t *testing.T) {
	err := chatImageFailure("local_mapping_unavailable", unknownChatImageRemoteDescriptor(), errors.New(`D:\private\wechat\hardlink.db`))
	if err.Error() != "local_mapping_unavailable" || strings.Contains(err.Error(), "private") {
		t.Fatalf("图片诊断泄露了内部原因：%q", err.Error())
	}
	if !errors.Is(err, err.cause) {
		t.Fatal("内部调用方仍应能通过 Unwrap 取得原因")
	}
}

func TestChatImageCandidateIndexMatchesBoundFallbackWalk(t *testing.T) {
	snapshot := t.TempDir()
	account := t.TempDir()
	stem := strings.Repeat("ab", 16)
	name := stem + "_h.dat"
	hardlinkPath := filepath.Join(snapshot, "hardlink", "hardlink.db")
	if err := ensureParent(hardlinkPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, hardlinkPath,
		"CREATE TABLE dir2id(username TEXT)",
		"INSERT INTO dir2id(rowid,username) VALUES(1,'one'),(2,'two')",
		"CREATE TABLE image_hardlink_info_v4(md5 TEXT,file_name TEXT,dir1 INTEGER,dir2 INTEGER)",
		"INSERT INTO image_hardlink_info_v4 VALUES('','"+name+"',1,2)",
	)
	nested := filepath.Join(account, "msg", "attach", "conversation", "extra", name)
	if err := ensureParent(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := sqliteFiles(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	walked, err := chatImageCandidatesFromFiles(files, account, stem, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := chatImageCandidatesFromFiles(files, account, stem, "", map[string][]string{
		strings.ToLower(name): {nested},
	})
	if err != nil {
		t.Fatal(err)
	}
	walkedPaths, indexedPaths := map[string]bool{}, map[string]bool{}
	for _, candidate := range walked {
		if _, err := os.Lstat(candidate.path); err == nil {
			walkedPaths[strings.ToLower(filepath.Clean(candidate.path))] = candidate.qualityTier == "high"
		}
	}
	for _, candidate := range indexed {
		if _, err := os.Lstat(candidate.path); err == nil {
			indexedPaths[strings.ToLower(filepath.Clean(candidate.path))] = candidate.qualityTier == "high"
		}
	}
	if len(walkedPaths) != 1 || len(indexedPaths) != 1 || !walkedPaths[strings.ToLower(filepath.Clean(nested))] ||
		!indexedPaths[strings.ToLower(filepath.Clean(nested))] {
		t.Fatalf("预索引与有界补扫结果不一致：walked=%v indexed=%v", walkedPaths, indexedPaths)
	}
}

func TestParseChatImageRemoteDescriptorKeepsSecretsPrivateAndClassifiesStructure(t *testing.T) {
	highParameter := strings.Repeat("ab", 89)
	mediumParameter := strings.Repeat("cd", 96)
	thumbnailParameter := strings.Repeat("ef", 97)
	key := strings.Repeat("01", 16)
	thumbnailKey := strings.Repeat("23", 16)
	mediumMD5 := strings.Repeat("45", 16)
	highMD5 := strings.Repeat("67", 16)
	content := `<msg><img cdnbigimgurl="` + highParameter + `" cdnmidimgurl="` + mediumParameter + `" cdnthumburl="` + thumbnailParameter +
		`" aeskey="` + key + `" cdnthumbaeskey="` + thumbnailKey + `" hdlength="4096" length="2048" cdnthumblength="512"` +
		` cdnhdwidth="320" cdnhdheight="240" cdnmidwidth="300" cdnmidheight="225" cdnthumbwidth="120" cdnthumbheight="90"` +
		` originsourcemd5="` + highMD5 + `" md5="` + mediumMD5 + `" /></msg>`
	descriptor := parseChatImageRemoteDescriptor(content)
	defer descriptor.clear()
	if descriptor.status != "present_expiry_unknown" || descriptor.parseStatus != "parsed_unverified_protocol" ||
		descriptor.protocolStatus != "unverified_desktop_protocol" || len(descriptor.candidates) != 3 {
		t.Fatalf("聊天 CDN 描述符结构分类异常：%+v", descriptor)
	}
	if strings.Join(descriptor.tiers, ",") != "high,medium,thumbnail" {
		t.Fatalf("聊天 CDN 档位顺序异常：%v", descriptor.tiers)
	}
	high := descriptor.candidates[0]
	if high.tier != "high" || high.encryptedQueryParameter != highParameter || high.parameterEncoding != "opaque_hex" ||
		high.expectedBytes != 4096 || high.expectedWidth != 320 || high.expectedHeight != 240 || high.expectedMD5 != highMD5 ||
		!bytes.Equal(high.aesKey[:], bytes.Repeat([]byte{0x01}, 16)) {
		t.Fatalf("high 描述符解析异常：%+v", high)
	}
	thumbnail := descriptor.candidates[2]
	if thumbnail.expectedMD5 != "" || !bytes.Equal(thumbnail.aesKey[:], bytes.Repeat([]byte{0x23}, 16)) {
		t.Fatalf("缩略图独立 key 或无 MD5 边界异常：%+v", thumbnail)
	}
	publicFailure := chatImageFailure("local_file_missing", descriptor, errors.New(highParameter+key))
	if strings.Contains(publicFailure.Error(), highParameter) || strings.Contains(publicFailure.Error(), key) ||
		publicFailure.RemoteDescriptorParseStatus != "parsed_unverified_protocol" {
		t.Fatalf("公开错误泄露描述符或丢失脱敏状态：%q %+v", publicFailure.Error(), publicFailure)
	}
}

func TestParseChatImageRemoteDescriptorSeparatesIncompleteInvalidAndPartial(t *testing.T) {
	parameter := strings.Repeat("ab", 89)
	key := strings.Repeat("01", 16)
	cases := []struct {
		name, content, status string
		candidates            int
	}{
		{name: "incomplete", content: `<msg><img cdnbigimgurl="` + parameter + `" /></msg>`, status: "present_incomplete"},
		{name: "binding_missing", content: `<msg><img cdnbigimgurl="` + parameter + `" aeskey="` + key + `" /></msg>`, status: "present_incomplete"},
		{name: "invalid", content: `<msg><img cdnbigimgurl="https://example.invalid/image" aeskey="` + key + `" /></msg>`, status: "present_invalid"},
		{name: "partial", content: `<msg><img cdnbigimgurl="` + parameter + `" cdnmidimgurl="not-opaque-hex" aeskey="` + key + `" hdlength="4096" cdnhdwidth="320" cdnhdheight="240" /></msg>`, status: "parsed_partial_unverified_protocol", candidates: 1},
		{name: "optional_metadata_invalid", content: `<msg><img cdnbigimgurl="` + parameter + `" aeskey="` + key + `" hdlength="not-a-number" cdnhdwidth="320" originsourcemd5="` + strings.Repeat("23", 16) + `" /></msg>`, status: "parsed_partial_unverified_protocol", candidates: 1},
		{name: "zero_optional_metadata_is_unavailable", content: `<msg><img cdnmidimgurl="` + parameter + `" aeskey="` + key + `" length="0" cdnmidwidth="0" cdnmidheight="0" md5="` + strings.Repeat("23", 16) + `" /></msg>`, status: "parsed_unverified_protocol", candidates: 1},
		{name: "missing", content: `<msg><img md5="` + strings.Repeat("ab", 16) + `" /></msg>`, status: "not_applicable"},
		{name: "unknown", content: `<msg><img`, status: "not_evaluated"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor := parseChatImageRemoteDescriptor(testCase.content)
			defer descriptor.clear()
			if descriptor.parseStatus != testCase.status || len(descriptor.candidates) != testCase.candidates {
				t.Fatalf("parse_status=%q want=%q descriptor=%+v", descriptor.parseStatus, testCase.status, descriptor)
			}
		})
	}
}

func TestParseChatImageRemoteDescriptorTreatsZeroObservationsAsMissingButNotBinding(t *testing.T) {
	parameter := strings.Repeat("ab", 89)
	key := strings.Repeat("01", 16)
	md5Value := strings.Repeat("23", 16)
	withMD5 := parseChatImageRemoteDescriptor(`<msg><img cdnmidimgurl="` + parameter + `" aeskey="` + key +
		`" length="0" cdnmidwidth="0" cdnmidheight="0" md5="` + md5Value + `" /></msg>`)
	defer withMD5.clear()
	if withMD5.parseStatus != "parsed_unverified_protocol" || len(withMD5.candidates) != 1 ||
		withMD5.candidates[0].expectedBytes != 0 || withMD5.candidates[0].expectedWidth != 0 ||
		withMD5.candidates[0].expectedHeight != 0 || withMD5.candidates[0].expectedMD5 != md5Value {
		t.Fatalf("0 占位不应成为非法值或响应绑定：%+v", withMD5)
	}

	withoutMD5 := parseChatImageRemoteDescriptor(`<msg><img cdnthumburl="` + parameter + `" cdnthumbaeskey="` + key +
		`" cdnthumblength="0" cdnthumbwidth="0" cdnthumbheight="0" /></msg>`)
	defer withoutMD5.clear()
	if withoutMD5.parseStatus != "present_incomplete" || len(withoutMD5.candidates) != 0 {
		t.Fatalf("0 占位不能单独满足响应绑定：%+v", withoutMD5)
	}
}
