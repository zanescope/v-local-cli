package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func directRecoveryCandidate(t *testing.T, plain []byte, width, height int) chatImageRemoteCandidate {
	t.Helper()
	var key [16]byte
	copy(key[:], []byte("0123456789abcdef"))
	digest := md5.Sum(plain)
	return chatImageRemoteCandidate{
		tier: "high", encryptedQueryParameter: "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=temporary-token",
		parameterEncoding: "direct_https_url", aesKey: key, expectedBytes: int64(len(plain)),
		expectedWidth: width, expectedHeight: height, expectedMD5: hex.EncodeToString(digest[:]),
	}
}

func TestParseDirectChatImageURLRequiresExactHTTPSDestinationAndQuery(t *testing.T) {
	valid := "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=temporary-token%3D"
	parsed, ok := parseDirectChatImageURL(valid)
	if !ok || parsed == "" {
		t.Fatalf("当前快照提供的严格 full URL 未被识别：%q", parsed)
	}
	for _, rejected := range []string{
		"http://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=x",
		"https://novac2c.cdn.weixin.qq.com.evil.invalid/c2c/download?encrypted_query_param=x",
		"https://novac2c.cdn.weixin.qq.com:443/c2c/download?encrypted_query_param=x",
		"https://user@novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=x",
		"https://novac2c.cdn.weixin.qq.com/c2c/other?encrypted_query_param=x",
		"https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=x&next=https://evil.invalid",
		"https://novac2c.cdn.weixin.qq.com/c2c/download#encrypted_query_param=x",
	} {
		if value, ok := parseDirectChatImageURL(rejected); ok || value != "" {
			t.Errorf("越界聊天图片 URL 未被拒绝：%s -> %q", rejected, value)
		}
	}
}

func TestChatImageMessageBindingLengthPrefixesEveryIdentityField(t *testing.T) {
	left := Message{EvidenceID: "a", Chat: "b\x00c", LocalID: 42, SourceDB: "message/message_0.db"}
	right := Message{EvidenceID: "a\x00b", Chat: "c", LocalID: 42, SourceDB: "message/message_0.db"}
	if chatImageMessageBinding(left) == chatImageMessageBinding(right) {
		t.Fatal("消息绑定字段边界发生碰撞")
	}
}

func TestDownloadAndVerifyChatImageCandidateBindsMIMEContainerAndDescriptor(t *testing.T) {
	plain := syntheticChatImagePNG(t, 37, 29)
	candidate := directRecoveryCandidate(t, plain, 37, 29)
	ciphertext := encryptSyntheticChatImageAES128ECBPKCS7(t, plain, candidate.aesKey)
	target, err := url.Parse(candidate.encryptedQueryParameter)
	if err != nil {
		t.Fatal(err)
	}
	message := Message{EvidenceID: "wechat:chat:42", Chat: "chat", LocalID: 42, ServerID: 9002, Timestamp: 1700000000, SortKey: 1700000000000, SourceDB: "message/message_0.db"}
	downloader := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{
		Payload: ciphertext, Bytes: int64(len(ciphertext)), Encrypted: true, ContentType: "application/octet-stream",
	}}
	artifact, err := downloadAndVerifyChatImageCandidate(context.Background(), message, &candidate, target, downloader)
	if err != nil {
		t.Fatalf("严格绑定的 CDN 图片恢复失败：%v", err)
	}
	defer clear(artifact.Data)
	if downloader.calls != 1 || artifact.EvidenceID != message.EvidenceID || artifact.Format != "png" ||
		artifact.Bytes != len(plain) || artifact.Width != 37 || artifact.Height != 29 ||
		artifact.DescriptorBytesStatus != "match" || artifact.DescriptorDimensionsStatus != "match_observation_not_quality_gate" ||
		artifact.DescriptorMD5Status != "match" || artifact.SourceOriginalQualityStatus != "unknown" ||
		artifact.NetworkAccessPerformed != true || artifact.DescriptorSHA256 == "" || artifact.MessageBindingSHA256 == "" {
		t.Fatalf("远端恢复证据异常：%+v", artifact)
	}
}

func TestDownloadAndVerifyChatImageCandidateAcceptsPlainJPEGWithMatchingMIME(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 19, 11)), nil); err != nil {
		t.Fatal(err)
	}
	plain := encoded.Bytes()
	candidate := directRecoveryCandidate(t, plain, 19, 11)
	target, _ := url.Parse(candidate.encryptedQueryParameter)
	message := Message{EvidenceID: "wechat:chat:jpeg", Chat: "chat", LocalID: 45, ServerID: 9005, SourceDB: "message/message_0.db"}
	downloader := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{
		Payload: append([]byte(nil), plain...), Bytes: int64(len(plain)), ContentType: "image/jpeg; charset=binary",
	}}
	artifact, err := downloadAndVerifyChatImageCandidate(context.Background(), message, &candidate, target, downloader)
	if err != nil {
		t.Fatalf("MIME 与完整 JPEG 一致时不应拒绝：%v", err)
	}
	defer clear(artifact.Data)
	if artifact.Format != "jpg" || artifact.DecryptionScope != "not_required" || downloader.calls != 1 {
		t.Fatalf("明文 JPEG 恢复证据异常：%+v calls=%d", artifact, downloader.calls)
	}
}

