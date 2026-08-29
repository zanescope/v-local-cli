package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeChatImageQualificationDownloader struct {
	calls    int
	response momentRemoteResponse
	err      error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (downloader *fakeChatImageQualificationDownloader) Download(_ context.Context, _ *url.URL, _ int64) (momentRemoteResponse, error) {
	downloader.calls++
	return downloader.response, downloader.err
}

func syntheticChatImagePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func encryptSyntheticChatImageAES128ECBPKCS7(t *testing.T, plain []byte, key [16]byte) []byte {
	t.Helper()
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+padding)
	copy(padded, plain)
	for index := len(plain); index < len(padded); index++ {
		padded[index] = byte(padding)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += aes.BlockSize {
		block.Encrypt(ciphertext[offset:offset+aes.BlockSize], padded[offset:offset+aes.BlockSize])
	}
	clear(padded)
	return ciphertext
}

func syntheticChatImageDescriptor(t *testing.T, plain []byte, parameter, keyHex string, width, height int) chatImageRemoteDescriptor {
	t.Helper()
	digest := md5.Sum(plain)
	content := `<msg><img cdnbigimgurl="` + parameter + `" aeskey="` + keyHex + `" hdlength="` +
		strconv.Itoa(len(plain)) + `" cdnhdwidth="` + strconv.Itoa(width) + `" cdnhdheight="` +
		strconv.Itoa(height) + `" originsourcemd5="` + hex.EncodeToString(digest[:]) + `" /></msg>`
	descriptor := parseChatImageRemoteDescriptor(content)
	if descriptor.parseStatus != "parsed_unverified_protocol" || len(descriptor.candidates) != 1 {
		descriptor.clear()
		t.Fatalf("合成聊天 CDN 描述符无法进入资格验证：%+v", descriptor)
	}
	return descriptor
}

func syntheticChatImageProtocol(t *testing.T, server *httptest.Server) chatImageQualificationProtocol {
	t.Helper()
	endpoint, err := url.Parse(server.URL + "/download")
	if err != nil {
		t.Fatal(err)
	}
	return chatImageQualificationProtocol{
		name: syntheticChatImageProtocolProfile, endpoint: endpoint,
		expectedHost: endpoint.Hostname(), syntheticFixture: true,
	}
}

func qualificationError(t *testing.T, err error) *chatImageQualificationError {
	t.Helper()
	qualified, ok := err.(*chatImageQualificationError)
	if !ok {
		t.Fatalf("错误类型不是脱敏的资格验证错误：%T %v", err, err)
	}
	return qualified
}

func TestSyntheticChatImageQualificationUsesOneRequestAndStrongBinding(t *testing.T) {
	plain := syntheticChatImagePNG(t, 320, 240)
	parameter := strings.Repeat("ab", 89)
	keyHex := strings.Repeat("01", 16)
	descriptor := syntheticChatImageDescriptor(t, plain, parameter, keyHex, 320, 240)
	defer descriptor.clear()
	ciphertext := encryptSyntheticChatImageAES128ECBPKCS7(t, plain, descriptor.candidates[0].aesKey)
	defer clear(ciphertext)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/download" || request.URL.Query().Get("encrypted_query_param") != parameter || len(request.URL.Query()) != 1 {
			t.Errorf("合成 CDN 请求越界：method=%s target=%s", request.Method, request.URL.RequestURI())
		}
		if len(request.Cookies()) != 0 {
			t.Errorf("合成 CDN 请求携带了 cookie")
		}
		if request.Header.Get("Authorization") != "" || request.Header.Get("Referer") != "" {
			t.Errorf("合成 CDN 请求携带了会话或来源凭据")
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(ciphertext)
	}))
	defer server.Close()
	downloader, err := newSyntheticChatImageQualificationDownloader(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := qualifySyntheticChatImageRemoteCandidate(context.Background(), syntheticChatImageProtocol(t, server), &descriptor.candidates[0], downloader)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(artifact.data)
	digest := sha256.Sum256(plain)
	contentDigest := md5.Sum(plain)
	if calls.Load() != 1 || artifact.protocolProfile != syntheticChatImageProtocolProfile ||
		artifact.protocolStatus != "synthetic_crypto_binding_harness_only" || artifact.networkScope != "literal_loopback_fixture_only" ||
		artifact.tier != "high" || artifact.width != 320 || artifact.height != 240 || artifact.bytes != len(plain) ||
		artifact.sha256 != hex.EncodeToString(digest[:]) || artifact.contentMD5 != hex.EncodeToString(contentDigest[:]) || artifact.containerValidation != "full_decode" ||
		artifact.descriptorBytesStatus != "match" || artifact.descriptorDimensionsStatus != "match_observation_not_quality_gate" ||
		artifact.descriptorMD5Status != "match" || !artifact.networkAccessPerformed || !bytes.Equal(artifact.data, plain) {
		t.Fatalf("合成聊天 CDN 资格验证结果异常：calls=%d artifact=%+v", calls.Load(), artifact)
	}
	if downloader.client.Jar != nil || !errorsIsRedirectRejected(downloader.client) {
		t.Fatal("资格验证客户端没有移除 cookie 或禁止重定向")
	}
	serialized := artifact.protocolStatus + artifact.networkScope + artifact.sha256
	if strings.Contains(serialized, parameter) || strings.Contains(serialized, keyHex) || strings.Contains(serialized, server.URL) {
		t.Fatal("合成资格验证产物泄露请求参数、key 或端点")
	}
}

