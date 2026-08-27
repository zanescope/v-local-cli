package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
)

const maxTranscriptBytes = 2 * 1024 * 1024

var newASRProviderCommand = func(ctx context.Context, executable string) *exec.Cmd {
	return exec.CommandContext(ctx, executable)
}

type voiceDependency struct {
	Backend        string
	Engine         string
	Provider       string
	Model          string
	EngineSource   string
	ProviderSource string
	ModelSource    string
}

type asrProviderRequest struct {
	Protocol          string `json:"protocol"`
	Action            string `json:"action"`
	AudioPath         string `json:"audio_path"`
	SourceAudioSHA256 string `json:"source_audio_sha256"`
	SampleRate        int    `json:"sample_rate"`
	Channels          int    `json:"channels"`
	Language          string `json:"language"`
	ModelPath         string `json:"model_path"`
}

type asrProviderResponse struct {
	Protocol    string `json:"protocol"`
	Transcript  string `json:"transcript"`
	Engine      string `json:"engine"`
	Model       string `json:"model"`
	Language    string `json:"language"`
	NetworkUsed *bool  `json:"network_used"`
}

type asrResult struct {
	Transcript string
	Engine     string
	Model      string
	Language   string
	Source     string
}

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
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		_, _ = writer.buffer.Write(payload[:remaining])
	}
	if len(payload) > remaining {
		writer.over = true
	}
	return len(payload), nil
}

func resolveExecutable(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	resolved, err := exec.LookPath(value)
	if err != nil {
		return "", false
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(canonical)
	return canonical, err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func resolveVoiceDependency(engineValue, modelValue, providerValue string) voiceDependency {
	dependency := voiceDependency{}
	engineExplicit := strings.TrimSpace(engineValue) != ""
	providerValue = strings.TrimSpace(providerValue)
	if providerValue != "" {
		dependency.ProviderSource = "flag"
	} else if !engineExplicit {
		providerValue = strings.TrimSpace(os.Getenv("V_LOCAL_CLI_ASR_PROVIDER"))
		if providerValue != "" {
			dependency.ProviderSource = "environment"
		}
	}
	if providerValue != "" {
		if provider, found := resolveExecutable(providerValue); found {
			dependency.Provider = provider
		}
		dependency.Backend = "external_provider"
	}
	engineValue = strings.TrimSpace(engineValue)
	if engineValue != "" {
		dependency.EngineSource = "flag"
	} else if engineValue = strings.TrimSpace(os.Getenv("V_LOCAL_CLI_WHISPER_BIN")); engineValue != "" {
		dependency.EngineSource = "environment"
	} else {
		engineValue = "whisper-cli"
		dependency.EngineSource = "path"
	}
	if engine, found := resolveExecutable(engineValue); found {
		dependency.Engine = engine
	}
	if dependency.Backend == "" {
		dependency.Backend = "whisper_cpp"
	}
	modelValue = strings.TrimSpace(modelValue)
	if modelValue != "" {
		dependency.ModelSource = "flag"
	} else if dependency.Backend == "external_provider" {
		modelValue = strings.TrimSpace(os.Getenv("V_LOCAL_CLI_ASR_MODEL"))
		if modelValue != "" {
			dependency.ModelSource = "environment"
		}
	} else if modelValue = strings.TrimSpace(os.Getenv("V_LOCAL_CLI_WHISPER_MODEL")); modelValue != "" {
		dependency.ModelSource = "environment"
	}
	if modelValue != "" {
		if absolute, err := filepath.Abs(modelValue); err == nil {
			if canonical, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
				info, statErr := os.Lstat(canonical)
				if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
					return dependency
				}
				if dependency.Backend == "external_provider" && (info.IsDir() || info.Size() > 0) {
					dependency.Model = canonical
				} else if dependency.Backend == "whisper_cpp" && !info.IsDir() && info.Size() > 0 {
					dependency.Model = canonical
				}
			}
		}
	}
	return dependency
}

