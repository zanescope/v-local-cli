package store

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fakeMomentDownloader struct {
	response momentRemoteResponse
	target   *url.URL
	maxBytes int64
	calls    int
	err      error
}

func testLargeJPEG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 600, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 600; x++ {
			value.Set(x, y, color.RGBA{R: byte(x*17 + y*31), G: byte(x*47 + y*13), B: byte(x*7 + y*61), A: 0xff})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, value, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	if output.Len() <= 128*1024 {
		t.Fatalf("大图片测试样本过小：%d", output.Len())
	}
	return output.Bytes()
}

func (downloader *fakeMomentDownloader) Download(_ context.Context, target *url.URL, maxBytes int64) (momentRemoteResponse, error) {
	downloader.calls++
	downloader.target = target
	downloader.maxBytes = maxBytes
	if downloader.err != nil {
		return momentRemoteResponse{}, downloader.err
	}
	return downloader.response, nil
}

func testMP4Box(name string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], name)
	copy(result[8:], payload)
	return result
}

func testMomentVideo() []byte {
	ftyp := testMP4Box("ftyp", []byte("isom\x00\x00\x02\x00isommp41"))
	moov := testMP4Box("moov", nil)
	mediaData := make([]byte, 160*1024)
	for index := range mediaData {
		mediaData[index] = byte(index*29 + 17)
	}
	return append(append(ftyp, moov...), testMP4Box("mdat", mediaData)...)
}

func createMomentExportFixture(t *testing.T, root string, payload []byte, seed, token string) string {
	t.Helper()
	digest := contentMD5(payload)
	xml := `<SnsDataItem><TimelineObject><id>29</id><username>wxid_author</username><createTime>1700000000</createTime>` +
		`<contentDesc>export fixture</contentDesc><ContentObject><type>1</type></ContentObject></TimelineObject>` +
		`<LocalExtraInfo><comment_user_list><user_comment><comment_id>301</comment_id><comment_64id>3001</comment_64id>` +
		`<type>2</type><username>wxid_commenter</username><nickname>commenter</nickname><create_time>1700000020</create_time>` +
		`<content>image comment</content><comment_imageinfo_count>1</comment_imageinfo_count><comment_emojiinfo_count>0</comment_emojiinfo_count>` +
		`<imagelist><imageinfo><media_id>` + digest + `</media_id><md5>` + digest + `</md5>` +
		`<url>http://szmmsns.qpic.cn/sns/resource/0?token=embedded-token-value&amp;unused=public</url><token>` + token + `</token><key>` + seed + `</key>` +
		`<enc_idx>1</enc_idx><file_size>` + strconv.Itoa(len(payload)) + `</file_size></imageinfo></imagelist>` +
		`</user_comment></comment_user_list></LocalExtraInfo></SnsDataItem>`
	path := filepath.Join(root, "sns", "sns.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path,
		"CREATE TABLE SnsTimeLine(tid INTEGER, user_name TEXT, content BLOB, pack_info_buf BLOB)",
		"INSERT INTO SnsTimeLine VALUES(29,'wxid_author','"+strings.ReplaceAll(xml, "'", "''")+"',X'00')",
	)
	return "moment:wxid_author:29:interaction:comment:3001:media:1"
}

func createMomentVideoExportFixture(t *testing.T, root string, payload []byte, seed, token string) string {
	t.Helper()
	digest := contentMD5(payload)
	xml := `<SnsDataItem><TimelineObject><id>31</id><username>wxid_author</username><createTime>1700000000</createTime>` +
		`<contentDesc>video export fixture</contentDesc><enc key="` + seed + `"/><ContentObject><type>15</type><mediaList><media><SnsDataItem>` +
		`<id>` + digest + `</id><type>6</type><videoDuration>12</videoDuration><url token="` + token + `" md5="` + digest + `">` +
		`http://snsvideodownload-a.tc.qq.com/sns/video/sample.mp4?quality=source</url><file_size>` + strconv.Itoa(len(payload)) + `</file_size>` +
		`</SnsDataItem></media></mediaList></ContentObject></TimelineObject></SnsDataItem>`
	path := filepath.Join(root, "sns", "sns.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path,
		"CREATE TABLE SnsTimeLine(tid INTEGER, user_name TEXT, content BLOB, pack_info_buf BLOB)",
		"INSERT INTO SnsTimeLine VALUES(31,'wxid_author','"+strings.ReplaceAll(xml, "'", "''")+"',X'00')",
	)
	return "moment:wxid_author:31:media:1"
}

