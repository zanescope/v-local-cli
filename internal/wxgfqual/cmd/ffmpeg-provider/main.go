// ffmpeg-provider is a qualification-only adapter. It is intentionally kept
// below internal/ and is not built or distributed with v-local-cli releases.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/wxgfqual"
)

const (
	maximumInputBytes      = wxgfqual.MaxWXGFBytes
	maximumOutputBytes     = 64 * 1024 * 1024
	maximumFramehashBytes  = 64 * 1024
	maximumDiagnosticBytes = 16 * 1024
	adapterTimeout         = 25 * time.Second
)

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
	over   bool
}

func (writer *cappedBuffer) Write(payload []byte) (int, error) {
	remaining := writer.limit - writer.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	if remaining > len(payload) {
		remaining = len(payload)
	}
	if remaining > 0 {
		_, _ = writer.buffer.Write(payload[:remaining])
	}
	if len(payload) > remaining {
		writer.over = true
	}
	return len(payload), nil
}

func fail(message string) int {
	_, _ = fmt.Fprintln(os.Stderr, message)
	return 1
}

func decodeRequest(reader io.Reader) (wxgfqual.ProviderRequest, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, 64*1024+1))
	if err != nil || len(payload) == 0 || len(payload) > 64*1024 {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request wxgfqual.ProviderRequest
	if err := decoder.Decode(&request); err != nil {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	if request.Protocol != wxgfqual.ProviderProtocol || request.Action != "decode_still" ||
		request.InputFormat != "hevc_annex_b" || request.OutputFormat != "png" ||
		request.MaximumFrames != 1 || request.MaximumPixels != cryptoutil.MaxDecodedImagePixels || request.NetworkAllowed ||
		len(request.RequestID) != 32 || len(request.InputSHA256) != sha256.Size*2 ||
		request.DecoderName != "ffmpeg" || request.DecoderIdentityBasis != wxgfqual.ProviderIdentityBasis {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	if _, err := hex.DecodeString(request.RequestID); err != nil {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	if _, err := hex.DecodeString(request.InputSHA256); err != nil || strings.ToLower(request.InputSHA256) != request.InputSHA256 {
		return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
	}
	for _, digest := range []string{request.ProviderIdentityManifestSHA256, request.ProviderSHA256, request.DecoderSHA256} {
		if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
			return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return wxgfqual.ProviderRequest{}, errors.New("invalid_request")
		}
	}
	return request, nil
}

func pathInsideStage(path, stage, expectedBase string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("invalid_path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Base(absolute) != expectedBase {
		return "", errors.New("invalid_path")
	}
	relative, err := filepath.Rel(stage, absolute)
	if err != nil || relative != expectedBase || filepath.IsAbs(relative) {
		return "", errors.New("invalid_path")
	}
	return absolute, nil
}

func readRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("invalid_file")
	}
	return os.ReadFile(path)
}

func digestRegularFile(path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return "", errors.New("invalid_file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("invalid_file")
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximum+1))
	if err != nil || written != info.Size() || written > maximum {
		return "", errors.New("invalid_file")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func adjacentFFmpeg() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("ffmpeg_unavailable")
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(filepath.Dir(executable), name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
		return "", errors.New("ffmpeg_unavailable")
	}
	return path, nil
}

func adjacentIdentityManifest() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("identity_manifest_unavailable")
	}
	path := executable + ".manifest.json"
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 64*1024 {
		return "", errors.New("identity_manifest_unavailable")
	}
	return path, nil
}

func baseFFmpegArguments(input string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-n",
		"-protocol_whitelist", "file", "-f", "hevc", "-threads", "1", "-i", input,
		"-map", "0:v:0", "-an", "-sn", "-dn",
	}
}

func framehashArguments(input, output string) []string {
	arguments := baseFFmpegArguments(input)
	return append(arguments, "-frames:v", "2", "-f", "framehash", "-hash", "sha256", output)
}

func pngArguments(input, output string) []string {
	arguments := baseFFmpegArguments(input)
	return append(arguments, "-frames:v", "1", "-threads", "1", "-c:v", "png", "-compression_level", "6", "-f", "image2", output)
}

func runFFmpeg(ctx context.Context, executable, stage string, arguments []string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = stage
	command.Env = os.Environ()
	command.Stdin = bytes.NewReader(nil)
	diagnostic := &cappedBuffer{limit: maximumDiagnosticBytes}
	command.Stdout = diagnostic
	command.Stderr = diagnostic
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil || diagnostic.over || ctx.Err() != nil {
		return errors.New("ffmpeg_failed")
	}
	return nil
}