func voiceDependencyData(dependency voiceDependency, showPaths bool) map[string]any {
	available := dependency.Model != "" && ((dependency.Backend == "external_provider" && dependency.Provider != "") || (dependency.Backend == "whisper_cpp" && dependency.Engine != ""))
	data := map[string]any{
		"transcription_backend_ready": available,
		"backend":                     dependency.Backend,
		"engine_found":                dependency.Engine != "",
		"provider_found":              dependency.Provider != "",
		"model_found":                 dependency.Model != "",
		"engine_source":               dependency.EngineSource,
		"provider_source":             dependency.ProviderSource,
		"model_source":                dependency.ModelSource,
		"dependency":                  map[string]any{"default": "whisper.cpp", "optional_provider_protocol": "v-local-cli-asr/1", "sensevoice_supported_via_provider": true},
		"local_processing":            true,
		"automatic_download":          false,
		"provider_invoked":            false,
		"cached_search_available_without_dependency": true,
		"install_consent_required":                   !available,
		"configuration": map[string]string{
			"whisper":  "--engine/--model 或 V_LOCAL_CLI_WHISPER_BIN/V_LOCAL_CLI_WHISPER_MODEL",
			"provider": "--asr-provider/--model 或 V_LOCAL_CLI_ASR_PROVIDER/V_LOCAL_CLI_ASR_MODEL",
		},
	}
	if showPaths {
		data["engine_path"] = dependency.Engine
		data["provider_path"] = dependency.Provider
		data["model_path"] = dependency.Model
	}
	return data
}

func voiceDependencyRequired(dependency voiceDependency, cached, missing int) error {
	return &commandError{
		typeName: "voice_dependency_required",
		message:  "完整的语音转写搜索需要本地 ASR 引擎和模型",
		hint:     "先询问用户是否安装可选的本地语音依赖；可选择 whisper.cpp，或通过 v-local-cli-asr/1 适配 SenseVoice。用户不同意时，增加 --cached-only 只搜索已有文字。",
		details: map[string]any{
			"dependency_options": []string{"whisper.cpp", "SenseVoice via v-local-cli-asr/1 provider"}, "backend": dependency.Backend,
			"engine_found": dependency.Engine != "", "provider_found": dependency.Provider != "", "model_found": dependency.Model != "",
			"cached_transcriptions": cached, "missing_transcriptions": missing,
			"install_consent_required": true, "network_install_performed": false,
		},
		code: 5,
	}
}

func voiceModelSupportsLanguage(model, language string) bool {
	if strings.EqualFold(strings.TrimSpace(language), "en") {
		return true
	}
	name := strings.ToLower(filepath.Base(model))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return !strings.HasSuffix(name, ".en")
}

func voiceModelLanguageMismatch(dependency voiceDependency, language string) error {
	return &commandError{
		typeName: "voice_model_language_mismatch",
		message:  "英文专用 whisper.cpp 模型不能用于中文或混合语言转写",
		hint:     "请选择名称不以 .en 结尾的多语言模型，或仅在英文语音时使用 --language en。",
		details: map[string]any{
			"dependency": "whisper.cpp", "model": filepath.Base(dependency.Model), "language": language,
			"network_install_performed": false,
		},
		code: 5,
	}
}

func validASRLanguage(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "zh", "yue", "en", "ja", "ko", "unknown":
		return true
	default:
		return false
	}
}

func validRequestedASRLanguage(value string) bool {
	return validASRLanguage(value) && !strings.EqualFold(strings.TrimSpace(value), "unknown")
}

