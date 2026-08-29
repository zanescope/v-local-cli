package wxgfqual

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const (
	VisualReviewCaptureProtocol = "v-local-cli/wxgf-visual-review-capture/v1"
	VisualReviewRecordProtocol  = "v-local-cli/wxgf-visual-review-record/v1"
	VisualReviewMatrixProtocol  = "v-local-cli/wxgf-visual-review-matrix/v1"
	VisualReviewHelperProtocol  = "v-local-cli/wxgf-visual-review-helper/v1"
	VisualReviewDecoder         = "ffmpeg"

	VisualReviewQualityTierBasis            = "hardlink_cache_filename_variant_not_source_quality"
	VisualReviewSourceOriginalQualityStatus = "unknown"
	VisualReviewSourceProducerVersionStatus = "unknown"
	VisualReviewVersionCoverageBasis        = "installed_package_at_review_not_source_provenance"
	VisualReviewDecoderIdentityBasis        = "provider_reported_adjacent_decoder_sha256_unattested_provider"
	VisualReviewProviderBinaryTrustStatus   = "unverified"
	VisualReviewQualificationScope          = "human_visual_equivalence_only"

	MinimumVisualReviewSamples  = 4
	MinimumVisualReviewVersions = 2
)

var requiredVisualReviewTiers = []string{"high", "medium"}

type VisualReviewCaptureSample struct {
	Ordinal                     int    `json:"ordinal"`
	EvidenceID                  string `json:"evidence_id"`
	QualityTier                 string `json:"quality_tier"`
	QualityTierBasis            string `json:"quality_tier_basis"`
	WXGFBytes                   int    `json:"wxgf_bytes"`
	WXGFSHA256                  string `json:"wxgf_sha256"`
	DecodedSHA256               string `json:"decoded_sha256"`
	DecodedVisualFingerprint    string `json:"decoded_visual_fingerprint"`
	DecodedWidth                int    `json:"decoded_width"`
	DecodedHeight               int    `json:"decoded_height"`
	DecodedFileName             string `json:"decoded_file_name"`
	SourceOriginalQualityStatus string `json:"source_original_quality_status"`
}

type VisualReviewCapture struct {
	Protocol                  string                      `json:"protocol"`
	GenerationID              string                      `json:"generation_id"`
	SnapshotManifestSHA256    string                      `json:"snapshot_manifest_sha256"`
	ReportedDecoder           string                      `json:"reported_decoder"`
	ReportedDecoderVersion    string                      `json:"reported_decoder_version"`
	DecoderIdentityBasis      string                      `json:"decoder_identity_basis"`
	ProviderProtocol          string                      `json:"provider_protocol"`
	ProviderBinaryTrustStatus string                      `json:"provider_binary_trust_status"`
	Samples                   []VisualReviewCaptureSample `json:"samples"`
	PrivateOnly               bool                        `json:"private_only"`
	ContainsEvidenceIDs       bool                        `json:"contains_evidence_ids"`
	ContainsContentDigests    bool                        `json:"contains_content_digests"`
}

