package wxgfqual

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func visualReviewPNG(t *testing.T, width, height int, base color.RGBA) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := base
			pixel.R ^= uint8(x * 17)
			pixel.G ^= uint8(y * 29)
			value.SetRGBA(x, y, pixel)
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestPrepareVisualReviewBundleRequiresStrictPNGsAndStaysOffline(t *testing.T) {
	reference := visualReviewPNG(t, 7, 5, color.RGBA{R: 0x80, G: 0x40, B: 0x20, A: 0xff})
	decoded := visualReviewPNG(t, 9, 6, color.RGBA{R: 0x10, G: 0x60, B: 0x90, A: 0xff})
	bundle, err := PrepareVisualReviewBundle(reference, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Reference.Width != 7 || bundle.Reference.Height != 5 || bundle.Decoded.Width != 9 || bundle.Decoded.Height != 6 ||
		!validReviewSHA256(bundle.Reference.SHA256) || !validReviewSHA256(bundle.Decoded.SHA256) ||
		!validVisualReviewFingerprint(bundle.Reference.VisualFingerprint) || !validVisualReviewFingerprint(bundle.Decoded.VisualFingerprint) {
		t.Fatalf("视觉复审图片元数据异常：%+v", bundle)
	}
	html := string(bundle.HTML)
	for _, forbidden := range []string{"http://", "https://", "<script", "file://"} {
		if strings.Contains(strings.ToLower(html), forbidden) {
			t.Fatalf("视觉复审页面包含越界能力 %q", forbidden)
		}
	}
	for _, required := range []string{"Content-Security-Policy", "data:image/png;base64,", "内容、方向、裁剪", "颜色/解码伪影", "max-width:none", "不构成色度学证明"} {
		if !strings.Contains(html, required) {
			t.Fatalf("视觉复审页面缺少 %q", required)
		}
	}

	var jpegPayload bytes.Buffer
	if err := jpeg.Encode(&jpegPayload, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareVisualReviewBundle(jpegPayload.Bytes(), decoded); err == nil {
		t.Fatal("视觉复审接受了非 PNG 参考图")
	}
	if _, err := PrepareVisualReviewBundle(append(reference, 0), decoded); err == nil {
		t.Fatal("视觉复审接受了带尾随数据的 PNG")
	}
}

func visualReviewRecord(version, tier, suffix string) VisualReviewRecord {
	digest := func(prefix string) string { return lowercaseSHA256([]byte(prefix + "-" + suffix)) }
	return VisualReviewRecord{
		Protocol: VisualReviewRecordProtocol, ReviewStatus: "confirmed",
		ReviewMethod: "human_side_by_side_wechat_ui_reference",
		RunNonce:     strings.Repeat("ab", 16), ReviewedAtUTC: "2026-08-29T12:00:00.1234567Z",
		WeChatVersion: version, ClientVersionObservation: "installed_package_at_review",
		SourceProducerVersionStatus: VisualReviewSourceProducerVersionStatus,
		ReportedDecoder:             VisualReviewDecoder, ReportedDecoderVersion: "sha256:" + lowercaseSHA256([]byte("ffmpeg-build")),
		DecoderIdentityBasis: VisualReviewDecoderIdentityBasis, ProviderProtocol: ProviderProtocol,
		ProviderIdentityManifestProtocol: ProviderIdentityManifestProtocol,
		ProviderIdentityManifestSHA256:   lowercaseSHA256([]byte("identity-manifest")),
		ProviderSHA256:                   lowercaseSHA256([]byte("provider-build")),
		ProviderSourceStatus:             ProviderSourceStatus,
		DecoderSourceStatus:              DecoderSourceStatus,
		DecoderDistributionLicenseStatus: DecoderDistributionLicenseStatus,
		ProviderBinaryTrustStatus:        VisualReviewProviderBinaryTrustStatus,
		QualityTier:                      tier, QualityTierBasis: VisualReviewQualityTierBasis,
		EvidenceID: "wechat:private:" + suffix, GenerationID: "generation-" + suffix,
		SnapshotManifestSHA256: digest("manifest"), WXGFSHA256: digest("wxgf"),
		DecodedSHA256: digest("decoded"), DecodedVisualFingerprint: digest("visual")[:16], ReferenceSHA256: digest("reference"),
		DecodedWidth: 640, DecodedHeight: 480, ReferenceWidth: 320, ReferenceHeight: 240,
		SameContentConfirmed: true, OrientationConfirmed: true, CropConfirmed: true,
		ColorAndArtifactsConfirmed: true, SourceOriginalQualityStatus: VisualReviewSourceOriginalQualityStatus,
		TemporaryDecodedRemoved: true, TemporaryReferenceRemoved: true, TemporaryReviewBundleRemoved: true,
	}
}

func visualReviewTarget(record VisualReviewRecord) VisualReviewTargetIdentity {
	return VisualReviewTargetIdentity{
		ReportedDecoder: record.ReportedDecoder, ReportedDecoderVersion: record.ReportedDecoderVersion,
		ProviderIdentityManifestSHA256: record.ProviderIdentityManifestSHA256,
		ProviderSHA256:                 record.ProviderSHA256,
	}
}

func TestEvaluateVisualReviewMatrixRequiresTwoVersionTierGrid(t *testing.T) {
	records := []VisualReviewRecord{
		visualReviewRecord("4.1.12.55", "medium", "one"),
		visualReviewRecord("4.1.12.55", "high", "two"),
		visualReviewRecord("4.2.0.1", "medium", "three"),
		visualReviewRecord("4.2.0.1", "high", "four"),
	}
	result, err := EvaluateVisualReviewMatrix(records, visualReviewTarget(records[0]))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "pass" || result.DistinctWXGFSamples != 4 || result.DistinctDecodedVisualFingerprints != 4 ||
		result.DistinctInstalledReviewVersions != 2 || result.InstalledReviewVersionsWithRequiredTierCoverage != 2 ||
		len(result.Blockers) != 0 || result.FixedDimensionQualityGate || result.ProductionReady ||
		result.ContainsEvidenceIDs || result.ContainsImageContentDigests ||
		result.VersionCoverageBasis != VisualReviewVersionCoverageBasis || result.TierCoverageBasis != VisualReviewQualityTierBasis {
		t.Fatalf("完整视觉复审矩阵没有通过：%+v", result)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{records[0].EvidenceID, records[0].WXGFSHA256, records[0].DecodedSHA256} {
		if bytes.Contains(payload, []byte(private)) {
			t.Fatalf("脱敏矩阵泄露私有值 %q", private)
		}
	}
}

func TestEvaluateVisualReviewMatrixRejectsDuplicateGamingAndConflicts(t *testing.T) {
	first := visualReviewRecord("4.1.12.55", "medium", "same")
	duplicate := first
	result, err := EvaluateVisualReviewMatrix([]VisualReviewRecord{first, duplicate, duplicate, duplicate}, visualReviewTarget(first))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctWXGFSamples != 1 || result.DistinctDecodedVisualFingerprints != 1 || len(result.Blockers) != 4 {
		t.Fatalf("重复记录错误扩充了矩阵：%+v", result)
	}
	conflict := first
	conflict.DecodedSHA256 = lowercaseSHA256([]byte("conflict"))
	if _, err := EvaluateVisualReviewMatrix([]VisualReviewRecord{first, conflict}, visualReviewTarget(first)); err == nil {
		t.Fatal("同一 WXGF 的冲突记录没有被拒绝")
	}
	incomplete := first
	incomplete.CropConfirmed = false
	if _, err := EvaluateVisualReviewMatrix([]VisualReviewRecord{incomplete}, visualReviewTarget(first)); err == nil {
		t.Fatal("缺少裁剪确认的人工记录被接受")
	}
}

func TestEvaluateVisualReviewMatrixSeparatesDecoderBuildsAndVisualDuplicates(t *testing.T) {
	records := []VisualReviewRecord{
		visualReviewRecord("4.1.12.55", "medium", "one"),
		visualReviewRecord("4.1.12.55", "high", "two"),
		visualReviewRecord("4.2.0.1", "medium", "three"),
		visualReviewRecord("4.2.0.1", "high", "four"),
	}
	targetVersion := records[0].ReportedDecoderVersion
	records[3].ReportedDecoderVersion = "sha256:" + lowercaseSHA256([]byte("another-ffmpeg-build"))
	target := visualReviewTarget(records[0])
	result, err := EvaluateVisualReviewMatrix(records, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctWXGFSamples != 3 {
		t.Fatalf("不同解码器构建错误拼入当前矩阵：%+v", result)
	}

	records[3].ReportedDecoderVersion = targetVersion
	records[3].ProviderSHA256 = lowercaseSHA256([]byte("another-provider-build"))
	result, err = EvaluateVisualReviewMatrix(records, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctWXGFSamples != 3 || result.OtherBinaryIdentityRecordsExcluded != 1 {
		t.Fatalf("不同 provider 构建错误拼入当前矩阵：%+v", result)
	}

	records[3].ProviderSHA256 = target.ProviderSHA256
	records[3].ProviderIdentityManifestSHA256 = lowercaseSHA256([]byte("another-identity-manifest"))
	result, err = EvaluateVisualReviewMatrix(records, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctWXGFSamples != 3 || result.OtherBinaryIdentityRecordsExcluded != 1 {
		t.Fatalf("不同身份清单错误拼入当前矩阵：%+v", result)
	}

	records[3].ProviderIdentityManifestSHA256 = target.ProviderIdentityManifestSHA256
	records[3].DecodedVisualFingerprint = records[0].DecodedVisualFingerprint
	result, err = EvaluateVisualReviewMatrix(records, target)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctDecodedVisualFingerprints != 3 ||
		!slicesContain(result.Blockers, "distinct_decoded_visual_fingerprints_below_four") {
		t.Fatalf("相同画面指纹错误扩充了视觉多样性：%+v", result)
	}
}

func TestEvaluateVisualReviewMatrixDoesNotUseThumbnailsForDiversityGate(t *testing.T) {
	medium := visualReviewRecord("4.1.12.55", "medium", "medium")
	high := visualReviewRecord("4.1.12.55", "high", "high")
	repeatedMedium := medium
	repeatedMedium.WeChatVersion = "4.2.0.1"
	repeatedHigh := high
	repeatedHigh.WeChatVersion = "4.2.0.1"
	records := []VisualReviewRecord{medium, high, repeatedMedium, repeatedHigh}
	for _, suffix := range []string{"thumb-one", "thumb-two", "thumb-three", "thumb-four"} {
		records = append(records, visualReviewRecord("4.2.0.1", "thumbnail", suffix))
	}

	result, err := EvaluateVisualReviewMatrix(records, visualReviewTarget(medium))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "insufficient" || result.DistinctWXGFSamples != 2 ||
		result.DistinctDecodedVisualFingerprints != 2 || result.ObservedTierCounts["thumbnail"] != 4 ||
		!slicesContain(result.Blockers, "distinct_wxgf_samples_below_four") ||
		!slicesContain(result.Blockers, "distinct_decoded_visual_fingerprints_below_four") {
		t.Fatalf("thumbnail 错误参与了 high+medium 多样性门禁：%+v", result)
	}
}

func slicesContain(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