func countFramehash(payload []byte) (int, error) {
	if len(payload) == 0 || len(payload) > maximumFramehashBytes {
		return 0, errors.New("invalid_framehash")
	}
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(payload), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) != 6 || strings.TrimSpace(fields[0]) != "0" {
			return 0, errors.New("invalid_framehash")
		}
		hash := strings.TrimSpace(fields[5])
		if len(hash) != sha256.Size*2 {
			return 0, errors.New("invalid_framehash")
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return 0, errors.New("invalid_framehash")
		}
		count++
		if count > 1 {
			return count, nil
		}
	}
	if count == 0 {
		return 0, errors.New("invalid_framehash")
	}
	return count, nil
}

func run() int {
	if len(os.Args) != 2 || os.Args[1] != "decode-still" {
		return fail("invalid_action")
	}
	request, err := decodeRequest(os.Stdin)
	if err != nil {
		return fail("invalid_request")
	}
	stage, err := filepath.Abs(".")
	if err != nil {
		return fail("invalid_stage")
	}
	inputPath, err := pathInsideStage(request.InputPath, stage, "input.hevc")
	if err != nil {
		return fail("invalid_input_path")
	}
	outputPath, err := pathInsideStage(request.OutputPath, stage, "output.png")
	if err != nil {
		return fail("invalid_output_path")
	}
	if _, err := os.Lstat(outputPath); !os.IsNotExist(err) {
		return fail("output_exists")
	}
	inputDigest, err := digestRegularFile(inputPath, maximumInputBytes)
	if err != nil || inputDigest != request.InputSHA256 {
		return fail("input_binding_failed")
	}
	providerPath, err := os.Executable()
	if err != nil {
		return fail("provider_identity_unavailable")
	}
	providerDigest, err := digestRegularFile(providerPath, 256*1024*1024)
	if err != nil || providerDigest != request.ProviderSHA256 {
		return fail("provider_identity_mismatch")
	}
	manifestPath, err := adjacentIdentityManifest()
	if err != nil {
		return fail("identity_manifest_unavailable")
	}
	manifestDigest, err := digestRegularFile(manifestPath, 64*1024)
	if err != nil || manifestDigest != request.ProviderIdentityManifestSHA256 {
		return fail("identity_manifest_mismatch")
	}
	ffmpegPath, err := adjacentFFmpeg()
	if err != nil {
		return fail("ffmpeg_unavailable")
	}
	ffmpegDigest, err := digestRegularFile(ffmpegPath, 1024*1024*1024)
	if err != nil || ffmpegDigest != request.DecoderSHA256 {
		return fail("ffmpeg_unavailable")
	}
	decoderVersion := "sha256:" + request.DecoderSHA256
	framehashPath := filepath.Join(stage, "frames.sha256")
	defer os.Remove(framehashPath)
	if _, err := os.Lstat(framehashPath); !os.IsNotExist(err) {
		return fail("frame_validation_output_exists")
	}

	ctx, cancel := context.WithTimeout(context.Background(), adapterTimeout)
	defer cancel()
	if err := runFFmpeg(ctx, ffmpegPath, stage, framehashArguments(inputPath, framehashPath)); err != nil {
		return fail("ffmpeg_frame_validation_failed")
	}
	framehash, err := readRegularFile(framehashPath, maximumFramehashBytes)
	if err != nil {
		return fail("frame_validation_failed")
	}
	frameCount, err := countFramehash(framehash)
	if err != nil || frameCount != 1 {
		return fail("not_single_frame")
	}
	if err := runFFmpeg(ctx, ffmpegPath, stage, pngArguments(inputPath, outputPath)); err != nil {
		return fail("ffmpeg_png_failed")
	}
	ffmpegDigestAfter, ffmpegErr := digestRegularFile(ffmpegPath, 1024*1024*1024)
	providerDigestAfter, providerErr := digestRegularFile(providerPath, 256*1024*1024)
	manifestDigestAfter, manifestErr := digestRegularFile(manifestPath, 64*1024)
	if ffmpegErr != nil || providerErr != nil || manifestErr != nil || ffmpegDigestAfter != request.DecoderSHA256 ||
		providerDigestAfter != request.ProviderSHA256 || manifestDigestAfter != request.ProviderIdentityManifestSHA256 {
		return fail("binary_identity_changed_during_decode")
	}
	outputDigest, err := digestRegularFile(outputPath, maximumOutputBytes)
	if err != nil {
		return fail("png_output_invalid")
	}
	networkUsed := false
	response := wxgfqual.ProviderResponse{
		Protocol: wxgfqual.ProviderProtocol, RequestID: request.RequestID, Status: "decoded",
		InputSHA256: request.InputSHA256, OutputSHA256: outputDigest, OutputFormat: "png",
		FrameCount: 1, NetworkUsed: &networkUsed, Decoder: request.DecoderName, DecoderVersion: decoderVersion,
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		return fail("response_failed")
	}
	return 0
}

func main() {
	os.Exit(run())
}