func errorsIsRedirectRejected(client *http.Client) bool {
	request, _ := http.NewRequest(http.MethodGet, "https://example.invalid/next", nil)
	return client.CheckRedirect(request, nil) == http.ErrUseLastResponse
}

func TestSyntheticChatImageQualificationRejectsRedirectWithoutSecondRequest(t *testing.T) {
	plain := syntheticChatImagePNG(t, 32, 24)
	parameter := strings.Repeat("ab", 89)
	descriptor := syntheticChatImageDescriptor(t, plain, parameter, strings.Repeat("01", 16), 32, 24)
	defer descriptor.clear()
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		http.Redirect(response, request, "/must-not-follow", http.StatusFound)
	}))
	defer server.Close()
	downloader, err := newSyntheticChatImageQualificationDownloader(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), syntheticChatImageProtocol(t, server), &descriptor.candidates[0], downloader)
	qualified := qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_download_failed_redirect_rejected" || calls.Load() != 1 {
		t.Fatalf("重定向没有 fail closed：calls=%d err=%+v", calls.Load(), qualified)
	}
}

func TestSyntheticChatImageQualificationClassifiesExpiryWithoutOverclaim(t *testing.T) {
	plain := syntheticChatImagePNG(t, 32, 24)
	parameter := strings.Repeat("ab", 89)
	cases := []struct {
		name         string
		status       int
		kind         string
		expiryStatus string
	}{
		{name: "forbidden", status: http.StatusForbidden, kind: "chat_image_qualification_download_failed_authorization_rejected", expiryStatus: "unknown_unavailable_at_request_time"},
		{name: "gone", status: http.StatusGone, kind: "chat_image_qualification_download_failed_resource_unavailable", expiryStatus: "unknown_unavailable_at_request_time"},
		{name: "rate_limited", status: http.StatusTooManyRequests, kind: "chat_image_qualification_download_failed_rate_limited", expiryStatus: "not_evidence_of_expiry"},
		{name: "server_error", status: http.StatusInternalServerError, kind: "chat_image_qualification_download_failed_http_status", expiryStatus: "unknown_unavailable_at_request_time"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			descriptor := syntheticChatImageDescriptor(t, plain, parameter, strings.Repeat("01", 16), 32, 24)
			defer descriptor.clear()
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(testCase.status)
			}))
			defer server.Close()
			downloader, err := newSyntheticChatImageQualificationDownloader(server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), syntheticChatImageProtocol(t, server), &descriptor.candidates[0], downloader)
			qualified := qualificationError(t, err)
			if qualified.kind != testCase.kind || qualified.expiryStatus != testCase.expiryStatus || strings.Contains(qualified.Error(), parameter) {
				t.Fatalf("时效分类过度声明：%+v", qualified)
			}
		})
	}
}

