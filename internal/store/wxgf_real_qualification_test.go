package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/wxgfqual"
)

type realWXGFPixelObservation struct {
	Samples      int
	CoarseColors int
	RedSpan      uint32
	GreenSpan    uint32
	BlueSpan     uint32
}

// realWXGFPerceptualHash is only a low-cost diversity fingerprint. Equal or
// distant values do not prove pixel identity, source quality, or visual
// equivalence with the image shown by WeChat.
func realWXGFPerceptualHash(decoded image.Image) uint64 {
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
	var result uint64
	var bit uint
	for row := 0; row < 8; row++ {
		y := coordinate(bounds.Min.Y, bounds.Dy(), row, 7)
		for column := 0; column < 8; column++ {
			left := coordinate(bounds.Min.X, bounds.Dx(), column, 8)
			right := coordinate(bounds.Min.X, bounds.Dx(), column+1, 8)
			if luminance(left, y) > luminance(right, y) {
				result |= uint64(1) << bit
			}
			bit++
		}
	}
	return result
}

func observeRealWXGFPixels(t *testing.T, payload []byte) (realWXGFPixelObservation, uint64) {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatal("经过验证的真实 WXGF PNG 无法再次解码")
	}
	bounds := decoded.Bounds()
	stepX, stepY := bounds.Dx()/64, bounds.Dy()/64
	if stepX < 1 {
		stepX = 1
	}
	if stepY < 1 {
		stepY = 1
	}
	minimum := [3]uint32{0xffff, 0xffff, 0xffff}
	maximum := [3]uint32{}
	colors := map[uint16]bool{}
	observation := realWXGFPixelObservation{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			red, green, blue, _ := decoded.At(x, y).RGBA()
			channels := [3]uint32{red, green, blue}
			for index, channel := range channels {
				if channel < minimum[index] {
					minimum[index] = channel
				}
				if channel > maximum[index] {
					maximum[index] = channel
				}
			}
			colors[uint16(red>>12)<<8|uint16(green>>12)<<4|uint16(blue>>12)] = true
			observation.Samples++
		}
	}
	observation.CoarseColors = len(colors)
	observation.RedSpan = maximum[0] - minimum[0]
	observation.GreenSpan = maximum[1] - minimum[1]
	observation.BlueSpan = maximum[2] - minimum[2]
	return observation, realWXGFPerceptualHash(decoded)
}

const (
	realWXGFMessagesPerSession = 5000
	realWXGFMaximumMessages    = 100000
	realWXGFMaximumSamples     = 5
)

func realWXGFSampleTarget(t *testing.T) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("V_LOCAL_TEST_WXGF_SAMPLE_TARGET"))
	if raw == "" {
		return 1
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > realWXGFMaximumSamples {
		t.Fatalf("V_LOCAL_TEST_WXGF_SAMPLE_TARGET 必须为 1..%d", realWXGFMaximumSamples)
	}
	return value
}

func realWXGFFileIndex(t *testing.T, accountPath string) (map[string][]string, int) {
	t.Helper()
	result := map[string][]string{}
	seen := map[string]bool{}
	filesScanned := 0
	for _, root := range hardlinkRoots(accountPath, "image") {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			identity := strings.ToLower(filepath.Clean(path))
			if seen[identity] {
				return nil
			}
			seen[identity] = true
			filesScanned++
			if filesScanned > maxMomentMediaFiles {
				return fs.SkipAll
			}
			if !pathUnderRoot(path, root) {
				return nil
			}
			name := strings.ToLower(entry.Name())
			result[name] = append(result[name], path)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal("真实资格验证附件索引失败")
		}
		if filesScanned > maxMomentMediaFiles {
			t.Fatal("真实资格验证附件索引达到文件上限")
		}
	}
	return result, filesScanned
}

func realWXGFMinimumVisualDistance(value uint64, previous []uint64) int {
	minimum := 64
	for _, candidate := range previous {
		if distance := bits.OnesCount64(value ^ candidate); distance < minimum {
			minimum = distance
		}
	}
	return minimum
}

func realWXGFReviewRoot(t *testing.T, accountPath, snapshotPath string) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("V_LOCAL_TEST_WXGF_REVIEW_ROOT"))
	if raw == "" {
		return ""
	}
	if !filepath.IsAbs(raw) || (runtime.GOOS == "windows" && (strings.HasPrefix(raw, `\\`) || strings.HasPrefix(raw, "//"))) {
		t.Fatal("真实 WXGF 视觉复审目录必须是本机绝对路径")
	}
	root, err := filepath.Abs(raw)
	if err != nil {
		t.Fatal("真实 WXGF 视觉复审目录无效")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || state.ValidatePrivateDirectorySecurity(root) != nil {
		t.Fatal("真实 WXGF 视觉复审目录不是安全的私有目录")
	}
	if pathUnderRoot(root, accountPath) || pathUnderRoot(root, snapshotPath) ||
		pathUnderRoot(accountPath, root) || pathUnderRoot(snapshotPath, root) {
		t.Fatal("真实 WXGF 视觉复审目录不能与微信数据或快照目录重叠")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("真实 WXGF 视觉复审目录必须为空")
	}
	return root
}

