package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/wxgfqual"
)

func helperPNG(t *testing.T, seed uint8) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			value.SetRGBA(x, y, color.RGBA{R: seed + uint8(x*11), G: uint8(y * 23), B: 0x80, A: 0xff})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func helperSHA(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func withPrivateRootStub(t *testing.T) {
	t.Helper()
	previous := validatePrivateReviewRoot
	validatePrivateReviewRoot = func(string) error { return nil }
	t.Cleanup(func() { validatePrivateReviewRoot = previous })
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareCreatesBoundOfflineReviewBundle(t *testing.T) {
	withPrivateRootStub(t)
	root := t.TempDir()
	decoded, reference := helperPNG(t, 1), helperPNG(t, 2)
	if err := os.WriteFile(filepath.Join(root, "decoded-01.png"), decoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reference-01.png"), reference, 0o600); err != nil {
		t.Fatal(err)
	}
	decodedReview, err := wxgfqual.InspectVisualReviewPNG(decoded)
	if err != nil {
		t.Fatal(err)
	}
	capture := wxgfqual.VisualReviewCapture{
		Protocol: wxgfqual.VisualReviewCaptureProtocol, GenerationID: "generation-one",
		SnapshotManifestSHA256: helperSHA([]byte("manifest")), PrivateOnly: true,
		ReportedDecoder:                  wxgfqual.VisualReviewDecoder,
		ReportedDecoderVersion:           "sha256:" + helperSHA([]byte("ffmpeg-build")),
		DecoderIdentityBasis:             wxgfqual.VisualReviewDecoderIdentityBasis,
		ProviderProtocol:                 wxgfqual.ProviderProtocol,
		ProviderIdentityManifestProtocol: wxgfqual.ProviderIdentityManifestProtocol,
		ProviderIdentityManifestSHA256:   helperSHA([]byte("identity-manifest")),
		ProviderSHA256:                   helperSHA([]byte("provider-build")),
		ProviderSourceStatus:             wxgfqual.ProviderSourceStatus,
		DecoderSourceStatus:              wxgfqual.DecoderSourceStatus,
		DecoderDistributionLicenseStatus: wxgfqual.DecoderDistributionLicenseStatus,
		ProviderBinaryTrustStatus:        wxgfqual.VisualReviewProviderBinaryTrustStatus,
		ContainsEvidenceIDs:              true, ContainsContentDigests: true,
		Samples: []wxgfqual.VisualReviewCaptureSample{{
			Ordinal: 1, EvidenceID: "wechat:private:1", QualityTier: "medium", WXGFBytes: 100,
			QualityTierBasis: wxgfqual.VisualReviewQualityTierBasis,
			WXGFSHA256:       helperSHA([]byte("wxgf")), DecodedSHA256: helperSHA(decoded),
			DecodedWidth: 8, DecodedHeight: 6, DecodedFileName: "decoded-01.png",
			DecodedVisualFingerprint:    decodedReview.VisualFingerprint,
			SourceOriginalQualityStatus: wxgfqual.VisualReviewSourceOriginalQualityStatus,
		}},
	}
	writeJSON(t, filepath.Join(root, "capture.json"), capture)
	request := helperRequest{Protocol: wxgfqual.VisualReviewHelperProtocol, Action: "prepare", ReviewRoot: root}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(bytes.NewReader(payload), &output); err != nil {
		t.Fatal(err)
	}
	var response helperResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "prepared" || len(response.Samples) != 1 || response.Samples[0].Decoded.SHA256 != helperSHA(decoded) ||
		response.Samples[0].Reference.SHA256 != helperSHA(reference) {
		t.Fatalf("视觉复审 helper 响应异常：%+v", response)
	}
	html, err := os.ReadFile(filepath.Join(root, "review-01.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte("Content-Security-Policy")) || bytes.Contains(bytes.ToLower(html), []byte("https://")) {
		t.Fatal("视觉复审 helper 生成的页面越过离线边界")
	}
	if err := os.Remove(filepath.Join(root, "review-01.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "reference-01.png")); err != nil {
		t.Fatal(err)
	}
	inspected, err := prepare(root, false)
	if err != nil || inspected.Status != "inspected" || len(inspected.Samples) != 1 {
		t.Fatalf("无参考图清理预检失败：%+v %v", inspected, err)
	}
	if _, err := os.Stat(filepath.Join(root, "review-01.html")); !os.IsNotExist(err) {
		t.Fatal("无参考图清理预检意外生成了 HTML")
	}

	capture.Samples[0].DecodedSHA256 = helperSHA([]byte("wrong"))
	writeJSON(t, filepath.Join(root, "capture.json"), capture)
	if _, err := prepare(root, true); err == nil {
		t.Fatal("视觉复审 helper 接受了未绑定的解码 PNG")
	}
}

func helperRecord(version, tier, suffix string) wxgfqual.VisualReviewRecord {
	digest := func(prefix string) string { return helperSHA([]byte(prefix + "-" + suffix)) }
	return wxgfqual.VisualReviewRecord{
		Protocol: wxgfqual.VisualReviewRecordProtocol, ReviewStatus: "confirmed",
		ReviewMethod: "human_side_by_side_wechat_ui_reference", WeChatVersion: version,
		RunNonce: strings.Repeat("ab", 16), ReviewedAtUTC: "2026-08-29T12:00:00.1234567Z",
		ClientVersionObservation:         "installed_package_at_review",
		SourceProducerVersionStatus:      wxgfqual.VisualReviewSourceProducerVersionStatus,
		ReportedDecoder:                  wxgfqual.VisualReviewDecoder,
		ReportedDecoderVersion:           "sha256:" + helperSHA([]byte("ffmpeg-build")),
		DecoderIdentityBasis:             wxgfqual.VisualReviewDecoderIdentityBasis,
		ProviderProtocol:                 wxgfqual.ProviderProtocol,
		ProviderIdentityManifestProtocol: wxgfqual.ProviderIdentityManifestProtocol,
		ProviderIdentityManifestSHA256:   helperSHA([]byte("identity-manifest")),
		ProviderSHA256:                   helperSHA([]byte("provider-build")),
		ProviderSourceStatus:             wxgfqual.ProviderSourceStatus,
		DecoderSourceStatus:              wxgfqual.DecoderSourceStatus,
		DecoderDistributionLicenseStatus: wxgfqual.DecoderDistributionLicenseStatus,
		ProviderBinaryTrustStatus:        wxgfqual.VisualReviewProviderBinaryTrustStatus,
		QualityTier:                      tier, QualityTierBasis: wxgfqual.VisualReviewQualityTierBasis,
		EvidenceID: "wechat:private:" + suffix, GenerationID: "generation-" + suffix,
		SnapshotManifestSHA256: digest("manifest"), WXGFSHA256: digest("wxgf"),
		DecodedSHA256: digest("decoded"), DecodedVisualFingerprint: digest("visual")[:16], ReferenceSHA256: digest("reference"),
		DecodedWidth: 640, DecodedHeight: 480, ReferenceWidth: 320, ReferenceHeight: 240,
		SameContentConfirmed: true, OrientationConfirmed: true, CropConfirmed: true,
		ColorAndArtifactsConfirmed: true, TemporaryDecodedRemoved: true,
		SourceOriginalQualityStatus: wxgfqual.VisualReviewSourceOriginalQualityStatus,
		TemporaryReferenceRemoved:   true, TemporaryReviewBundleRemoved: true,
	}
}

func TestEvaluateMatrixReturnsOnlySanitizedCoverage(t *testing.T) {
	withPrivateRootStub(t)
	root := t.TempDir()
	paths := []string{}
	for index, record := range []wxgfqual.VisualReviewRecord{
		helperRecord("4.1.12.55", "medium", "one"), helperRecord("4.1.12.55", "high", "two"),
		helperRecord("4.2.0.1", "medium", "three"), helperRecord("4.2.0.1", "high", "four"),
	} {
		directory := filepath.Join(root, "run-"+string(rune('a'+index)))
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "record.json")
		writeJSON(t, path, record)
		paths = append(paths, path)
	}
	legacyDirectory := filepath.Join(root, "run-legacy")
	if err := os.Mkdir(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := helperRecord("4.0.0.0", "medium", "legacy")
	legacy.Protocol = wxgfqual.VisualReviewLegacyRecordProtocol
	legacy.DecoderIdentityBasis = "provider_reported_adjacent_decoder_sha256_unattested_provider"
	legacy.ProviderProtocol = "v-local-cli-image-decoder/1"
	legacyPath := filepath.Join(legacyDirectory, "record.json")
	writeJSON(t, legacyPath, legacy)
	paths = append(paths, legacyPath)
	request := helperRequest{
		Protocol: wxgfqual.VisualReviewHelperProtocol, Action: "evaluate_matrix",
		RecordRoot: root, RecordPaths: paths, ReportedDecoder: wxgfqual.VisualReviewDecoder,
		ReportedDecoderVersion:         "sha256:" + helperSHA([]byte("ffmpeg-build")),
		ProviderIdentityManifestSHA256: helperSHA([]byte("identity-manifest")),
		ProviderSHA256:                 helperSHA([]byte("provider-build")),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(bytes.NewReader(payload), &output); err != nil {
		t.Fatal(err)
	}
	var response helperResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Matrix == nil || response.Matrix.Status != "pass" || response.Matrix.DistinctWXGFSamples != 4 ||
		response.Matrix.LegacyRecordsExcluded != 1 {
		t.Fatalf("视觉复审矩阵 helper 结果异常：%+v", response)
	}
	for _, private := range []string{"wechat:private:one", helperRecord("4.1.12.55", "medium", "one").WXGFSHA256} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("视觉复审矩阵响应泄露私有字段 %q", private)
		}
	}

	request.RecordPaths = append(request.RecordPaths, request.RecordPaths[0])
	payload, _ = json.Marshal(request)
	if err := run(bytes.NewReader(payload), &bytes.Buffer{}); err == nil {
		t.Fatal("视觉复审矩阵 helper 接受了重复记录路径")
	}

	legacy.ReviewStatus = "not_confirmed"
	writeJSON(t, legacyPath, legacy)
	request.RecordPaths = paths
	payload, _ = json.Marshal(request)
	if err := run(bytes.NewReader(payload), &bytes.Buffer{}); err == nil {
		t.Fatal("视觉复审矩阵 helper 把无效 v1 标记计入 legacy_records_excluded")
	}
}

func TestRunRejectsUnknownRequestFields(t *testing.T) {
	withPrivateRootStub(t)
	payload := `{"protocol":"` + wxgfqual.VisualReviewHelperProtocol + `","action":"prepare","review_root":"C:\\\\private","unknown":true}`
	if err := run(strings.NewReader(payload), &bytes.Buffer{}); err == nil {
		t.Fatal("视觉复审 helper 接受了未知请求字段")
	}
}