func TestMomentMediaEvidenceIDDoesNotExposeRemoteSecrets(t *testing.T) {
	root := t.TempDir()
	payload := testPNG("remote")
	secretToken := "secret-token-value"
	evidenceID := createMomentExportFixture(t, root, payload, "12345", secretToken)
	report, err := Moments(root, "wxid_author", nil, nil, 10)
	if err != nil || len(report.Items) != 1 || len(report.Items[0].Interactions.Comments) != 1 {
		t.Fatalf("朋友圈评论媒体读取失败：report=%+v err=%v", report, err)
	}
	media := report.Items[0].Interactions.Comments[0].Media[0]
	if media.EvidenceID != evidenceID {
		t.Fatalf("媒体证据标识异常：%q", media.EvidenceID)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretToken) || strings.Contains(string(encoded), "embedded-token-value") || strings.Contains(string(encoded), "unused=public") || strings.Contains(string(encoded), `"Key"`) || strings.Contains(string(encoded), `"Token"`) {
		t.Fatal("结构化输出泄漏了远端令牌或密钥")
	}
}

func TestExportMomentMediaRequiresAuthorizationThenDecryptsRemoteImage(t *testing.T) {
	root := t.TempDir()
	payload := testPNG("remote-encrypted")
	seedText := "12345"
	evidenceID := createMomentExportFixture(t, root, payload, seedText, "secret-token-value")
	options := MomentMediaExportOptions{MomentMediaOptions: MomentMediaOptions{AccountPath: t.TempDir()}}
	_, err := exportMomentMediaWithDownloader(context.Background(), root, evidenceID, options, &fakeMomentDownloader{})
	var exportErr *MomentMediaExportError
	if !errors.As(err, &exportErr) || exportErr.Kind != "moment_media_network_authorization_required" {
		t.Fatalf("缺少联网授权时的错误异常：%v", err)
	}
	seed := uint64(12345)
	stream := isaac64Keystream(seed, len(payload))
	encrypted := make([]byte, len(payload))
	for index := range payload {
		encrypted[index] = payload[index] ^ stream[index]
	}
	downloader := &fakeMomentDownloader{response: momentRemoteResponse{Payload: encrypted, Encrypted: true}}
	options.AllowNetwork = true
	artifact, err := exportMomentMediaWithDownloader(context.Background(), root, evidenceID, options, downloader)
	if err != nil {
		t.Fatal(err)
	}
	if string(artifact.Data) != string(payload) || artifact.ResolutionStatus != "verified_remote_download" || artifact.ContainerValidation != "full_decode" || artifact.DecryptionScope != "full_payload" || !artifact.NetworkAccessPerformed {
		t.Fatalf("远端图片导出异常：%+v", artifact)
	}
	if downloader.calls != 1 || downloader.target == nil || downloader.target.Scheme != "https" || downloader.target.Hostname() != "szmmsns.qpic.cn" || downloader.target.Query().Get("idx") != "1" || downloader.target.Query().Get("token") == "" || downloader.target.Query().Get("unused") != "" {
		t.Fatal("远端请求未按受限 HTTPS 契约构造")
	}
	options.AllowNetwork = false
	_, err = exportMomentMediaWithDownloader(context.Background(), root, evidenceID, options, downloader)
	if !errors.As(err, &exportErr) || exportErr.Kind != "moment_media_network_authorization_required" || downloader.calls != 1 {
		t.Fatalf("联网授权被复用到后续调用：err=%v calls=%d", err, downloader.calls)
	}
}

func TestRemoteMomentImageDecryptsEntireLargePayload(t *testing.T) {
	payload := testLargeJPEG(t)
	seed := uint64(123456)
	stream := isaac64Keystream(seed, len(payload))
	ciphertext := append([]byte(nil), payload...)
	for index := range ciphertext {
		ciphertext[index] ^= stream[index]
	}
	media := MomentMedia{EvidenceID: "moment:test:1:media:1", Kind: "image"}
	artifact, err := decodeRemoteMomentImage(media, momentRemoteVariant{Key: strconv.FormatUint(seed, 10)}, momentRemoteResponse{
		Payload: ciphertext, Encrypted: true,
	})
	if err != nil || artifact.DecryptionScope != "full_payload" || artifact.ContainerValidation != "full_decode" || !bytes.Equal(artifact.Data, payload) {
		t.Fatalf("完整加密图片解密异常：artifact=%+v err=%v", artifact, err)
	}
}