type VisualReviewImage struct {
	SHA256            string `json:"sha256"`
	VisualFingerprint string `json:"visual_fingerprint"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
}

type VisualReviewBundle struct {
	HTML      []byte            `json:"-"`
	Reference VisualReviewImage `json:"reference"`
	Decoded   VisualReviewImage `json:"decoded"`
}

type VisualReviewRecord struct {
	Protocol                     string `json:"protocol"`
	ReviewStatus                 string `json:"review_status"`
	ReviewMethod                 string `json:"review_method"`
	RunNonce                     string `json:"run_nonce"`
	ReviewedAtUTC                string `json:"reviewed_at_utc"`
	WeChatVersion                string `json:"wechat_version"`
	ClientVersionObservation     string `json:"client_version_observation"`
	SourceProducerVersionStatus  string `json:"source_producer_version_status"`
	ReportedDecoder              string `json:"reported_decoder"`
	ReportedDecoderVersion       string `json:"reported_decoder_version"`
	DecoderIdentityBasis         string `json:"decoder_identity_basis"`
	ProviderProtocol             string `json:"provider_protocol"`
	ProviderBinaryTrustStatus    string `json:"provider_binary_trust_status"`
	QualityTier                  string `json:"quality_tier"`
	QualityTierBasis             string `json:"quality_tier_basis"`
	EvidenceID                   string `json:"evidence_id"`
	GenerationID                 string `json:"generation_id"`
	SnapshotManifestSHA256       string `json:"snapshot_manifest_sha256"`
	WXGFSHA256                   string `json:"wxgf_sha256"`
	DecodedSHA256                string `json:"decoded_sha256"`
	DecodedVisualFingerprint     string `json:"decoded_visual_fingerprint"`
	ReferenceSHA256              string `json:"reference_sha256"`
	DecodedWidth                 int    `json:"decoded_width"`
	DecodedHeight                int    `json:"decoded_height"`
	ReferenceWidth               int    `json:"reference_width"`
	ReferenceHeight              int    `json:"reference_height"`
	SameContentConfirmed         bool   `json:"same_content_confirmed"`
	OrientationConfirmed         bool   `json:"orientation_confirmed"`
	CropConfirmed                bool   `json:"crop_confirmed"`
	ColorAndArtifactsConfirmed   bool   `json:"color_and_artifacts_confirmed"`
	SourceOriginalQualityStatus  string `json:"source_original_quality_status"`
	TemporaryDecodedRemoved      bool   `json:"temporary_decoded_removed"`
	TemporaryReferenceRemoved    bool   `json:"temporary_reference_removed"`
	TemporaryReviewBundleRemoved bool   `json:"temporary_review_bundle_removed"`
}

type VisualReviewMatrix struct {
	Protocol                                        string         `json:"protocol"`
	Status                                          string         `json:"status"`
	QualificationScope                              string         `json:"qualification_scope"`
	ReportedDecoder                                 string         `json:"reported_decoder"`
	ReportedDecoderVersion                          string         `json:"reported_decoder_version"`
	DecoderIdentityBasis                            string         `json:"decoder_identity_basis"`
	ProviderBinaryTrustStatus                       string         `json:"provider_binary_trust_status"`
	VersionCoverageBasis                            string         `json:"version_coverage_basis"`
	TierCoverageBasis                               string         `json:"tier_coverage_basis"`
	SourceOriginalQualityStatus                     string         `json:"source_original_quality_status"`
	SourceProducerVersionStatus                     string         `json:"source_producer_version_status"`
	DistinctWXGFSamples                             int            `json:"distinct_wxgf_samples"`
	DistinctDecodedVisualFingerprints               int            `json:"distinct_decoded_visual_fingerprints"`
	DistinctInstalledReviewVersions                 int            `json:"distinct_installed_review_versions"`
	InstalledReviewVersionsWithRequiredTierCoverage int            `json:"installed_review_versions_with_required_tier_coverage"`
	RequiredTiers                                   []string       `json:"required_tiers"`
	ObservedTierCounts                              map[string]int `json:"observed_tier_counts"`
	Blockers                                        []string       `json:"blockers"`
	ContainsEvidenceIDs                             bool           `json:"contains_evidence_ids"`
	ContainsImageContentDigests                     bool           `json:"contains_image_content_digests"`
	FixedDimensionQualityGate                       bool           `json:"fixed_dimension_quality_gate"`
	ProductionReady                                 bool           `json:"production_ready"`
}

func lowercaseSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// visualReviewPerceptualFingerprint is only a conservative diversity key. It
// is private evidence and never proves pixel identity, fidelity, or source
// quality.
func visualReviewPerceptualFingerprint(decoded image.Image) string {
	bounds := decoded.Bounds()
	luminance := func(x, y int) uint64 {
		red, green, blue, _ := decoded.At(x, y).RGBA()
		return 299*uint64(red) + 587*uint64(green) + 114*uint64(blue)
	}
	coordinate := func(minimum, size, index, divisions int) int {
		if size <= 1 {
			return minimum
		}
		return minimum + index*(size-1)/divisions
	}
	var value uint64
	var bit uint
	for row := 0; row < 8; row++ {
		y := coordinate(bounds.Min.Y, bounds.Dy(), row, 7)
		for column := 0; column < 8; column++ {
			left := coordinate(bounds.Min.X, bounds.Dx(), column, 8)
			right := coordinate(bounds.Min.X, bounds.Dx(), column+1, 8)
			if luminance(left, y) > luminance(right, y) {
				value |= uint64(1) << bit
			}
			bit++
		}
	}
	return fmt.Sprintf("%016x", value)
}

func reviewPNG(payload []byte) (VisualReviewImage, error) {
	if len(payload) == 0 || len(payload) > maxDecodedOutputBytes {
		return VisualReviewImage{}, errors.New("视觉复审 PNG 大小无效")
	}
	validation, err := cryptoutil.ValidateImageStructure(payload)
	if err != nil || validation.Format != "png" {
		return VisualReviewImage{}, errors.New("视觉复审只接受完整解码的 PNG")
	}
	decoded, err := png.Decode(bytes.NewReader(payload))
	if err != nil || decoded.Bounds().Dx() != validation.Width || decoded.Bounds().Dy() != validation.Height {
		return VisualReviewImage{}, errors.New("视觉复审 PNG 像素解码无效")
	}
	return VisualReviewImage{
		SHA256: lowercaseSHA256(payload), VisualFingerprint: visualReviewPerceptualFingerprint(decoded),
		Width: validation.Width, Height: validation.Height,
	}, nil
}

// InspectVisualReviewPNG returns private qualification metadata for one strict
// PNG. Callers must not publish the digest or perceptual fingerprint.
func InspectVisualReviewPNG(payload []byte) (VisualReviewImage, error) {
	return reviewPNG(payload)
}

func PrepareVisualReviewBundle(referencePNG, decodedPNG []byte) (VisualReviewBundle, error) {
	reference, err := reviewPNG(referencePNG)
	if err != nil {
		return VisualReviewBundle{}, err
	}
	decoded, err := reviewPNG(decodedPNG)
	if err != nil {
		return VisualReviewBundle{}, err
	}
	referenceData := base64.StdEncoding.EncodeToString(referencePNG)
	decodedData := base64.StdEncoding.EncodeToString(decodedPNG)
	html := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'">
<meta name="referrer" content="no-referrer"><title>WXGF 人工视觉等价复审</title>
<style>body{font-family:system-ui,sans-serif;margin:16px;background:#111;color:#eee}.notice{max-width:1100px;line-height:1.55}.grid{display:grid;grid-template-columns:1fr 1fr;gap:12px;align-items:start}.panel{border:1px solid #555;padding:10px;overflow:auto;background:#222;min-width:0}.panel img{display:block;max-width:none;height:auto;margin:auto}.meta{font-family:ui-monospace,monospace;color:#bbb}@media(max-width:900px){.grid{grid-template-columns:1fr}}</style>
</head><body><h1>WXGF 人工视觉等价复审</h1>
<div class="notice"><p>左侧必须是从同一条微信消息原图界面人工截取的图片内容，右侧是 WXGF 解码结果。只有在内容、方向、裁剪以及颜色/解码伪影四项都确认一致时才能通过。两图按自身像素显示并可滚动；尺寸不同本身不代表质量更高或更低，浏览器显示也不构成色度学证明。</p></div>
<div class="grid"><section class="panel"><h2>微信界面参考截图</h2><div class="meta">%dx%d</div><img alt="微信界面参考截图" src="data:image/png;base64,%s"></section>
<section class="panel"><h2>WXGF 解码结果</h2><div class="meta">%dx%d</div><img alt="WXGF 解码结果" src="data:image/png;base64,%s"></section></div>
</body></html>`, reference.Width, reference.Height, referenceData, decoded.Width, decoded.Height, decodedData)
	return VisualReviewBundle{HTML: []byte(html), Reference: reference, Decoded: decoded}, nil
}