func TestSyntheticChatImageQualificationRejectsWrongEvidenceAndNeverEnablesRealEndpoint(t *testing.T) {
	plain := syntheticChatImagePNG(t, 32, 24)
	parameter := strings.Repeat("ab", 89)
	descriptor := syntheticChatImageDescriptor(t, plain, parameter, strings.Repeat("01", 16), 32, 24)
	defer descriptor.clear()
	ciphertext := encryptSyntheticChatImageAES128ECBPKCS7(t, plain, descriptor.candidates[0].aesKey)
	descriptor.candidates[0].expectedMD5 = strings.Repeat("ff", 16)
	downloader := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{Payload: ciphertext, Bytes: int64(len(ciphertext)), Encrypted: true}}
	endpoint, err := url.Parse("https://127.0.0.1/download")
	if err != nil {
		t.Fatal(err)
	}
	protocol := chatImageQualificationProtocol{name: syntheticChatImageProtocolProfile, endpoint: endpoint, expectedHost: endpoint.Hostname(), syntheticFixture: true}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &descriptor.candidates[0], downloader)
	qualified := qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_descriptor_md5_mismatch" || qualified.expiryStatus != "response_unverified" || downloader.calls != 1 {
		t.Fatalf("错误 evidence 没有在一次响应后 fail closed：calls=%d err=%+v", downloader.calls, qualified)
	}

	descriptor.candidates[0].expectedMD5 = ""
	downloader.calls = 0
	protocol.syntheticFixture = false
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &descriptor.candidates[0], downloader)
	qualified = qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_real_endpoint_not_enabled" || downloader.calls != 0 {
		t.Fatalf("真实端点在资格验证阶段被意外启用：calls=%d err=%+v", downloader.calls, qualified)
	}

	candidateWithoutBinding := descriptor.candidates[0]
	candidateWithoutBinding.expectedBytes = 0
	candidateWithoutBinding.expectedWidth = 0
	candidateWithoutBinding.expectedHeight = 0
	candidateWithoutBinding.expectedMD5 = ""
	protocol.syntheticFixture = true
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &candidateWithoutBinding, downloader)
	qualified = qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_descriptor_binding_insufficient" || qualified.expiryStatus != "not_evaluated" || downloader.calls != 0 {
		t.Fatalf("绑定元数据不足时仍发出请求：calls=%d err=%+v", downloader.calls, qualified)
	}
	clear(candidateWithoutBinding.aesKey[:])
}

func TestSyntheticChatImageQualificationRejectsWrongKeySizeAndDimensions(t *testing.T) {
	plain := syntheticChatImagePNG(t, 32, 24)
	parameter := strings.Repeat("ab", 89)
	descriptor := syntheticChatImageDescriptor(t, plain, parameter, strings.Repeat("01", 16), 32, 24)
	defer descriptor.clear()
	endpoint, err := url.Parse("https://127.0.0.1/download")
	if err != nil {
		t.Fatal(err)
	}
	protocol := chatImageQualificationProtocol{name: syntheticChatImageProtocolProfile, endpoint: endpoint, expectedHost: endpoint.Hostname(), syntheticFixture: true}
	validCiphertext := encryptSyntheticChatImageAES128ECBPKCS7(t, plain, descriptor.candidates[0].aesKey)
	defer clear(validCiphertext)
	cases := []struct {
		name     string
		mutate   func(*chatImageRemoteCandidate)
		wantKind string
		kindAlt  string
	}{
		{name: "wrong_key", mutate: func(candidate *chatImageRemoteCandidate) { candidate.aesKey[0] ^= 0xff }, wantKind: "chat_image_qualification_decrypt_failed", kindAlt: "chat_image_qualification_container_invalid"},
		{name: "wrong_size", mutate: func(candidate *chatImageRemoteCandidate) { candidate.expectedBytes++ }, wantKind: "chat_image_qualification_descriptor_size_mismatch"},
		{name: "wrong_dimensions", mutate: func(candidate *chatImageRemoteCandidate) { candidate.expectedWidth++ }, wantKind: "chat_image_qualification_descriptor_dimensions_mismatch"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := descriptor.candidates[0]
			testCase.mutate(&candidate)
			payload := append([]byte(nil), validCiphertext...)
			downloader := &fakeChatImageQualificationDownloader{response: momentRemoteResponse{Payload: payload, Bytes: int64(len(payload)), Encrypted: true}}
			_, err := qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &candidate, downloader)
			qualified := qualificationError(t, err)
			if (qualified.kind != testCase.wantKind && qualified.kind != testCase.kindAlt) || qualified.expiryStatus != "response_unverified" || downloader.calls != 1 {
				t.Fatalf("错误 key/绑定元数据没有 fail closed：calls=%d err=%+v", downloader.calls, qualified)
			}
			clear(candidate.aesKey[:])
		})
	}
}