func TestExportMomentVideoUsesOuterKeyAndDecryptsPrefix(t *testing.T) {
	root := t.TempDir()
	payload := testMomentVideo()
	seedText := "246810"
	evidenceID := createMomentVideoExportFixture(t, root, payload, seedText, "video-token-value")
	report, err := Moments(root, "wxid_author", nil, nil, 10)
	if err != nil || len(report.Items) != 1 || len(report.Items[0].Media) != 1 {
		t.Fatalf("视频媒体解析异常：report=%+v err=%v", report, err)
	}
	media := report.Items[0].Media[0]
	if media.Kind != "video" || media.remote.Original.Key != seedText {
		t.Fatalf("视频外层密钥未绑定到媒体描述符：%+v", media)
	}
	seed, _ := strconv.ParseUint(seedText, 10, 64)
	ciphertext := append([]byte(nil), payload...)
	stream := isaac64Keystream(seed, 128*1024)
	for index := range stream {
		ciphertext[index] ^= stream[index]
	}
	downloader := &fakeMomentDownloader{response: momentRemoteResponse{Payload: ciphertext, Encrypted: true}}
	artifact, err := exportMomentMediaWithDownloader(context.Background(), root, evidenceID, MomentMediaExportOptions{
		MomentMediaOptions: MomentMediaOptions{AccountPath: t.TempDir()}, AllowNetwork: true,
	}, downloader)
	if err != nil {
		t.Fatal(err)
	}
	exported, readErr := os.ReadFile(artifact.Path)
	defer os.Remove(artifact.Path)
	if readErr != nil || !artifact.RemoveAfterRead || artifact.Kind != "video" || artifact.Format != "mp4" || artifact.DecryptionScope != "prefix_131072" || artifact.ContainerValidation != "iso_bmff_top_level_boxes" || !bytes.Equal(exported, payload) {
		t.Fatalf("朋友圈视频导出异常：artifact=%+v", artifact)
	}
	if downloader.maxBytes != maxMomentVideoBytes || downloader.target == nil || downloader.target.Hostname() != "snsvideodownload-a.tc.qq.com" || !strings.HasPrefix(downloader.target.RawQuery, "token=video-token-value&idx=1") || downloader.target.Query().Get("quality") != "source" {
		t.Fatalf("朋友圈视频下载契约异常：target=%v max=%d", downloader.target, downloader.maxBytes)
	}
}

func TestTimelineVideoKeyRejectsConflictingOuterKeys(t *testing.T) {
	root, parseStatus := parseXML(`<TimelineObject><enc key="123"/><nested><enc key="456"/></nested></TimelineObject>`, false)
	if parseStatus != "" || root == nil {
		t.Fatalf("测试 XML 解析失败：%s", parseStatus)
	}
	if key := timelineVideoKey(root); key != "" {
		t.Fatalf("冲突的视频密钥未被拒绝：%s", key)
	}
}

func TestReadLocalMomentVideoRequiresCompleteMP4(t *testing.T) {
	payload := testMomentVideo()
	path := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := contentMD5(payload)
	media := MomentMedia{
		EvidenceID: "moment:test:1:media:1", Kind: "video", ResolutionStatus: "verified_local",
		Local: &MomentLocalMedia{Path: path, Cipher: "plain", SourceMD5: digest, ContentMD5: digest, VerifiedBy: "source_file_md5"},
	}
	artifact, err := readLocalMomentMedia(media, MomentMediaOptions{})
	if err != nil || artifact.Kind != "video" || artifact.Format != "mp4" || artifact.ContainerValidation != "iso_bmff_top_level_boxes" || artifact.DecryptionScope != "local_cache" {
		t.Fatalf("本地朋友圈视频导出异常：artifact=%+v err=%v", artifact, err)
	}
	if err := os.WriteFile(path, payload[:len(payload)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	media.Local.SourceMD5 = ""
	media.Local.ContentMD5 = ""
	if _, err := readLocalMomentMedia(media, MomentMediaOptions{}); err == nil {
		t.Fatal("截断的本地 MP4 未被拒绝")
	}
}

func TestExportMomentMediaPrefersVerifiedLocalImage(t *testing.T) {
	root := t.TempDir()
	payload := testPNG("local")
	evidenceID := createMomentExportFixture(t, root, payload, "12345", "secret-token-value")
	account := t.TempDir()
	path := filepath.Join(account, "cache", contentMD5(payload)+".png")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	downloader := &fakeMomentDownloader{}
	artifact, err := exportMomentMediaWithDownloader(context.Background(), root, evidenceID, MomentMediaExportOptions{
		MomentMediaOptions: MomentMediaOptions{AccountPath: account}, AllowNetwork: true,
	}, downloader)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Source != "local_cache" || artifact.NetworkAccessPerformed || downloader.calls != 0 || string(artifact.Data) != string(payload) {
		t.Fatalf("本地优先导出异常：%+v calls=%d", artifact, downloader.calls)
	}
}

func TestExportMomentMediaDoesNotLeakDownloaderError(t *testing.T) {
	root := t.TempDir()
	payload := testPNG("remote-error")
	secretToken := "secret-token-value"
	evidenceID := createMomentExportFixture(t, root, payload, "12345", secretToken)
	downloader := &fakeMomentDownloader{err: errors.New("request failed: https://example.invalid/?token=" + secretToken)}
	_, err := exportMomentMediaWithDownloader(context.Background(), root, evidenceID, MomentMediaExportOptions{
		MomentMediaOptions: MomentMediaOptions{AccountPath: t.TempDir()}, AllowNetwork: true,
	}, downloader)
	var exportErr *MomentMediaExportError
	if !errors.As(err, &exportErr) || exportErr.Kind != "moment_media_download_failed" || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("下载错误未被安全归一化：%v", err)
	}
}

func TestSafeMomentDownloaderClassifiesHTTPErrorWithoutLeakingTarget(t *testing.T) {
	secretToken := "secret-live-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/image?token=" + secretToken)
	if err != nil {
		t.Fatal(err)
	}
	downloader := &safeMomentDownloader{client: server.Client()}
	_, err = downloader.Download(context.Background(), target, maxMomentImageBytes)
	var downloadErr *momentDownloadError
	if !errors.As(err, &downloadErr) || downloadErr.Kind != "authorization_rejected" || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("下载错误分类不安全：%v", err)
	}
}

