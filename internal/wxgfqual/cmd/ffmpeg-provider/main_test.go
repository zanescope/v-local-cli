package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/wxgfqual"
)

func validTestRequest() wxgfqual.ProviderRequest {
	return wxgfqual.ProviderRequest{
		Protocol: wxgfqual.ProviderProtocol, RequestID: strings.Repeat("a", 32), Action: "decode_still",
		InputPath: "input.hevc", InputFormat: "hevc_annex_b", InputSHA256: strings.Repeat("b", 64),
		OutputPath: "output.png", OutputFormat: "png", MaximumFrames: 1,
		MaximumPixels: cryptoutil.MaxDecodedImagePixels, NetworkAllowed: false,
	}
}

func TestDecodeRequestIsStrict(t *testing.T) {
	request := validTestRequest()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRequest(bytes.NewReader(payload))
	if err != nil || decoded.RequestID != request.RequestID {
		t.Fatalf("合法请求解析失败：%+v %v", decoded, err)
	}
	for _, invalid := range [][]byte{
		append(payload, []byte("{}")...),
		[]byte(`{"protocol":"v-local-cli-image-decoder/1","unknown":true}`),
	} {
		if _, err := decodeRequest(bytes.NewReader(invalid)); err == nil {
			t.Fatal("非严格请求未被拒绝")
		}
	}
	request.NetworkAllowed = true
	payload, _ = json.Marshal(request)
	if _, err := decodeRequest(bytes.NewReader(payload)); err == nil {
		t.Fatal("允许网络的请求未被拒绝")
	}
}

func TestPathInsideStageRequiresExactFiles(t *testing.T) {
	stage := t.TempDir()
	input := filepath.Join(stage, "input.hevc")
	if value, err := pathInsideStage(input, stage, "input.hevc"); err != nil || value != input {
		t.Fatalf("合法 stage 文件被拒绝：%s %v", value, err)
	}
	for _, path := range []string{
		filepath.Join(stage, "other.hevc"),
		filepath.Join(stage, "nested", "input.hevc"),
		filepath.Join(filepath.Dir(stage), "input.hevc"),
		"input.hevc",
	} {
		if _, err := pathInsideStage(path, stage, "input.hevc"); err == nil {
			t.Fatalf("越界或非精确 stage 文件未被拒绝：%s", path)
		}
	}
}

func TestFFmpegArgumentsPinLocalSingleFramePipeline(t *testing.T) {
	input := filepath.Join("root", "input.hevc")
	framehash := filepath.Join("root", "frames.sha256")
	png := filepath.Join("root", "output.png")
	preflight := framehashArguments(input, framehash)
	decode := pngArguments(input, png)
	expectedBase := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-n",
		"-protocol_whitelist", "file", "-f", "hevc", "-threads", "1", "-i", input,
		"-map", "0:v:0", "-an", "-sn", "-dn",
	}
	if !slices.Equal(preflight, append(slices.Clone(expectedBase),
		"-frames:v", "2", "-f", "framehash", "-hash", "sha256", framehash)) {
		t.Fatalf("帧数预检 argv 未被精确锁定：%v", preflight)
	}
	if !slices.Equal(decode, append(slices.Clone(expectedBase),
		"-frames:v", "1", "-threads", "1", "-c:v", "png", "-compression_level", "6", "-f", "image2", png)) {
		t.Fatalf("PNG 解码 argv 未被精确锁定：%v", decode)
	}
	for _, arguments := range [][]string{preflight, decode} {
		for _, required := range []string{"-nostdin", "-xerror", "-protocol_whitelist", "file", "-f", "hevc", "-threads", "1", "-map", "0:v:0", "-an", "-sn", "-dn"} {
			if !slices.Contains(arguments, required) {
				t.Fatalf("FFmpeg 参数缺少 %q：%v", required, arguments)
			}
		}
		for _, forbidden := range []string{"http", "https", "tcp", "udp", "pipe"} {
			if slices.Contains(arguments, forbidden) {
				t.Fatalf("FFmpeg 参数意外允许协议 %q：%v", forbidden, arguments)
			}
		}
	}
}

func TestCountFramehashRequiresExactlyParseableVideoRows(t *testing.T) {
	one := []byte("#format: frame checksums\n#stream#, dts, pts, duration, size, hash\n0, 0, 0, 1, 24, " + strings.Repeat("a", 64) + "\n")
	if count, err := countFramehash(one); err != nil || count != 1 {
		t.Fatalf("单帧 framehash 解析异常：count=%d err=%v", count, err)
	}
	two := append(one, []byte("0, 1, 1, 1, 24, "+strings.Repeat("b", 64)+"\n")...)
	if count, err := countFramehash(two); err != nil || count != 2 {
		t.Fatalf("多帧 framehash 解析异常：count=%d err=%v", count, err)
	}
	for _, invalid := range [][]byte{
		nil,
		[]byte("# only comments\n"),
		[]byte("1, bad\n"),
		[]byte("0, 0, 0, 1, 24, short\n"),
		[]byte("0, 0, 0, 1, 24, " + strings.Repeat("z", 64) + "\n"),
		[]byte("0, 0, 0, 1, 24, " + strings.Repeat("a", 64) + ", extra\n"),
	} {
		if _, err := countFramehash(invalid); err == nil {
			t.Fatal("无效 framehash 未被拒绝")
		}
	}
}

func TestAdjacentFFmpegUsesPlatformFileName(t *testing.T) {
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if filepath.Base(filepath.Join("qualification", name)) != name {
		t.Fatal("FFmpeg 平台文件名构造异常")
	}
}