func validReviewToken(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._:+@-", character)) {
			return false
		}
	}
	return true
}

func validPrivateReviewIdentifier(value, prefix string, maximum int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximum || !strings.HasPrefix(value, prefix) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validReviewSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validVisualReviewFingerprint(value string) bool {
	return validLowerHex(value, 8)
}

func validVisualReviewDecoderIdentity(decoder, version string) bool {
	return decoder == VisualReviewDecoder && strings.HasPrefix(version, "sha256:") && validReviewSHA256(strings.TrimPrefix(version, "sha256:"))
}

func validLowerHex(value string, bytes int) bool {
	if len(value) != bytes*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validReviewTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z")
}

func validReviewDimensions(width, height int) bool {
	return width > 0 && height > 0 && int64(width) <= cryptoutil.MaxDecodedImagePixels/int64(height)
}

func ValidateVisualReviewCapture(value VisualReviewCapture) error {
	if value.Protocol != VisualReviewCaptureProtocol || !value.PrivateOnly ||
		!value.ContainsEvidenceIDs || !value.ContainsContentDigests ||
		!validReviewToken(value.GenerationID, 128) || !validReviewSHA256(value.SnapshotManifestSHA256) ||
		!validVisualReviewDecoderIdentity(value.ReportedDecoder, value.ReportedDecoderVersion) ||
		value.DecoderIdentityBasis != VisualReviewDecoderIdentityBasis || value.ProviderProtocol != ProviderProtocol ||
		value.ProviderBinaryTrustStatus != VisualReviewProviderBinaryTrustStatus ||
		len(value.Samples) == 0 || len(value.Samples) > 5 {
		return errors.New("WXGF 私有视觉复审清单无效")
	}
	seenEvidence, seenWXGF := map[string]bool{}, map[string]bool{}
	for index, sample := range value.Samples {
		ordinal := index + 1
		if sample.Ordinal != ordinal || sample.DecodedFileName != fmt.Sprintf("decoded-%02d.png", ordinal) ||
			!validPrivateReviewIdentifier(sample.EvidenceID, "wechat:", 4096) ||
			(sample.QualityTier != "high" && sample.QualityTier != "medium" && sample.QualityTier != "thumbnail") ||
			sample.QualityTierBasis != VisualReviewQualityTierBasis ||
			sample.WXGFBytes <= 0 || sample.WXGFBytes > MaxWXGFBytes ||
			!validReviewSHA256(sample.WXGFSHA256) || !validReviewSHA256(sample.DecodedSHA256) ||
			!validVisualReviewFingerprint(sample.DecodedVisualFingerprint) ||
			!validReviewDimensions(sample.DecodedWidth, sample.DecodedHeight) ||
			sample.SourceOriginalQualityStatus != VisualReviewSourceOriginalQualityStatus ||
			seenEvidence[sample.EvidenceID] || seenWXGF[sample.WXGFSHA256] {
			return errors.New("WXGF 私有视觉复审样本无效")
		}
		seenEvidence[sample.EvidenceID] = true
		seenWXGF[sample.WXGFSHA256] = true
	}
	return nil
}

func validateVisualReviewRecord(value VisualReviewRecord) error {
	if value.Protocol != VisualReviewRecordProtocol || value.ReviewStatus != "confirmed" ||
		value.ReviewMethod != "human_side_by_side_wechat_ui_reference" ||
		value.ClientVersionObservation != "installed_package_at_review" ||
		value.SourceProducerVersionStatus != VisualReviewSourceProducerVersionStatus ||
		!validVisualReviewDecoderIdentity(value.ReportedDecoder, value.ReportedDecoderVersion) ||
		value.DecoderIdentityBasis != VisualReviewDecoderIdentityBasis || value.ProviderProtocol != ProviderProtocol ||
		value.ProviderBinaryTrustStatus != VisualReviewProviderBinaryTrustStatus ||
		!validLowerHex(value.RunNonce, 16) || !validReviewTime(value.ReviewedAtUTC) ||
		!validReviewToken(value.WeChatVersion, 64) || !validPrivateReviewIdentifier(value.EvidenceID, "wechat:", 4096) ||
		!validReviewToken(value.GenerationID, 128) {
		return errors.New("WXGF 人工视觉复审记录身份无效")
	}
	if value.QualityTier != "high" && value.QualityTier != "medium" && value.QualityTier != "thumbnail" {
		return errors.New("WXGF 人工视觉复审缓存档位无效")
	}
	if value.QualityTierBasis != VisualReviewQualityTierBasis ||
		value.SourceOriginalQualityStatus != VisualReviewSourceOriginalQualityStatus ||
		!validVisualReviewFingerprint(value.DecodedVisualFingerprint) {
		return errors.New("WXGF 人工视觉复审证据范围无效")
	}
	for _, digest := range []string{value.SnapshotManifestSHA256, value.WXGFSHA256, value.DecodedSHA256, value.ReferenceSHA256} {
		if !validReviewSHA256(digest) {
			return errors.New("WXGF 人工视觉复审摘要无效")
		}
	}
	if !validReviewDimensions(value.DecodedWidth, value.DecodedHeight) ||
		!validReviewDimensions(value.ReferenceWidth, value.ReferenceHeight) {
		return errors.New("WXGF 人工视觉复审尺寸无效")
	}
	if !value.SameContentConfirmed || !value.OrientationConfirmed || !value.CropConfirmed ||
		!value.ColorAndArtifactsConfirmed ||
		!value.TemporaryDecodedRemoved || !value.TemporaryReferenceRemoved || !value.TemporaryReviewBundleRemoved {
		return errors.New("WXGF 人工视觉复审确认或清理不完整")
	}
	return nil
}

func EvaluateVisualReviewMatrix(records []VisualReviewRecord, reportedDecoder, reportedDecoderVersion string) (VisualReviewMatrix, error) {
	if !validVisualReviewDecoderIdentity(reportedDecoder, reportedDecoderVersion) {
		return VisualReviewMatrix{}, errors.New("WXGF 人工视觉复审目标解码器身份无效")
	}
	result := VisualReviewMatrix{
		Protocol: VisualReviewMatrixProtocol, Status: "insufficient",
		QualificationScope: VisualReviewQualificationScope,
		ReportedDecoder:    reportedDecoder, ReportedDecoderVersion: reportedDecoderVersion,
		DecoderIdentityBasis:        VisualReviewDecoderIdentityBasis,
		ProviderBinaryTrustStatus:   VisualReviewProviderBinaryTrustStatus,
		VersionCoverageBasis:        VisualReviewVersionCoverageBasis,
		TierCoverageBasis:           VisualReviewQualityTierBasis,
		SourceOriginalQualityStatus: VisualReviewSourceOriginalQualityStatus,
		SourceProducerVersionStatus: VisualReviewSourceProducerVersionStatus,
		RequiredTiers:               append([]string(nil), requiredVisualReviewTiers...),
		ObservedTierCounts:          map[string]int{}, Blockers: []string{},
		ContainsEvidenceIDs: false, ContainsImageContentDigests: false,
		FixedDimensionQualityGate: false, ProductionReady: false,
	}
	type observedRecord struct {
		decoded, reference, fingerprint string
	}
	type sourceOutput struct {
		decoded, fingerprint string
	}
	seenSources := map[string]bool{}
	seenVisualFingerprints := map[string]bool{}
	seenReviews := map[string]observedRecord{}
	sourceOutputs := map[string]sourceOutput{}
	versionTiers := map[string]map[string]bool{}
	for _, record := range records {
		if err := validateVisualReviewRecord(record); err != nil {
			return VisualReviewMatrix{}, err
		}
		if record.ReportedDecoder != reportedDecoder || record.ReportedDecoderVersion != reportedDecoderVersion {
			continue
		}
		if previous, exists := sourceOutputs[record.WXGFSHA256]; exists {
			if previous.decoded != record.DecodedSHA256 || previous.fingerprint != record.DecodedVisualFingerprint {
				return VisualReviewMatrix{}, errors.New("同一 WXGF 摘要存在冲突的解码输出")
			}
		} else {
			sourceOutputs[record.WXGFSHA256] = sourceOutput{decoded: record.DecodedSHA256, fingerprint: record.DecodedVisualFingerprint}
		}
		reviewKey := record.WXGFSHA256 + "\x00" + record.WeChatVersion + "\x00" + record.QualityTier
		if previous, exists := seenReviews[reviewKey]; exists {
			if previous.decoded != record.DecodedSHA256 || previous.reference != record.ReferenceSHA256 ||
				previous.fingerprint != record.DecodedVisualFingerprint {
				return VisualReviewMatrix{}, errors.New("同一 WXGF 摘要存在冲突的人工复审记录")
			}
			continue
		}
		seenReviews[reviewKey] = observedRecord{
			decoded: record.DecodedSHA256, reference: record.ReferenceSHA256,
			fingerprint: record.DecodedVisualFingerprint,
		}
		result.ObservedTierCounts[record.QualityTier]++
		// Thumbnail reviews remain observable for diagnostics, but they cannot
		// satisfy the four-sample diversity gate for the required high+medium
		// qualification matrix.
		if record.QualityTier != "high" && record.QualityTier != "medium" {
			continue
		}
		seenSources[record.WXGFSHA256] = true
		seenVisualFingerprints[record.DecodedVisualFingerprint] = true
		if versionTiers[record.WeChatVersion] == nil {
			versionTiers[record.WeChatVersion] = map[string]bool{}
		}
		versionTiers[record.WeChatVersion][record.QualityTier] = true
	}
	result.DistinctWXGFSamples = len(seenSources)
	result.DistinctDecodedVisualFingerprints = len(seenVisualFingerprints)
	result.DistinctInstalledReviewVersions = len(versionTiers)
	for _, tiers := range versionTiers {
		covered := true
		for _, required := range requiredVisualReviewTiers {
			covered = covered && tiers[required]
		}
		if covered {
			result.InstalledReviewVersionsWithRequiredTierCoverage++
		}
	}
	if result.DistinctWXGFSamples < MinimumVisualReviewSamples {
		result.Blockers = append(result.Blockers, "distinct_wxgf_samples_below_four")
	}
	if result.DistinctDecodedVisualFingerprints < MinimumVisualReviewSamples {
		result.Blockers = append(result.Blockers, "distinct_decoded_visual_fingerprints_below_four")
	}
	if result.DistinctInstalledReviewVersions < MinimumVisualReviewVersions {
		result.Blockers = append(result.Blockers, "distinct_installed_review_versions_below_two")
	}
	if result.InstalledReviewVersionsWithRequiredTierCoverage < MinimumVisualReviewVersions {
		result.Blockers = append(result.Blockers, "two_installed_review_versions_with_high_and_medium_reviews_not_observed")
	}
	if len(result.Blockers) == 0 {
		result.Status = "pass"
	}
	return result, nil
}
