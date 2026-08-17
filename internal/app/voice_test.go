package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestASRProviderHelper(t *testing.T) {
	if os.Getenv("V_LOCAL_CLI_TEST_ASR_HELPER") == "" {
		return
	}
	if os.Getenv("V_LOCAL_CLI_TEST_ASR_OVERSIZE") != "" {
		_, _ = os.Stdout.Write(make([]byte, maxTranscriptBytes+1))
		os.Exit(0)
	}
	var request asrProviderRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	if request.Protocol != "v-local-cli-asr/1" || request.Action != "transcribe" || request.SampleRate != 16000 || request.Channels != 1 {
		os.Exit(3)
	}
	if _, err := os.Stat(request.AudioPath); err != nil {
		os.Exit(4)
	}
	response := asrProviderResponse{
		Protocol: "v-local-cli-asr/1", Transcript: "本地识别结果", Engine: "sensevoice",
		Model: "sensevoice-small-int8", Language: request.Language, NetworkUsed: boolPointer(false),
	}
	if os.Getenv("V_LOCAL_CLI_TEST_ASR_NETWORK") != "" {
		response.NetworkUsed = boolPointer(true)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func boolPointer(value bool) *bool {
	return &value
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary plaintext files were not cleaned: %v", entries)
	}
}

func TestRunLocalASRProviderContract(t *testing.T) {
	previous := newASRProviderCommand
	defer func() { newASRProviderCommand = previous }()
	newASRProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestASRProviderHelper")
		command.Env = append(os.Environ(), "V_LOCAL_CLI_TEST_ASR_HELPER=1")
		return command
	}
	model := filepath.Join(t.TempDir(), "sensevoice-model")
	if err := os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	result, err := runLocalASRProvider(context.Background(), voiceDependency{
		Backend: "external_provider", Provider: os.Args[0], Model: model,
	}, "zh", []byte("RIFF-test"), "0123456789abcdef", temporary)
	if err != nil {
		t.Fatal(err)
	}
	if result.Transcript != "本地识别结果" || result.Engine != "sensevoice" || result.Source != "local_asr_provider" {
		t.Fatalf("适配器结果错误：%+v", result)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("临时音频没有清理：%v", entries)
	}
}

func TestRunLocalASRProviderRejectsNetwork(t *testing.T) {
	previous := newASRProviderCommand
	defer func() { newASRProviderCommand = previous }()
	newASRProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestASRProviderHelper")
		command.Env = append(os.Environ(), "V_LOCAL_CLI_TEST_ASR_HELPER=1", "V_LOCAL_CLI_TEST_ASR_NETWORK=1")
		return command
	}
	model := filepath.Join(t.TempDir(), "sensevoice-model")
	if err := os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	_, err := runLocalASRProvider(context.Background(), voiceDependency{
		Backend: "external_provider", Provider: os.Args[0], Model: model,
	}, "zh", []byte("RIFF-test"), "0123456789abcdef", temporary)
	if err == nil {
		t.Fatal("报告联网的适配器结果不应被接受")
	}
	assertDirectoryEmpty(t, temporary)
}

func TestRunLocalASRProviderRejectsOversizedResponse(t *testing.T) {
	previous := newASRProviderCommand
	defer func() { newASRProviderCommand = previous }()
	newASRProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestASRProviderHelper")
		command.Env = append(os.Environ(), "V_LOCAL_CLI_TEST_ASR_HELPER=1", "V_LOCAL_CLI_TEST_ASR_OVERSIZE=1")
		return command
	}
	model := filepath.Join(t.TempDir(), "sensevoice-model")
	if err := os.Mkdir(model, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	_, err := runLocalASRProvider(context.Background(), voiceDependency{
		Backend: "external_provider", Provider: os.Args[0], Model: model,
	}, "zh", []byte("RIFF-test"), "0123456789abcdef", temporary)
	if err == nil || !strings.Contains(err.Error(), "超过安全上限") {
		t.Fatalf("超大适配器响应未被明确拒绝：%v", err)
	}
	assertDirectoryEmpty(t, temporary)
}

func TestRunLocalASRProviderCleansTemporaryAudioWhenCommandCannotStart(t *testing.T) {
	previous := newASRProviderCommand
	defer func() { newASRProviderCommand = previous }()
	newASRProviderCommand = func(ctx context.Context, _ string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-asr-provider"))
	}
	temporary := t.TempDir()
	_, err := runLocalASRProvider(context.Background(), voiceDependency{
		Backend: "external_provider", Provider: "missing", Model: "model",
	}, "zh", []byte("RIFF-test"), "0123456789abcdef", temporary)
	if err == nil {
		t.Fatal("missing ASR provider should fail")
	}
	assertDirectoryEmpty(t, temporary)
}

func TestRunLocalWhisperCleansTemporaryPlaintextWhenCommandFails(t *testing.T) {
	temporary := t.TempDir()
	_, err := runLocalWhisper(context.Background(), voiceDependency{
		Engine: filepath.Join(t.TempDir(), "missing-whisper"), Model: "model",
	}, "zh", []byte("RIFF-test"), temporary)
	if err == nil {
		t.Fatal("missing whisper engine should fail")
	}
	assertDirectoryEmpty(t, temporary)
}