func TestSafeChatImageDownloaderRejectsEveryRedirect(t *testing.T) {
	downloader := newSafeChatImageDownloader()
	request, err := http.NewRequest(http.MethodGet, "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := downloader.client.CheckRedirect(request, []*http.Request{request}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("聊天图片下载器没有拒绝重定向：%v", err)
	}
	transport, ok := downloader.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.DisableKeepAlives != true || transport.DisableCompression != true {
		t.Fatalf("聊天图片网络边界发生漂移：%T %+v", downloader.client.Transport, transport)
	}
}

func TestDownloadAndVerifyChatImageCandidateRejectsForgedMIMEAndDescriptorMismatch(t *testing.T) {
	plain := syntheticChatImagePNG(t, 31, 23)
	candidate := directRecoveryCandidate(t, plain, 31, 23)
	ciphertext := encryptSyntheticChatImageAES128ECBPKCS7(t, plain, candidate.aesKey)
	target, _ := url.Parse(candidate.encryptedQueryParameter)
	message := Message{EvidenceID: "wechat:chat:43", Chat: "chat", LocalID: 43, ServerID: 9003, SourceDB: "message/message_0.db"}

	forgedMIME := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{
		Payload: append([]byte(nil), ciphertext...), Bytes: int64(len(ciphertext)), Encrypted: true, ContentType: "image/png",
	}}
	_, err := downloadAndVerifyChatImageCandidate(context.Background(), message, &candidate, target, forgedMIME)
	var recoveryErr *ChatImageRemoteRecoveryError
	if !errors.As(err, &recoveryErr) || recoveryErr.Kind != "chat_image_remote_mime_mismatch" || !recoveryErr.NetworkAttempted {
		t.Fatalf("伪造 MIME 未被拒绝：%T %v", err, err)
	}

	mismatched := candidate
	mismatched.expectedMD5 = strings.Repeat("ff", 16)
	descriptorMismatch := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{
		Payload: append([]byte(nil), ciphertext...), Bytes: int64(len(ciphertext)), Encrypted: true, ContentType: "application/octet-stream",
	}}
	_, err = downloadAndVerifyChatImageCandidate(context.Background(), message, &mismatched, target, descriptorMismatch)
	if !errors.As(err, &recoveryErr) || recoveryErr.Kind != "chat_image_remote_descriptor_md5_mismatch" ||
		recoveryErr.DescriptorExpiryStatus != "response_unverified" {
		t.Fatalf("描述符错配未被拒绝：%T %v", err, err)
	}
}

func TestDownloadAndVerifyChatImageCandidateClassifiesOversizeInterruptionAndExpiry(t *testing.T) {
	plain := syntheticChatImagePNG(t, 17, 13)
	candidate := directRecoveryCandidate(t, plain, 17, 13)
	target, _ := url.Parse(candidate.encryptedQueryParameter)
	message := Message{EvidenceID: "wechat:chat:44", Chat: "chat", LocalID: 44, ServerID: 9004, SourceDB: "message/message_0.db"}
	cases := []struct {
		name, downloadKind, wantKind, wantExpiry string
	}{
		{name: "oversized", downloadKind: "response_size_invalid", wantKind: "chat_image_remote_response_size_invalid", wantExpiry: "response_unverified"},
		{name: "interrupted", downloadKind: "response_read_failed", wantKind: "chat_image_remote_response_read_failed", wantExpiry: "unknown_after_request_failure"},
		{name: "redirect", downloadKind: "redirect_rejected", wantKind: "chat_image_remote_redirect_rejected", wantExpiry: "unknown_after_request_failure"},
		{name: "authorization_rejected", downloadKind: "authorization_rejected", wantKind: "chat_image_remote_authorization_rejected", wantExpiry: "unknown_unavailable_at_request_time"},
		{name: "resource_unavailable", downloadKind: "resource_unavailable", wantKind: "chat_image_remote_resource_unavailable", wantExpiry: "unknown_unavailable_at_request_time"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			downloader := &fakeChatImageQualificationDownloader{err: &momentDownloadError{Kind: testCase.downloadKind}}
			_, err := downloadAndVerifyChatImageCandidate(context.Background(), message, &candidate, target, downloader)
			var recoveryErr *ChatImageRemoteRecoveryError
			if !errors.As(err, &recoveryErr) || recoveryErr.Kind != testCase.wantKind ||
				recoveryErr.DescriptorExpiryStatus != testCase.wantExpiry || !recoveryErr.NetworkAttempted || downloader.calls != 1 {
				t.Fatalf("下载失败分类异常：err=%T %v calls=%d", err, err, downloader.calls)
			}
		})
	}
}

func TestDownloadAndVerifyChatImageCandidateDoesNotClaimNetworkForRequestBuildFailure(t *testing.T) {
	plain := syntheticChatImagePNG(t, 13, 11)
	candidate := directRecoveryCandidate(t, plain, 13, 11)
	target, _ := url.Parse(candidate.encryptedQueryParameter)
	message := Message{EvidenceID: "wechat:chat:request-build", Chat: "chat", LocalID: 46, SourceDB: "message/message_0.db"}
	downloader := &fakeChatImageQualificationDownloader{err: &momentDownloadError{Kind: "request_build_failed"}}
	_, err := downloadAndVerifyChatImageCandidate(context.Background(), message, &candidate, target, downloader)
	var recoveryErr *ChatImageRemoteRecoveryError
	if !errors.As(err, &recoveryErr) || recoveryErr.Kind != "chat_image_remote_request_build_failed" ||
		recoveryErr.NetworkAttempted || recoveryErr.DescriptorExpiryStatus != "not_evaluated" || downloader.calls != 1 {
		t.Fatalf("构造请求失败错误地声称发生了网络访问：err=%T %v calls=%d", err, err, downloader.calls)
	}
}