func validASRMetadata(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func runVoiceStatus(args []string) (any, error) {
	set := flag.NewFlagSet("voice-status", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	engine := set.String("engine", "", "whisper-cli 路径")
	provider := set.String("asr-provider", "", "v-local-cli-asr/1 本地适配器路径")
	model := set.String("model", "", "本地 ASR 模型文件或目录")
	showPaths := set.Bool("show-paths", false, "显示本机绝对路径")
	if err := noExtraArguments(set, args); err != nil {
		return nil, invalidArguments("用法：v-local-cli voice-status [--account NAME] [--engine FILE | --asr-provider FILE] [--model PATH] [--show-paths]")
	}
	if strings.TrimSpace(*engine) != "" && strings.TrimSpace(*provider) != "" {
		return nil, invalidArguments("--engine 与 --asr-provider 不能同时使用")
	}
	data := voiceDependencyData(resolveVoiceDependency(*engine, *model, *provider), *showPaths)
	data["preferred_source"] = "wechat_index_probe+v-local-cli_private_cache"
	data["private_cache_rows"] = 0
	data["wechat_existing_index"] = map[string]any{
		"index_has_transcripts": false, "initialized_account_required": true, "read_only": true,
		"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
	}
	if value, err := resolveInitializedAccount(*account); err == nil {
		status, statusErr := store.WeChatTextIndexStatus(value.SnapshotPath)
		if statusErr != nil {
			return nil, statusErr
		}
		data["wechat_existing_index"] = map[string]any{
			"index_has_transcripts": status.VoiceIndexedRows > 0, "indexed_voice_rows": status.VoiceIndexedRows,
			"index_tables": status.VoiceIndexTables, "read_only": true, "initialized_account_required": false,
			"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
		}
		cachePath, cachePathErr := state.VoiceTranscriptPath(value.AccountID)
		if cachePathErr != nil {
			return nil, cachePathErr
		}
		cacheRows, cacheErr := store.VoiceTranscriptCount(cachePath)
		if cacheErr != nil {
			return nil, cacheErr
		}
		data["private_cache_rows"] = cacheRows
		return outputWithGeneration(data, value), nil
	}
	return data, nil
}

func voiceTranscriptFromWeChat(message store.Message, indexed store.WeChatIndexedText) store.VoiceTranscript {
	return store.VoiceTranscript{
		EvidenceID: message.EvidenceID, Chat: message.Chat, ServerID: message.ServerID,
		Timestamp: message.Timestamp, SortKey: message.SortKey, Sender: message.Sender,
		Transcript: indexed.Text, Engine: "wechat-existing-index", Model: "wechat-existing-index",
		Language: "unknown", Source: "wechat_existing_index",
	}
}

func attachExistingVoiceTranscripts(value state.AccountState, messages []store.Message, includePrivateCache bool) error {
	evidenceIDs := map[string]bool{}
	for _, message := range messages {
		if message.Kind == "voice" {
			evidenceIDs[message.EvidenceID] = true
		}
	}
	if len(evidenceIDs) == 0 {
		return nil
	}
	indexed, err := store.WeChatVoiceTexts(value.SnapshotPath, messages)
	if err != nil {
		return err
	}
	cached := map[string]store.VoiceTranscript{}
	if includePrivateCache {
		cachePath, err := state.VoiceTranscriptPath(value.AccountID)
		if err != nil {
			return err
		}
		cached, err = store.LoadVoiceTranscripts(cachePath, evidenceIDs)
		if err != nil {
			return err
		}
	}
	for index := range messages {
		if messages[index].Kind != "voice" {
			continue
		}
		text, source := "", ""
		if existing, found := indexed[messages[index].EvidenceID]; found {
			text, source = existing.Text, existing.Source
		} else if existing, found := cached[messages[index].EvidenceID]; found {
			text, source = existing.Transcript, existing.Source
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		messages[index].VoiceTranscript = text
		messages[index].VoiceTranscriptSource = source
		messages[index].Content += " 转文字：" + text
	}
	return nil
}

func runLocalWhisper(ctx context.Context, dependency voiceDependency, language string, wav []byte, temporaryDirectory string) (transcript string, returnErr error) {
	if dependency.Engine == "" || dependency.Model == "" {
		return "", errors.New("voice_dependency_required")
	}
	wavFile, err := os.CreateTemp(temporaryDirectory, "v-local-cli-voice-*.wav")
	if err != nil {
		return "", err
	}
	wavPath := wavFile.Name()
	cleanupPaths := []string{wavPath}
	defer func() {
		if cleanupErr := removeTemporaryFiles(cleanupPaths...); cleanupErr != nil {
			transcript = ""
			returnErr = errors.New("本地语音转写临时明文清理失败")
		}
	}()
	if err := wavFile.Chmod(0o600); err != nil {
		_ = wavFile.Close()
		return "", err
	}
	if _, err := wavFile.Write(wav); err != nil {
		_ = wavFile.Close()
		return "", err
	}
	if err := wavFile.Sync(); err != nil {
		_ = wavFile.Close()
		return "", err
	}
	if err := wavFile.Close(); err != nil {
		return "", err
	}
	baseFile, err := os.CreateTemp(temporaryDirectory, "v-local-cli-transcript-*")
	if err != nil {
		return "", err
	}
	outputBase := baseFile.Name()
	_ = baseFile.Close()
	cleanupPaths = append(cleanupPaths, outputBase, outputBase+".txt", outputBase+".json")
	if err := removeTemporaryFiles(outputBase); err != nil {
		return "", errors.New("本地语音转写临时明文清理失败")
	}
	command := exec.CommandContext(ctx, dependency.Engine,
		"-m", dependency.Model, "-f", wavPath, "-l", language,
		"-otxt", "-of", outputBase, "-np",
	)
	command.Dir = temporaryDirectory
	var diagnostic cappedBuffer
	diagnostic.limit = 16 * 1024
	command.Stdout = &diagnostic
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("本地语音转写超时")
		}
		return "", fmt.Errorf("本地语音转写引擎执行失败")
	}
	info, err := os.Stat(outputBase + ".txt")
	if err != nil || info.Size() <= 0 || info.Size() > maxTranscriptBytes {
		return "", errors.New("本地语音转写引擎没有生成有效文本")
	}
	payload, err := os.ReadFile(outputBase + ".txt")
	if err != nil {
		return "", err
	}
	transcript = strings.TrimSpace(strings.ToValidUTF8(string(payload), "�"))
	if transcript == "" {
		return "", errors.New("本地语音转写结果为空")
	}
	return transcript, nil
}

func runLocalASRProvider(ctx context.Context, dependency voiceDependency, language string, wav []byte, sourceDigest, temporaryDirectory string) (result asrResult, returnErr error) {
	if dependency.Provider == "" || dependency.Model == "" {
		return asrResult{}, errors.New("voice_dependency_required")
	}
	wavFile, err := os.CreateTemp(temporaryDirectory, "v-local-cli-voice-*.wav")
	if err != nil {
		return asrResult{}, err
	}
	wavPath := wavFile.Name()
	defer func() {
		if cleanupErr := removeTemporaryFiles(wavPath); cleanupErr != nil {
			result = asrResult{}
			returnErr = errors.New("本地 ASR 临时明文清理失败")
		}
	}()
	if err := wavFile.Chmod(0o600); err != nil {
		_ = wavFile.Close()
		return asrResult{}, err
	}
	if _, err := wavFile.Write(wav); err != nil {
		_ = wavFile.Close()
		return asrResult{}, err
	}
	if err := wavFile.Sync(); err != nil {
		_ = wavFile.Close()
		return asrResult{}, err
	}
	if err := wavFile.Close(); err != nil {
		return asrResult{}, err
	}
	request := asrProviderRequest{
		Protocol: "v-local-cli-asr/1", Action: "transcribe", AudioPath: wavPath,
		SourceAudioSHA256: sourceDigest, SampleRate: 16000, Channels: 1,
		Language: language, ModelPath: dependency.Model,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return asrResult{}, err
	}
	command := newASRProviderCommand(ctx, dependency.Provider)
	command.Dir = temporaryDirectory
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	var output, diagnostic cappedBuffer
	output.limit = maxTranscriptBytes
	diagnostic.limit = 16 * 1024
	command.Stdout = &output
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return asrResult{}, errors.New("本地语音转写超时")
		}
		return asrResult{}, errors.New("本地 ASR 适配器执行失败")
	}
	if output.over {
		return asrResult{}, errors.New("本地 ASR 适配器响应超过安全上限")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.buffer.Bytes()))
	decoder.DisallowUnknownFields()
	var response asrProviderResponse
	if err := decoder.Decode(&response); err != nil {
		return asrResult{}, errors.New("本地 ASR 适配器返回了无效响应")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || response.Protocol != "v-local-cli-asr/1" {
		return asrResult{}, errors.New("本地 ASR 适配器协议不匹配")
	}
	transcript := strings.TrimSpace(strings.ToValidUTF8(response.Transcript, "�"))
	if transcript == "" {
		return asrResult{}, errors.New("本地 ASR 适配器返回了空文本")
	}
	if response.NetworkUsed == nil {
		return asrResult{}, errors.New("本地 ASR 适配器没有声明网络使用状态")
	}
	if *response.NetworkUsed {
		return asrResult{}, errors.New("本地 ASR 适配器报告使用了网络，结果已拒绝")
	}
	engine := strings.TrimSpace(response.Engine)
	model := strings.TrimSpace(response.Model)
	detectedLanguage := strings.ToLower(strings.TrimSpace(response.Language))
	if !validASRMetadata(engine) || !validASRMetadata(model) || !validASRLanguage(detectedLanguage) {
		return asrResult{}, errors.New("本地 ASR 适配器缺少可信的 engine/model/language 元数据")
	}
	return asrResult{Transcript: transcript, Engine: engine, Model: model, Language: detectedLanguage, Source: "local_asr_provider"}, nil
}