func TestSyntheticChatImageQualificationRejectsAmbientProxyAndUnsafeEndpoint(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	if _, err := newSyntheticChatImageQualificationDownloader(client); qualificationError(t, err).kind != "chat_image_qualification_proxy_rejected" {
		t.Fatalf("合成资格验证接受了环境代理：%v", err)
	}
	client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	if _, err := newSyntheticChatImageQualificationDownloader(client); qualificationError(t, err).kind != "chat_image_qualification_transport_rejected" {
		t.Fatalf("合成资格验证接受了自定义 transport：%v", err)
	}
	client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	if _, err := newSyntheticChatImageQualificationDownloader(client); qualificationError(t, err).kind != "chat_image_qualification_tls_config_rejected" {
		t.Fatalf("合成资格验证接受了跳过证书校验的 TLS 配置：%v", err)
	}
	plain := syntheticChatImagePNG(t, 32, 24)
	descriptor := syntheticChatImageDescriptor(t, plain, strings.Repeat("ab", 89), strings.Repeat("01", 16), 32, 24)
	defer descriptor.clear()
	endpoint, err := url.Parse("http://synthetic-cdn.invalid/download")
	if err != nil {
		t.Fatal(err)
	}
	protocol := chatImageQualificationProtocol{name: syntheticChatImageProtocolProfile, endpoint: endpoint, expectedHost: endpoint.Hostname(), syntheticFixture: true}
	downloader := &fakeChatImageQualificationDownloader{}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &descriptor.candidates[0], downloader)
	qualified := qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_endpoint_rejected" || downloader.calls != 0 {
		t.Fatalf("非 TLS 端点未在请求前被拒绝：calls=%d err=%+v", downloader.calls, qualified)
	}
	endpoint, err = url.Parse("https://desktop-cdn.invalid/download")
	if err != nil {
		t.Fatal(err)
	}
	protocol = chatImageQualificationProtocol{name: syntheticChatImageProtocolProfile, endpoint: endpoint, expectedHost: endpoint.Hostname(), syntheticFixture: true}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &descriptor.candidates[0], downloader)
	qualified = qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_real_endpoint_not_enabled" || downloader.calls != 0 {
		t.Fatalf("合成 profile 接受了非回环真实端点：calls=%d err=%+v", downloader.calls, qualified)
	}
}

func TestSyntheticChatImageQualificationRejectsOversizedResponseAndKeepsTransportExpiryUnknown(t *testing.T) {
	plain := syntheticChatImagePNG(t, 32, 24)
	parameter := strings.Repeat("ab", 89)
	descriptor := syntheticChatImageDescriptor(t, plain, parameter, strings.Repeat("01", 16), 32, 24)
	defer descriptor.clear()
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", strconv.FormatInt(maxChatImageRemoteResponseBytes+1, 10))
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	downloader, err := newSyntheticChatImageQualificationDownloader(server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), syntheticChatImageProtocol(t, server), &descriptor.candidates[0], downloader)
	qualified := qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_download_failed_response_size_invalid" || qualified.expiryStatus != "response_unverified" {
		t.Fatalf("超限响应分类异常：%+v", qualified)
	}

	transportFailure := &fakeChatImageQualificationDownloader{err: &momentDownloadError{Kind: "connection_failed"}}
	endpoint, err := url.Parse("https://127.0.0.1/download")
	if err != nil {
		t.Fatal(err)
	}
	protocol := chatImageQualificationProtocol{name: syntheticChatImageProtocolProfile, endpoint: endpoint, expectedHost: endpoint.Hostname(), syntheticFixture: true}
	_, err = qualifySyntheticChatImageRemoteCandidate(context.Background(), protocol, &descriptor.candidates[0], transportFailure)
	qualified = qualificationError(t, err)
	if qualified.kind != "chat_image_qualification_download_failed_connection_failed" || qualified.expiryStatus != "unknown_after_request_failure" || transportFailure.calls != 1 {
		t.Fatalf("传输失败被误判成过期：calls=%d err=%+v", transportFailure.calls, qualified)
	}
}
