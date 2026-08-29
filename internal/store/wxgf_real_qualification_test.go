package store

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
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

func observeRealWXGFPixels(t *testing.T, payload []byte) realWXGFPixelObservation {
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
	return observation
}

const (
	realWXGFMessagesPerSession = 5000
	realWXGFMaximumMessages    = 100000
)

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
	providerPath := strings.TrimSpace(os.Getenv("V_LOCAL_TEST_WXGF_PROVIDER"))
	messageCount := 0
	imageCount := 0
	wxgfCount := 0
	inspectionFailures := map[string]int{}

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
			stem, stemErr := imageResourceStem(value.SnapshotPath, message)
			if stemErr != nil {
				continue
			}
			candidates, candidateErr := chatImageCandidates(value.SnapshotPath, value.AccountPath, stem, message.MediaMD5)
			if candidateErr != nil {
				continue
			}
			for _, candidate := range candidates {
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
				inspection, inspectErr := wxgfqual.Inspect(plain)
				if inspectErr != nil {
					inspectionFailures[realWXGFInspectionReason(inspectErr)]++
					continue
				}
				if providerPath == "" {
					t.Logf("真实 WXGF 结构候选通过：bytes=%d hevc_offset=%d nal_units=%d pictures=%d；未设置 provider，未执行解码",
						len(plain), inspection.HEVCOffset, inspection.NALUnitCount, inspection.PictureCount)
					return
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
				pixels := observeRealWXGFPixels(t, result.OutputPNG)
				t.Logf("真实 WXGF 解码试验通过：bytes=%d dimensions=%dx%d decoder=%s pixel_observation=%+v blockers=%v",
					len(plain), result.Validation.Width, result.Validation.Height, result.Decoder, pixels, result.PromotionBlockers)
				return
			}
		}
	}
	t.Fatalf("没有找到通过保守结构检查的真实 WXGF：messages=%d images=%d wxgf=%d rejected=%v",
		messageCount, imageCount, wxgfCount, inspectionFailures)
}