func runLocalASR(ctx context.Context, dependency voiceDependency, language string, wav []byte, sourceDigest, temporaryDirectory string) (asrResult, error) {
	if dependency.Backend == "external_provider" {
		return runLocalASRProvider(ctx, dependency, language, wav, sourceDigest, temporaryDirectory)
	}
	text, err := runLocalWhisper(ctx, dependency, language, wav, temporaryDirectory)
	if err != nil {
		return asrResult{}, err
	}
	return asrResult{
		Transcript: text, Engine: filepath.Base(dependency.Engine),
		Model: filepath.Base(dependency.Model), Language: strings.ToLower(strings.TrimSpace(language)), Source: "local_whisper_cpp",
	}, nil
}

func transcribeVoiceMessage(value state.AccountState, cachePath string, message store.Message, dependency voiceDependency, language string) (store.VoiceTranscript, error) {
	payload, err := store.VoiceData(value.SnapshotPath, message.ServerID)
	if err != nil {
		return store.VoiceTranscript{}, err
	}
	wav, digest, err := store.DecodeVoiceWAV(payload)
	if err != nil {
		return store.VoiceTranscript{}, err
	}
	temporaryDirectory, err := state.EnsureExportTempPath(value.AccountID)
	if err != nil {
		return store.VoiceTranscript{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	result, err := runLocalASR(ctx, dependency, language, wav, digest, temporaryDirectory)
	if err != nil {
		return store.VoiceTranscript{}, err
	}
	transcript := store.VoiceTranscript{
		EvidenceID: message.EvidenceID, Chat: message.Chat, ServerID: message.ServerID,
		Timestamp: message.Timestamp, SortKey: message.SortKey, Sender: message.Sender,
		Transcript: result.Transcript, AudioSHA256: digest, Engine: result.Engine,
		Model: result.Model, Language: result.Language, Source: result.Source,
	}
	if err := store.SaveVoiceTranscript(cachePath, transcript); err != nil {
		return store.VoiceTranscript{}, err
	}
	return transcript, nil
}

func runVoiceTranscribe(args []string) (any, error) {
	set := flag.NewFlagSet("voice-transcribe", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	engine := set.String("engine", "", "whisper-cli 路径")
	provider := set.String("asr-provider", "", "v-local-cli-asr/1 本地适配器路径")
	model := set.String("model", "", "本地 ASR 模型文件或目录")
	language := set.String("language", "zh", "转写语言")
	force := set.Bool("force", false, "忽略已有暂存并重新转写")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || !validRequestedASRLanguage(*language) {
		return nil, invalidArguments("用法：v-local-cli voice-transcribe [--account NAME] [--engine FILE | --asr-provider FILE] [--model PATH] [--language zh] [--force] <voice_evidence_id>")
	}
	if strings.TrimSpace(*engine) != "" && strings.TrimSpace(*provider) != "" {
		return nil, invalidArguments("--engine 与 --asr-provider 不能同时使用")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	message, err := store.FindVoiceMessage(value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, &commandError{typeName: "voice_evidence_unavailable", message: err.Error(), hint: "先用 history 取得 kind=voice 的 evidence_id，并确认快照包含对应 media_*.db。", code: 5}
	}
	cachePath, err := state.VoiceTranscriptPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	if !*force {
		if indexed, indexErr := store.WeChatVoiceTexts(value.SnapshotPath, []store.Message{message}); indexErr != nil {
			return nil, indexErr
		} else if existing, found := indexed[message.EvidenceID]; found {
			return withGeneration(outputWithTimeWindow(map[string]any{
				"account": value.AccountName, "item": voiceTranscriptFromWeChat(message, existing),
				"cache_status": "not_written", "source": "wechat_existing_index", "local_processing": true,
				"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
			}, timeWindow{}, true), value), nil
		}
		if cached, found, loadErr := store.LoadVoiceTranscript(cachePath, message.EvidenceID); loadErr != nil {
			return nil, loadErr
		} else if found {
			return withGeneration(outputWithTimeWindow(map[string]any{
				"account": value.AccountName, "item": cached, "cache_status": "hit", "local_processing": true,
			}, timeWindow{}, true), value), nil
		}
	}
	dependency := resolveVoiceDependency(*engine, *model, *provider)
	if dependency.Model == "" || (dependency.Backend == "external_provider" && dependency.Provider == "") || (dependency.Backend == "whisper_cpp" && dependency.Engine == "") {
		return nil, voiceDependencyRequired(dependency, 0, 1)
	}
	if dependency.Backend == "whisper_cpp" && !voiceModelSupportsLanguage(dependency.Model, *language) {
		return nil, voiceModelLanguageMismatch(dependency, *language)
	}
	transcript, err := transcribeVoiceMessage(value, cachePath, message, dependency, *language)
	if err != nil {
		return nil, &commandError{typeName: "voice_transcription_failed", message: "语音转写失败", hint: "运行 voice-status 检查本地 ASR 引擎、适配器和模型；适配器必须遵循 v-local-cli-asr/1 且报告未使用网络。", details: map[string]any{"backend": dependency.Backend, "reason": err.Error()}, code: 5}
	}
	return withGeneration(outputWithTimeWindow(map[string]any{
		"account": value.AccountName, "item": transcript, "cache_status": "written", "local_processing": true,
	}, timeWindow{}, true), value), nil
}

func runVoiceSearch(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("voice-search", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	chat := set.String("chat", "", "限定会话 username")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多扫描和返回的语音条数")
	cachedOnly := set.Bool("cached-only", false, "只搜索已经暂存的转写")
	engine := set.String("engine", "", "whisper-cli 路径")
	provider := set.String("asr-provider", "", "v-local-cli-asr/1 本地适配器路径")
	model := set.String("model", "", "本地 ASR 模型文件或目录")
	language := set.String("language", "zh", "转写语言")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 || strings.TrimSpace(set.Args()[0]) == "" || !validRequestedASRLanguage(*language) {
		return nil, invalidArguments("用法：v-local-cli voice-search [--account NAME] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--cached-only] [--engine FILE | --asr-provider FILE] [--model PATH] [--language zh] <关键词>")
	}
	if strings.TrimSpace(*engine) != "" && strings.TrimSpace(*provider) != "" {
		return nil, invalidArguments("--engine 与 --asr-provider 不能同时使用")
	}
	window, err := resolveTimeWindow(*chat, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	cachePath, err := state.VoiceTranscriptPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	messages, err := store.VoiceMessages(value.SnapshotPath, *chat, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	cached := 0
	wechatIndexed := 0
	records := map[string]store.VoiceTranscript{}
	missing := []store.Message{}
	existing, err := store.WeChatVoiceTexts(value.SnapshotPath, messages)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if indexed, found := existing[message.EvidenceID]; found {
			records[message.EvidenceID] = voiceTranscriptFromWeChat(message, indexed)
			wechatIndexed++
			continue
		}
		record, found, loadErr := store.LoadVoiceTranscript(cachePath, message.EvidenceID)
		if loadErr != nil {
			return nil, loadErr
		}
		if found {
			cached++
			if record.Source == "" {
				record.Source = "v-local-cli_private_cache"
			}
			record.Chat = message.Chat
			record.ServerID = message.ServerID
			record.Timestamp = message.Timestamp
			record.SortKey = message.SortKey
			record.Sender = message.Sender
			records[message.EvidenceID] = record
		} else {
			missing = append(missing, message)
		}
	}
	transcribedNow := 0
	failedPayloads := 0
	if !*cachedOnly && len(missing) > 0 {
		dependency := resolveVoiceDependency(*engine, *model, *provider)
		if dependency.Model == "" || (dependency.Backend == "external_provider" && dependency.Provider == "") || (dependency.Backend == "whisper_cpp" && dependency.Engine == "") {
			return nil, voiceDependencyRequired(dependency, cached, len(missing))
		}
		if dependency.Backend == "whisper_cpp" && !voiceModelSupportsLanguage(dependency.Model, *language) {
			return nil, voiceModelLanguageMismatch(dependency, *language)
		}
		for _, message := range missing {
			record, transcribeErr := transcribeVoiceMessage(value, cachePath, message, dependency, *language)
			if transcribeErr != nil {
				failedPayloads++
				continue
			}
			records[message.EvidenceID] = record
			transcribedNow++
		}
	}
	keyword := strings.ToLower(strings.TrimSpace(set.Args()[0]))
	items := []store.VoiceTranscript{}
	for _, message := range messages {
		record, found := records[message.EvidenceID]
		if found && strings.Contains(strings.ToLower(record.Transcript), keyword) {
			items = append(items, record)
			if effectiveLimit > 0 && len(items) >= effectiveLimit {
				break
			}
		}
	}
	coverage := map[string]any{
		"scope":                    "locally_retained_voice_and_private_transcript_cache",
		"candidate_voice_messages": len(messages), "cached_before": cached, "missing_before": len(missing),
		"wechat_existing_index": wechatIndexed, "wechat_engine_invoked": false,
		"wechat_private_ipc_invoked": false, "wechat_network_performed": false,
		"transcribed_now": transcribedNow, "transcription_failures": failedPayloads,
		"cached_only": *cachedOnly, "candidate_limit_applied": effectiveLimit > 0,
		"complete":                              false,
		"complete_within_local_candidate_scope": !*cachedOnly && failedPayloads == 0 && effectiveLimit == 0,
		"external_network_used":                 false, "local_asr_only": true,
	}
	data := map[string]any{
		"account": value.AccountName, "query": set.Args()[0], "chat": *chat,
		"items": items, "count": len(items), "transcript_source_coverage": coverage,
	}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}