func TestMomentMediaURLAndAddressValidation(t *testing.T) {
	accepted, err := buildMomentMediaURL(momentRemoteVariant{URL: "http://szmmsns.qpic.cn/sns/resource/0", Token: "token"}, "image")
	if err != nil || accepted.Scheme != "https" || accepted.Query().Get("token") != "token" {
		t.Fatalf("合法图片地址被拒绝：url=%v err=%v", accepted, err)
	}
	for _, rejected := range []string{
		"https://127.0.0.1/image",
		"https://szmmsns.qpic.cn.evil.example/image",
		"https://user@szmmsns.qpic.cn/image",
		"https://szmmsns.qpic.cn:8443/image",
	} {
		if _, err := buildMomentMediaURL(momentRemoteVariant{URL: rejected, Token: "token"}, "image"); err == nil {
			t.Fatalf("不安全图片地址未被拒绝：%s", rejected)
		}
	}
	video, err := buildMomentMediaURL(momentRemoteVariant{
		URL: "http://snsvideodownload-a.tc.qq.com/sns/video/file.mp4?quality=source&token=old", Token: "new-token",
	}, "video")
	if err != nil || video.Scheme != "https" || !strings.HasPrefix(video.RawQuery, "token=new-token&idx=1") || video.Query().Get("quality") != "source" {
		t.Fatalf("合法视频地址处理异常：url=%v err=%v", video, err)
	}
	currentVideo, err := buildMomentMediaURL(momentRemoteVariant{
		URL: "http://szzjwxsns.video.qq.com/102/20202/snsvideodownload", Token: "new-token",
	}, "video")
	if err != nil || currentVideo.Scheme != "https" || currentVideo.Hostname() != "szzjwxsns.video.qq.com" || currentVideo.Query().Get("token") != "new-token" {
		t.Fatalf("当前朋友圈视频地址处理异常：url=%v err=%v", currentVideo, err)
	}
	for _, rejected := range []string{
		"https://snsvideodownload-a.tc.qq.com.evil.example/file.mp4",
		"https://vweixinthumb-a.tc.qq.com/file.mp4",
		"https://snsvideodownload-a.tc.qq.com:8443/file.mp4",
		"https://szzjwxsns.video.qq.com.evil.example/102/20202/snsvideodownload",
		"https://ordinary.video.qq.com/102/20202/snsvideodownload",
		"https://szzjwxsns.video.qq.com/102/20202/other",
		"https://-wxsns.video.qq.com/102/20202/snsvideodownload",
	} {
		if _, err := buildMomentMediaURL(momentRemoteVariant{URL: rejected, Token: "token"}, "video"); err == nil {
			t.Fatalf("不安全视频地址未被拒绝：%s", rejected)
		}
	}
	for _, rejected := range []string{
		"127.0.0.1", "10.0.0.1", "169.254.1.1", "192.88.99.1", "203.0.113.10",
		"::1", "64:ff9b:1::1", "100::1", "100:0:0:1::1", "2001:db8::1", "3fff::1", "5f00::1", "fc00::1",
	} {
		if publicRemoteIP(net.ParseIP(rejected)) {
			t.Fatalf("非公网地址未被拒绝：%s", rejected)
		}
	}
	if kind := rejectedRemoteIPKind(net.ParseIP("198.18.0.1")); kind != "synthetic_proxy_address" {
		t.Fatalf("代理合成地址分类错误：%s", kind)
	}
	if !syntheticProxyAddressesOnly([]net.IPAddr{{IP: net.ParseIP("198.18.0.1")}}) ||
		syntheticProxyAddressesOnly([]net.IPAddr{{IP: net.ParseIP("198.18.0.1")}, {IP: net.ParseIP("8.8.8.8")}}) {
		t.Fatal("fake-IP 回退条件判断错误")
	}
	if !publicRemoteIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("公网测试地址被错误拒绝")
	}
}

