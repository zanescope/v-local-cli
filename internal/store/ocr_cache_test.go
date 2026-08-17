package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOCRTextCacheRoundTripAndSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "ocr-texts.db")
	value := OCRText{
		EvidenceID: "wechat:chat:42", Chat: "chat", LocalID: 7, ServerID: 42,
		Timestamp: 100, SortKey: 101, Text: "图片中的测试文字",
		ImageSHA256: strings.Repeat("ab", 32), Engine: "wechat-native-ocr",
		EngineVersion: "4.1.test", Source: "v-local-cli_private_cache",
	}
	if err := SaveOCRText(path, value); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := LoadOCRText(path, value.EvidenceID)
	if err != nil || !found || loaded.Text != value.Text || loaded.ImageSHA256 != value.ImageSHA256 {
		t.Fatalf("OCR 缓存读取异常：found=%v err=%v value=%+v", found, err, loaded)
	}
	items, err := SearchOCRTexts(path, "测试", "chat", nil, nil, 0)
	if err != nil || len(items) != 1 || items[0].EvidenceID != value.EvidenceID {
		t.Fatalf("OCR 缓存搜索异常：err=%v items=%+v", err, items)
	}
	count, err := OCRTextCount(path)
	if err != nil || count != 1 {
		t.Fatalf("OCR 缓存计数异常：count=%d err=%v", count, err)
	}
	if err := DeleteOCRText(path, value.EvidenceID); err != nil {
		t.Fatal(err)
	}
	if count, err = OCRTextCount(path); err != nil || count != 0 {
		t.Fatalf("OCR 缓存删除异常：count=%d err=%v", count, err)
	}
}

func TestOCRTextCacheRejectsInvalidDigest(t *testing.T) {
	err := SaveOCRText(filepath.Join(t.TempDir(), "ocr.db"), OCRText{
		EvidenceID: "wechat:chat:42", Chat: "chat", LocalID: 7,
		Text: "文字", ImageSHA256: "invalid",
	})
	if err == nil {
		t.Fatal("无效图片摘要不应进入 OCR 缓存")
	}
}