func writeRealWXGFReviewFile(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	succeeded := false
	defer func() {
		_ = file.Close()
		if !succeeded {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	succeeded = true
	return nil
}

func realWXGFInspectionReason(err error) string {
	for _, candidate := range []struct {
		reason string
		target error
	}{
		{"too_large", wxgfqual.ErrWXGFTooLarge},
		{"no_hevc_candidate", wxgfqual.ErrNoHEVCCandidate},
		{"invalid_hevc_candidate", wxgfqual.ErrInvalidHEVCCandidate},
		{"missing_parameter_sets", wxgfqual.ErrMissingParameterSets},
		{"multiple_pictures", wxgfqual.ErrMultiplePictures},
		{"not_independent_still", wxgfqual.ErrNotIndependentStill},
	} {
		if errors.Is(err, candidate.target) {
			return candidate.reason
		}
	}
	return "other"
}

// TestRealWXGFQualificationFromSnapshot is opt-in and never runs in ordinary
// CI. It keeps the decrypted WXGF in memory, prints no chat/evidence/path/hash,
// and uses the same message-resource + hardlink mapping as public image export.
func TestRealWXGFQualificationFromSnapshot(t *testing.T) {
	accountSelector := strings.TrimSpace(os.Getenv("V_LOCAL_TEST_WXGF_ACCOUNT"))
	if accountSelector == "" {
		t.Skip("设置 V_LOCAL_TEST_WXGF_ACCOUNT 后运行本机 WXGF 结构资格验证")
	}
	value, err := state.Select(accountSelector)
	if err != nil {
		t.Fatal("无法选择真实资格验证账号")
	}
	secrets, err := state.LoadSecrets(value.AccountID)
	if err != nil || secrets.ImageKeys == nil {
		t.Fatal("真实资格验证账号缺少已验证的图片密钥")
	}
	report, err := Sessions(value.SnapshotPath, false, "", 0)
	if err != nil {
		t.Fatal("无法读取真实资格验证会话")
	}
	snapshotFiles, err := sqliteFiles(value.SnapshotPath)
	if err != nil {
		t.Fatal("无法建立真实资格验证数据库索引")
	}
	indexedFiles, indexedFileCount := realWXGFFileIndex(t, value.AccountPath)
	target := realWXGFSampleTarget(t)
	providerPath := strings.TrimSpace(os.Getenv("V_LOCAL_TEST_WXGF_PROVIDER"))
	reviewRoot := realWXGFReviewRoot(t, value.AccountPath, value.SnapshotPath)
	if reviewRoot != "" && providerPath == "" {
		t.Fatal("生成视觉复审材料必须显式设置 WXGF provider")
	}
	if reviewRoot != "" && (strings.TrimSpace(value.GenerationID) == "" || len(value.SnapshotManifestSHA256) != sha256.Size*2) {
		t.Fatal("生成视觉复审材料需要完整快照代际绑定")
	}
	reviewCapture := wxgfqual.VisualReviewCapture{
		Protocol: wxgfqual.VisualReviewCaptureProtocol, GenerationID: value.GenerationID,
		SnapshotManifestSHA256:           value.SnapshotManifestSHA256,
		DecoderIdentityBasis:             wxgfqual.VisualReviewDecoderIdentityBasis,
		ProviderProtocol:                 wxgfqual.ProviderProtocol,
		ProviderIdentityManifestProtocol: wxgfqual.ProviderIdentityManifestProtocol,
		ProviderSourceStatus:             wxgfqual.ProviderSourceStatus,
		DecoderSourceStatus:              wxgfqual.DecoderSourceStatus,
		ProviderSignatureStatus:          wxgfqual.ProviderSignatureStatus,
		DecoderSignatureStatus:           wxgfqual.DecoderSignatureStatus,
		DecoderDistributionLicenseStatus: wxgfqual.DecoderDistributionLicenseStatus,
		ProviderBinaryTrustStatus:        wxgfqual.VisualReviewProviderBinaryTrustStatus,
		PrivateOnly:                      true, ContainsEvidenceIDs: true, ContainsContentDigests: true,
	}
	createdReviewFiles := []string{}
	reviewCaptureCommitted := false
	t.Cleanup(func() {
		if reviewRoot == "" || reviewCaptureCommitted {
			return
		}
		for _, path := range createdReviewFiles {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("真实 WXGF 视觉复审失败材料清理失败")
			}
		}
	})
	messageCount := 0
	imageCount := 0
	wxgfCount := 0
	qualifiedCount := 0
	zeroDistanceFingerprints := 0
	minimumVisualDistance := 64
	inspectionFailures := map[string]int{}
	seenPaths := map[string]bool{}
	seenPayloads := map[[sha256.Size]byte]bool{}
	seenQualifiedEvidence := map[string]bool{}
	visualHashes := []uint64{}

	for _, session := range report.Items {
		messages, historyErr := History(value.SnapshotPath, session.Username, realWXGFMessagesPerSession)
		if historyErr != nil {
			continue
		}
		for _, message := range messages {
			messageCount++
			if messageCount > realWXGFMaximumMessages {
				t.Fatal("真实资格验证扫描达到消息上限")
			}
			if message.Kind != "image" {
				continue
			}
			imageCount++
			stem, stemErr := imageResourceStemFromFiles(snapshotFiles, message)
			if stemErr != nil {
				continue
			}
			candidates, candidateErr := chatImageCandidatesFromFiles(snapshotFiles, value.AccountPath, stem, message.MediaMD5, indexedFiles)
			if candidateErr != nil {
				continue
			}
			for _, candidate := range candidates {
				if strings.TrimSpace(message.EvidenceID) != "" && seenQualifiedEvidence[message.EvidenceID] {
					break
				}
				pathIdentity := strings.ToLower(filepath.Clean(candidate.path))
				if seenPaths[pathIdentity] {
					continue
				}
				seenPaths[pathIdentity] = true
				info, statErr := os.Lstat(candidate.path)
				if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > wxgfqual.MaxWXGFBytes {
					continue
				}
				raw, readErr := os.ReadFile(candidate.path)
				if readErr != nil {
					continue
				}
				plain, format := raw, cryptoutil.ImageFormat(raw)
				if format == "unknown" {
					plain, format, readErr = cryptoutil.DecryptImageDAT(raw, secrets.ImageKeys.AES, secrets.ImageKeys.XOR)
					if readErr != nil {
						continue
					}
				}
				if format != "wxgf" {
					continue
				}
				wxgfCount++
				payloadDigest := sha256.Sum256(plain)
				if seenPayloads[payloadDigest] {
					continue
				}
				seenPayloads[payloadDigest] = true
				inspection, inspectErr := wxgfqual.Inspect(plain)
				if inspectErr != nil {
					inspectionFailures[realWXGFInspectionReason(inspectErr)]++
					continue
				}
				if providerPath == "" {
					if strings.TrimSpace(message.EvidenceID) != "" {
						seenQualifiedEvidence[message.EvidenceID] = true
					}
					qualifiedCount++
					t.Logf("真实 WXGF 结构样本 %d/%d 通过：quality_tier=%s bytes=%d hevc_offset=%d nal_units=%d pictures=%d；未设置 provider，未执行解码",
						qualifiedCount, target, candidate.qualityTier, len(plain), inspection.HEVCOffset, inspection.NALUnitCount, inspection.PictureCount)
					if qualifiedCount >= target {
						t.Logf("真实 WXGF 结构矩阵达到目标：samples=%d indexed_files=%d", qualifiedCount, indexedFileCount)
						return
					}
					continue
				}
				result, providerErr := wxgfqual.RunProviderTrial(context.Background(), plain, wxgfqual.ProviderOptions{
					Executable: providerPath, TemporaryRoot: t.TempDir(), Timeout: 30 * time.Second,
				})
				if providerErr != nil {
					t.Fatal("真实 WXGF 适配器资格验证失败")
				}
				if result.ProductionReady || result.Validation.Format != "png" {
					t.Fatal("真实 WXGF 适配器结果越过了实验门禁")
				}
				pixels, visualHash := observeRealWXGFPixels(t, result.OutputPNG)
				if len(visualHashes) > 0 {
					distance := realWXGFMinimumVisualDistance(visualHash, visualHashes)
					if distance == 0 {
						zeroDistanceFingerprints++
						continue
					}
					if distance < minimumVisualDistance {
						minimumVisualDistance = distance
					}
				}
				visualHashes = append(visualHashes, visualHash)
				if strings.TrimSpace(message.EvidenceID) != "" {
					seenQualifiedEvidence[message.EvidenceID] = true
				}
				qualifiedCount++
				if reviewRoot != "" {
					if strings.TrimSpace(message.EvidenceID) == "" {
						t.Fatal("真实 WXGF 私有视觉复审样本缺少 evidence_id")
					}
					if reviewCapture.ReportedDecoder == "" {
						reviewCapture.ReportedDecoder = result.Decoder
						reviewCapture.ReportedDecoderVersion = result.DecoderVersion
						reviewCapture.ProviderIdentityManifestSHA256 = result.BinaryIdentity.ManifestSHA256
						reviewCapture.ProviderSHA256 = result.BinaryIdentity.ProviderSHA256
					} else if reviewCapture.ReportedDecoder != result.Decoder || reviewCapture.ReportedDecoderVersion != result.DecoderVersion ||
						reviewCapture.ProviderIdentityManifestSHA256 != result.BinaryIdentity.ManifestSHA256 ||
						reviewCapture.ProviderSHA256 != result.BinaryIdentity.ProviderSHA256 {
						t.Fatal("真实 WXGF 私有视觉复审样本混用了不同 provider/解码器身份")
					}
					decodedReview, err := wxgfqual.InspectVisualReviewPNG(result.OutputPNG)
					if err != nil || decodedReview.SHA256 == "" || decodedReview.VisualFingerprint != fmt.Sprintf("%016x", visualHash) {
						t.Fatal("真实 WXGF 私有视觉复审 PNG 元数据无效")
					}
					decodedFileName := fmt.Sprintf("decoded-%02d.png", qualifiedCount)
					decodedPath := filepath.Join(reviewRoot, decodedFileName)
					if err := writeRealWXGFReviewFile(decodedPath, result.OutputPNG); err != nil {
						t.Fatal("无法写入真实 WXGF 私有视觉复审材料")
					}
					createdReviewFiles = append(createdReviewFiles, decodedPath)
					decodedDigest := sha256.Sum256(result.OutputPNG)
					reviewCapture.Samples = append(reviewCapture.Samples, wxgfqual.VisualReviewCaptureSample{
						Ordinal: qualifiedCount, EvidenceID: message.EvidenceID, QualityTier: candidate.qualityTier,
						QualityTierBasis: wxgfqual.VisualReviewQualityTierBasis,
						WXGFBytes:        len(plain), WXGFSHA256: hex.EncodeToString(payloadDigest[:]),
						DecodedSHA256: hex.EncodeToString(decodedDigest[:]), DecodedWidth: result.Validation.Width,
						DecodedHeight: result.Validation.Height, DecodedFileName: decodedFileName,
						DecodedVisualFingerprint:    decodedReview.VisualFingerprint,
						SourceOriginalQualityStatus: wxgfqual.VisualReviewSourceOriginalQualityStatus,
					})
				}
				t.Logf("真实 WXGF 解码样本 %d/%d 通过：quality_tier=%s bytes=%d dimensions=%dx%d decoder=%s pixel_observation=%+v blockers=%v",
					qualifiedCount, target, candidate.qualityTier, len(plain), result.Validation.Width, result.Validation.Height, result.Decoder, pixels, result.PromotionBlockers)
				if qualifiedCount >= target {
					if len(visualHashes) == 1 {
						minimumVisualDistance = 0
					}
					if reviewRoot != "" {
						if err := wxgfqual.ValidateVisualReviewCapture(reviewCapture); err != nil {
							t.Fatal("真实 WXGF 私有视觉复审清单校验失败")
						}
						payload, err := json.MarshalIndent(reviewCapture, "", "  ")
						if err != nil {
							t.Fatal("无法编码真实 WXGF 私有视觉复审清单")
						}
						capturePath := filepath.Join(reviewRoot, "capture.json")
						if err := writeRealWXGFReviewFile(capturePath, append(payload, '\n')); err != nil {
							t.Fatal("无法写入真实 WXGF 私有视觉复审清单")
						}
						createdReviewFiles = append(createdReviewFiles, capturePath)
						reviewCaptureCommitted = true
						t.Logf("真实 WXGF 私有视觉复审材料已写入：samples=%d path_disclosed=false", qualifiedCount)
					}
					t.Logf("真实 WXGF 解码矩阵达到目标：samples=%d zero_distance_fingerprints=%d minimum_perceptual_hamming_distance=%d indexed_files=%d",
						qualifiedCount, zeroDistanceFingerprints, minimumVisualDistance, indexedFileCount)
					return
				}
			}
		}
	}
	t.Fatalf("真实 WXGF 样本不足：qualified=%d target=%d messages=%d images=%d wxgf=%d zero_distance_fingerprints=%d indexed_files=%d rejected=%v",
		qualifiedCount, target, messageCount, imageCount, wxgfCount, zeroDistanceFingerprints, indexedFileCount, inspectionFailures)
}