func TestRemoteMomentImageRecordsUnstableDescriptorMetadata(t *testing.T) {
	payload := testPNG("metadata-mismatch")
	media := MomentMedia{EvidenceID: "moment:test:1:media:1", Kind: "image"}
	artifact, err := decodeRemoteMomentImage(media, momentRemoteVariant{
		ExpectedMD5: strings.Repeat("0", 32), ExpectedBytes: int64(len(payload) + 1),
	}, momentRemoteResponse{Payload: payload})
	if err != nil || artifact.DescriptorMD5Status != "not_content_digest" || artifact.DescriptorSizeStatus != "mismatch" || artifact.VerifiedBy != "trusted_cdn_tls_token_and_plaintext_container" {
		t.Fatalf("不稳定描述符元数据未被正确记录：artifact=%+v err=%v", artifact, err)
	}
}

func TestRemoteMomentImageRejectsTruncatedKnownHeader(t *testing.T) {
	media := MomentMedia{EvidenceID: "moment:test:1:media:1", Kind: "image"}
	for _, payload := range [][]byte{
		{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10},
		[]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
	} {
		_, err := decodeRemoteMomentImage(media, momentRemoteVariant{}, momentRemoteResponse{Payload: payload})
		var exportErr *MomentMediaExportError
		if !errors.As(err, &exportErr) || exportErr.Kind != "moment_media_verify_failed_container" {
			t.Fatalf("截断容器未被拒绝：%v", err)
		}
	}
}

func TestRemoteMomentImagePrefersStrongDigestOverUnstableSize(t *testing.T) {
	payload := testPNG("digest-over-size")
	digest := md5.Sum(payload)
	media := MomentMedia{EvidenceID: "moment:test:1:media:1", Kind: "image"}
	artifact, err := decodeRemoteMomentImage(media, momentRemoteVariant{
		ExpectedMD5: hex.EncodeToString(digest[:]), ExpectedBytes: int64(len(payload) + 1),
	}, momentRemoteResponse{Payload: payload})
	if err != nil || artifact.VerifiedBy != "plaintext_md5" {
		t.Fatalf("强摘要未覆盖不稳定长度元数据：artifact=%+v err=%v", artifact, err)
	}
}

func TestRemoteMomentImageAcceptsCiphertextDigestWithPlaintextContainer(t *testing.T) {
	payload := testPNG("ciphertext-digest")
	seed := uint64(123456)
	stream := isaac64Keystream(seed, len(payload))
	ciphertext := make([]byte, len(payload))
	for index := range payload {
		ciphertext[index] = payload[index] ^ stream[index]
	}
	digest := md5.Sum(ciphertext)
	media := MomentMedia{EvidenceID: "moment:test:1:media:1", Kind: "image"}
	artifact, err := decodeRemoteMomentImage(media, momentRemoteVariant{
		Key: strconv.FormatUint(seed, 10), ExpectedMD5: hex.EncodeToString(digest[:]),
	}, momentRemoteResponse{Payload: ciphertext, Encrypted: true})
	if err != nil || artifact.VerifiedBy != "ciphertext_md5_and_plaintext_container" || string(artifact.Data) != string(payload) {
		t.Fatalf("密文摘要契约验真异常：artifact=%+v err=%v", artifact, err)
	}
}

func TestSafeMomentDownloaderDisablesAmbientNetworkState(t *testing.T) {
	downloader := newSafeMomentDownloader(t.TempDir())
	transport, ok := downloader.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 || !transport.DisableKeepAlives {
		t.Fatal("朋友圈下载器未禁用环境代理或未固定 TLS 安全配置")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://szmmsns.qpic.cn/redirect", nil)
	if !errors.Is(downloader.client.CheckRedirect(request, nil), http.ErrUseLastResponse) || downloader.client.Jar != nil {
		t.Fatal("朋友圈下载器允许重定向或携带 Cookie")
	}
}
