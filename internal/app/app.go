package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	"github.com/zanescope/v-local-cli/internal/provider"
	"github.com/zanescope/v-local-cli/internal/snapshot"
	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
)

var Version = "0.1.0-dev.1"

const responseSchemaVersion = 1

const setupUsage = "v-local-cli setup [--dry-run] [--account NAME] [--provider FILE] [--allow-key-access | --keys FILE] [--storage keychain|snapshot-only] [--require-media] [--allow-coverage-regression] [--show-paths]"

type envelope struct {
	SchemaVersion int            `json:"schema_version"`
	OK            bool           `json:"ok"`
	Data          any            `json:"data,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
	Error         *errorValue    `json:"error,omitempty"`
}

type commandOutput struct {
	data any
	meta map[string]any
}

type countingWriter struct {
	writer io.Writer
	bytes  int64
}

func (writer *countingWriter) Write(payload []byte) (int, error) {
	written, err := writer.writer.Write(payload)
	writer.bytes += int64(written)
	return written, err
}

type timeWindow struct {
	Mode           string  `json:"mode"`
	ChatType       string  `json:"chat_type"`
	DefaultApplied bool    `json:"default_applied"`
	Start          *string `json:"start"`
	End            *string `json:"end"`
	StartTimestamp *int64  `json:"start_ts"`
	EndTimestamp   *int64  `json:"end_ts"`
}

type errorValue struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Details any    `json:"details,omitempty"`
}

type commandError struct {
	typeName string
	message  string
	hint     string
	details  any
	code     int
}

func (err *commandError) Error() string { return err.message }

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeError(stderr, &commandError{typeName: "missing_command", message: "缺少命令", hint: "运行 v-local-cli --help 查看可用命令。", code: 2})
		return 2
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(stdout, Version)
		return 0
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		writeHelp(stdout)
		return 0
	}
	freshRequested := false
	var freshErr error
	args, freshRequested, freshErr = prepareFreshQuery(args)
	if freshErr != nil {
		var commandErr *commandError
		if !errors.As(freshErr, &commandErr) {
			commandErr = &commandError{typeName: "fresh_failed", message: "刷新快照失败", hint: freshErr.Error(), code: 5}
		}
		writeError(stderr, commandErr)
		return commandErr.code
	}
	var data any
	var err error
	switch args[0] {
	case "accounts":
		data, err = runAccounts(args[1:])
	case "status":
		data, err = runStatus(args[1:])
	case "provider":
		data, err = runProvider(args[1:])
	case "install":
		data, err = runInstall(args[1:], stdout, stderr)
	case "doctor":
		data, err = runDoctor(args[1:])
	case "capabilities":
		data, err = runCapabilities(args[1:])
	case "setup":
		data, err = runSetup(args[1:])
	case "refresh":
		data, err = runRefresh(args[1:])
	case "forget":
		data, err = runForget(args[1:])
	case "gc":
		data, err = runGC(args[1:])
	case "schema":
		data, err = runSchema(args[1:])
	case "contacts":
		data, err = runContacts(args[1:])
	case "history":
		data, err = runHistory(args[1:])
	case "search":
		data, err = runSearch(args[1:])
	case "voice-status":
		data, err = runVoiceStatus(args[1:])
	case "voice-transcribe":
		data, err = runVoiceTranscribe(args[1:])
	case "voice-search":
		data, err = runVoiceSearch(args[1:])
	case "ocr-status":
		data, err = runOCRStatus(args[1:])
	case "ocr-file":
		data, err = runOCRFile(args[1:])
	case "ocr-recognize":
		data, err = runOCRRecognize(args[1:])
	case "ocr-read":
		data, err = runOCRRead(args[1:])
	case "ocr-search":
		data, err = runOCRSearch(args[1:])
	case "stats":
		data, err = runStats(args[1:])
	case "moments-contacts":
		data, err = runMomentContacts(args[1:])
	case "moments":
		data, err = runMoments(args[1:])
	case "moments-search":
		data, err = runMomentSearch(args[1:])
	case "official-accounts":
		data, err = runOfficialAccounts(args[1:])
	case "official-history":
		data, err = runOfficialHistory(args[1:])
	case "official-search":
		data, err = runOfficialSearch(args[1:])
	case "official-article":
		data, err = runOfficialArticle(args[1:])
	case "export":
		data, err = runExport(args[1:])
	case "export-media":
		data, err = runExportMedia(args[1:])
	case "export-moment-media":
		data, err = runExportMomentMedia(args[1:])
	default:
		err = &commandError{typeName: "unknown_command", message: fmt.Sprintf("未知命令 %q", args[0]), hint: "运行 v-local-cli --help 查看当前版本实际支持的命令。", code: 2}
	}
	if err != nil {
		var commandErr *commandError
		if !errors.As(err, &commandErr) {
			commandErr = &commandError{
				typeName: "internal_error", message: "命令执行失败",
				hint: "运行 v-local-cli doctor 获取脱敏诊断信息后重试。", code: 1,
			}
		}
		writeError(stderr, commandErr)
		return commandErr.code
	}
	meta := map[string]any{"version": Version, "runtime": "go"}
	if output, ok := data.(commandOutput); ok {
		data = output.data
		for name, value := range output.meta {
			meta[name] = value
		}
	}
	if freshRequested {
		meta["fresh_requested"] = true
		meta["fresh_completed"] = true
	}
	if _, found := meta["snapshot_created_at"]; !found {
		meta["snapshot_created_at"] = nil
	}
	if _, found := meta["snapshot_age_seconds"]; !found {
		meta["snapshot_age_seconds"] = nil
	}
	writeJSON(stdout, envelope{SchemaVersion: responseSchemaVersion, OK: true, Data: data, Meta: meta})
	return 0
}

func prepareFreshQuery(args []string) ([]string, bool, error) {
	if len(args) < 2 {
		return args, false, nil
	}
	fresh := false
	filtered := []string{args[0]}
	for _, argument := range args[1:] {
		switch argument {
		case "--fresh", "-fresh", "--fresh=true", "-fresh=true":
			fresh = true
			continue
		case "--fresh=false", "-fresh=false":
			continue
		}
		filtered = append(filtered, argument)
	}
	if !fresh {
		return filtered, false, nil
	}
	supported := map[string]bool{
		"contacts": true, "history": true, "search": true, "stats": true,
		"moments-contacts": true, "moments": true, "moments-search": true,
		"official-accounts": true, "official-history": true, "official-search": true, "official-article": true,
		"voice-status": true, "voice-transcribe": true, "voice-search": true,
		"ocr-status": true, "ocr-recognize": true, "ocr-read": true, "ocr-search": true,
		"export": true, "export-media": true, "export-moment-media": true,
	}
	if !supported[args[0]] {
		return filtered, false, invalidArguments("--fresh 只用于读取微信快照的查询、识别和导出命令")
	}
	selector := ""
	for index := 1; index < len(filtered); index++ {
		argument := filtered[index]
		if strings.HasPrefix(argument, "--account=") || strings.HasPrefix(argument, "-account=") {
			selector = strings.TrimSpace(strings.SplitN(argument, "=", 2)[1])
			break
		}
		if argument == "--account" || argument == "-account" {
			if index+1 < len(filtered) {
				selector = filtered[index+1]
			}
			break
		}
	}
	_, err := resolveQueryAccount(selector, true)
	return filtered, true, err
}

func noExtraArguments(set *flag.FlagSet, args []string) error {
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 {
		return errors.New("invalid")
	}
	return nil
}

func flagProvided(args []string, name string) bool {
	for _, argument := range args {
		if argument == "--"+name || argument == "-"+name ||
			strings.HasPrefix(argument, "--"+name+"=") || strings.HasPrefix(argument, "-"+name+"=") {
			return true
		}
	}
	return false
}

func effectiveResultLimit(all, explicit bool, configured int) int {
	if all && !explicit {
		return 0
	}
	return configured
}

func parseLocalDate(value string, end bool, location *time.Location) (*string, *int64, error) {
	if value == "" {
		return nil, nil, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return nil, nil, &commandError{
			typeName: "invalid_date", message: fmt.Sprintf("日期 %q 无效", value),
			hint: "日期必须使用 YYYY-MM-DD 格式。", code: 2,
		}
	}
	if end {
		parsed = parsed.AddDate(0, 0, 1).Add(-time.Second)
	}
	formatted := parsed.Format("2006-01-02")
	timestamp := parsed.Unix()
	return &formatted, &timestamp, nil
}

func resolveTimeWindow(chat, startValue, endValue string, all bool, now time.Time) (timeWindow, error) {
	chatType := "contact"
	if chat == "" {
		chatType = "cross_chat"
	} else if strings.HasSuffix(chat, "@chatroom") {
		chatType = "group"
	}
	if all && (startValue != "" || endValue != "") {
		return timeWindow{}, &commandError{
			typeName: "conflicting_time_window", message: "--all 不能与 --start/--end 同时使用",
			hint: "查询全部本地时间范围只使用 --all；查询指定日期则去掉 --all。", code: 2,
		}
	}
	if all {
		return timeWindow{Mode: "all", ChatType: chatType}, nil
	}
	location := now.Location()
	if startValue != "" || endValue != "" {
		start, startTimestamp, err := parseLocalDate(startValue, false, location)
		if err != nil {
			return timeWindow{}, err
		}
		end, endTimestamp, err := parseLocalDate(endValue, true, location)
		if err != nil {
			return timeWindow{}, err
		}
		if startTimestamp != nil && endTimestamp != nil && *startTimestamp > *endTimestamp {
			return timeWindow{}, &commandError{
				typeName: "invalid_time_window", message: "开始日期晚于结束日期",
				hint: "调整 --start 和 --end，使开始日期不晚于结束日期。", code: 2,
			}
		}
		return timeWindow{
			Mode: "explicit", ChatType: chatType, Start: start, End: end,
			StartTimestamp: startTimestamp, EndTimestamp: endTimestamp,
		}, nil
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	dayEnd := dayStart.AddDate(0, 0, 1).Add(-time.Second)
	start := dayStart
	mode := "default_group_day"
	if chatType == "contact" {
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
		mode = "default_contact_month"
	} else if chatType == "cross_chat" {
		mode = "default_cross_chat_day"
	}
	startText, endText := start.Format("2006-01-02"), dayEnd.Format("2006-01-02")
	startTimestamp, endTimestamp := start.Unix(), dayEnd.Unix()
	return timeWindow{
		Mode: mode, ChatType: chatType, DefaultApplied: true,
		Start: &startText, End: &endText,
		StartTimestamp: &startTimestamp, EndTimestamp: &endTimestamp,
	}, nil
}

func outputWithTimeWindow(data any, window timeWindow, untrusted bool) commandOutput {
	meta := map[string]any{"time_window": window}
	if untrusted {
		meta["untrusted"] = true
	}
	return commandOutput{data: data, meta: meta}
}

func outputWithQueryMetadata(data any, window timeWindow, untrusted bool, limit int, explicit bool) commandOutput {
	output := outputWithTimeWindow(data, window, untrusted)
	output.meta["limit_explicit"] = explicit
	output.meta["unbounded_by_limit"] = limit == 0
	if limit == 0 {
		output.meta["result_limit"] = nil
	} else {
		output.meta["result_limit"] = limit
	}
	return output
}

func withGeneration(output commandOutput, value state.AccountState) commandOutput {
	if output.meta == nil {
		output.meta = map[string]any{}
	}
	output.meta["generation_id"] = value.GenerationID
	output.meta["snapshot_manifest_sha256"] = value.SnapshotManifestSHA256
	createdAt, age := snapshotAge(value)
	output.meta["snapshot_created_at"] = createdAt
	output.meta["snapshot_age_seconds"] = age
	return output
}

func snapshotAge(value state.AccountState) (string, any) {
	createdAt := value.SnapshotCreatedAt
	if createdAt == "" {
		createdAt = value.UpdatedAt
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil || createdAt == "" {
		return createdAt, nil
	}
	age := int64(time.Since(parsed).Seconds())
	if age < 0 {
		age = 0
	}
	return createdAt, age
}

func outputWithGeneration(data any, value state.AccountState) commandOutput {
	return withGeneration(commandOutput{data: data, meta: map[string]any{}}, value)
}

func publicLocalAccount(account localplatform.Account, showPaths bool) map[string]any {
	value := map[string]any{
		"name": account.Name, "account_id": state.AccountID(account.Path),
	}
	if showPaths {
		value["path"] = account.Path
		value["db_dir"] = account.DBDir
	}
	return value
}

func publicLocalAccounts(accounts []localplatform.Account, showPaths bool) []map[string]any {
	values := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		values = append(values, publicLocalAccount(account, showPaths))
	}
	return values
}

func publicAccountState(value state.AccountState, showPaths bool) map[string]any {
	createdAt, age := snapshotAge(value)
	result := map[string]any{
		"version": value.Version, "account_id": value.AccountID, "account_name": value.AccountName,
		"generation_id": value.GenerationID, "snapshot_manifest_sha256": value.SnapshotManifestSHA256,
		"snapshot_created_at": createdAt, "snapshot_age_seconds": age,
		"updated_at": value.UpdatedAt, "storage": value.Storage, "database": value.Database, "media": value.Media,
	}
	if showPaths {
		result["account_path"] = value.AccountPath
		result["snapshot_path"] = value.SnapshotPath
	}
	return result
}

func publicAccountStates(values []state.AccountState, showPaths bool) []map[string]any {
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		result = append(result, publicAccountState(value, showPaths))
	}
	return result
}

func publicProviderStatus(value provider.Status, showPaths bool) map[string]any {
	result := map[string]any{
		"available": value.Available, "source": value.Source, "name": value.Name,
		"platform": value.Platform, "protocol": value.Protocol, "integrity": value.Integrity,
		"helper_required": value.HelperRequired, "helper_available": value.HelperAvailable,
		"helper_integrity": value.HelperIntegrity,
	}
	if showPaths && value.Path != "" {
		result["path"] = value.Path
	}
	if value.HelperName != "" {
		result["helper_name"] = value.HelperName
	}
	if showPaths && value.HelperPath != "" {
		result["helper_path"] = value.HelperPath
	}
	return result
}

func runAccounts(args []string) (any, error) {
	set := flag.NewFlagSet("accounts", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	showPaths := set.Bool("show-paths", false, "显示本机绝对路径")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli accounts [--show-paths]")
	}
	return map[string]any{"accounts": publicLocalAccounts(localplatform.Accounts(), *showPaths), "paths_included": *showPaths}, nil
}

func runStatus(args []string) (any, error) {
	set := flag.NewFlagSet("status", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	showPaths := set.Bool("show-paths", false, "显示本机绝对路径")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli status [--show-paths]")
	}
	accounts := localplatform.Accounts()
	initialized, err := state.List()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"platform": runtime.GOOS, "accounts": publicLocalAccounts(accounts, *showPaths),
		"account_count": len(accounts), "no_accounts": len(accounts) == 0,
		"initialized_accounts": publicAccountStates(initialized, *showPaths), "paths_included": *showPaths,
	}, nil
}

func runProvider(args []string) (any, error) {
	if len(args) == 0 || args[0] != "status" {
		return nil, invalidArguments("用法：v-local-cli provider status [--path FILE]")
	}
	set := flag.NewFlagSet("provider", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	path := set.String("path", "", "显式指定密钥提供器路径")
	showPaths := set.Bool("show-paths", false, "显示本机绝对路径")
	if noExtraArguments(set, args[1:]) != nil {
		return nil, invalidArguments("用法：v-local-cli provider status [--path FILE] [--show-paths]")
	}
	return publicProviderStatus(provider.Current(*path), *showPaths), nil
}

func runInstall(args []string, _, _ io.Writer) (any, error) {
	set := flag.NewFlagSet("install", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	dryRun := set.Bool("dry-run", false, "只显示安装动作")
	skipSkill := set.Bool("skip-skill", false, "不安装 Agent Skill")
	showPaths := set.Bool("show-paths", false, "显示安装目标路径")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli install [--dry-run] [--skip-skill] [--show-paths]")
	}
	actions := []string{"校验当前 Go CLI"}
	if !*skipSkill {
		actions = append(actions, "安装 v-local-cli Agent Skill")
	}
	actions = append(actions, "运行 v-local-cli setup --dry-run")
	if *dryRun {
		return map[string]any{"status": "planned", "actions": actions, "provider": publicProviderStatus(provider.Current(""), *showPaths)}, nil
	}
	if !*skipSkill {
		installed, err := installBundledSkill(*showPaths)
		if err != nil {
			return nil, err
		}
		actions = append(actions, installed...)
	}
	return map[string]any{"status": "installed", "actions": actions, "next": "v-local-cli setup --dry-run", "provider": publicProviderStatus(provider.Current(""), *showPaths)}, nil
}

func runDoctor(args []string) (any, error) {
	set := flag.NewFlagSet("doctor", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	providerPath := set.String("provider", "", "显式指定密钥提供器路径")
	showPaths := set.Bool("show-paths", false, "显示本机绝对路径")
	bundlePath := set.String("bundle", "", "写入脱敏诊断 JSON 文件")
	force := set.Bool("force", false, "覆盖已存在的诊断文件")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli doctor [--provider FILE] [--show-paths] [--bundle FILE] [--force]")
	}
	accounts := localplatform.Accounts()
	initialized, err := state.List()
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"platform": runtime.GOOS, "arch": runtime.GOARCH,
		"accounts": publicLocalAccounts(accounts, *showPaths), "account_count": len(accounts),
		"initialized_accounts": publicAccountStates(initialized, *showPaths), "key_provider": publicProviderStatus(provider.Current(*providerPath), *showPaths),
		"paths_included": *showPaths,
		"checks":         map[string]bool{"single_go_binary": true, "readonly_snapshot": true, "immutable_generations": true},
	}
	if strings.TrimSpace(*bundlePath) != "" {
		bundle, err := writeDiagnosticBundle(*bundlePath, *force, initialized, provider.Current(*providerPath))
		if err != nil {
			return nil, err
		}
		if *showPaths {
			result["diagnostic_bundle"] = bundle
		} else {
			result["diagnostic_bundle"] = map[string]any{
				"bytes": bundle["bytes"], "sha256": bundle["sha256"], "sanitized": bundle["sanitized"],
			}
		}
	}
	return result, nil
}

func runCapabilities(args []string) (any, error) {
	if len(args) != 0 {
		return nil, invalidArguments("用法：v-local-cli capabilities")
	}
	return map[string]any{
		"cli_version": Version, "response_schema_version": responseSchemaVersion,
		"runtime":       map[string]any{"language": "go", "os": runtime.GOOS, "arch": runtime.GOARCH, "cgo_required": false},
		"build_targets": []string{"windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"},
		"data_layout_validation": map[string]any{
			"windows_amd64": "real_device_verified", "windows_arm64": "build_only",
			"darwin_amd64": "real_device_verified", "darwin_arm64": "build_only",
			"linux":        "build_only_import_or_explicit_path",
		},
		"provider": map[string]any{
			"protocol": provider.Protocol, "separate_repository": true, "required_for_refresh": false,
			"automatic_key_access_real_device_verified_targets": []string{"windows/amd64", "darwin/amd64"},
			"darwin_amd64_setup_source":                         "automatic_or_user_supplied_candidate_file",
			"darwin_arm64_setup_source":                         "user_supplied_candidate_file",
			"darwin_arm64_automatic_helper":                     "experimental_build_only",
		},
		"storage": map[string]any{
			"immutable_generations": true, "manifest_schema_version": 1, "retained_rollback_generations": 1,
			"query_snapshot_timestamps": true, "fresh_query_refresh": true,
		},
		"query": map[string]any{
			"contacts": true, "history": true, "search": true, "stats": true, "moments": true, "official_cards": true,
			"voice_transcripts": true, "wechat_existing_voice_text": true, "wechat_existing_ocr_text": true, "official_article_body": true,
			"structured_cards":      []string{"contact_card", "mini_program", "channels", "red_packet", "forward", "quote"},
			"group_identity_fields": true, "emoji_normalization": "wechat_known_brackets_to_unicode",
			"all_without_limit": "unbounded", "unbounded_export": "disk_spooled_stream",
		},
		"media": map[string]any{
			"local_images": true, "remote_images": []string{"jpeg", "png", "gif"},
			"remote_video": []string{"mp4"}, "live_photo_video": false, "network_default": false,
		},
		"voice": map[string]any{
			"preferred_source": "wechat_existing_index", "silk_decoder_bundled": true, "fallback_asr_engine": "optional_whisper_cpp_or_v-local-cli-asr_provider", "network": false,
			"wechat_private_api": false, "existing_index_layout_real_device_verified_targets": []string{"windows/amd64"}, "existing_index_row_real_device_verified_targets": []string{},
			"fallback_pipeline_fixture_verified": true, "sensevoice_adapter_separate_repository": true,
			"sensevoice_adapter_windows_amd64_real_voice_verified": true,
		},
		"ocr": map[string]any{
			"preferred_source": "wechat_existing_index", "native_experimental": "installed_wechat_package",
			"external_dependency": false, "repository_bundles_wechat_files": false,
			"wechat_private_ipc_default": false, "network_requested_by_cli": false,
			"existing_index_layout_real_device_verified_targets": []string{"windows/amd64"}, "existing_index_row_real_device_verified_targets": []string{},
			"native_backend_supported_targets": []string{"windows/amd64"}, "native_backend_real_device_verified_targets": []string{"windows/amd64"},
		},
		"official_article": map[string]any{
			"network_default": false, "destination": "mp.weixin.qq.com", "fixture_verified": true, "real_network_verified": false,
		},
		"optional": map[string]any{
			"voice_transcription": map[string]any{"preferred_source": "wechat_existing_index", "fallback_engines": []string{"whisper.cpp", "v-local-cli-sensevoice"}, "local_only": true, "silk_decoder_bundled": true, "user_install_consent": true},
			"ocr":                 map[string]any{"preferred_source": "wechat_existing_index", "native_experimental_targets": []string{"windows/amd64"}, "external_dependency": false, "per_call_private_ipc_consent": true}, "builtin_llm_analysis": false,
		},
	}, nil
}

func writeDiagnosticBundle(output string, force bool, initialized []state.AccountState, providerStatus provider.Status) (map[string]any, error) {
	target, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Lstat(target); statErr == nil && info.IsDir() {
		return nil, &commandError{typeName: "invalid_output", message: "诊断输出路径是目录", code: 2}
	} else if statErr == nil && !force {
		return nil, &commandError{typeName: "output_exists", message: "诊断输出文件已存在", hint: "更换路径，或明确传入 --force 覆盖。", code: 3}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	accounts := make([]map[string]any, 0, len(initialized))
	for _, value := range initialized {
		accounts = append(accounts, map[string]any{
			"account_id": value.AccountID, "generation_id": value.GenerationID,
			"snapshot_manifest_sha256": value.SnapshotManifestSHA256,
			"updated_at":               value.UpdatedAt, "storage": value.Storage,
			"database": value.Database, "media": value.Media,
		})
	}
	bundle := map[string]any{
		"schema_version": 1, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
		"cli":                       map[string]any{"version": Version, "response_schema_version": responseSchemaVersion, "runtime": "go", "os": runtime.GOOS, "arch": runtime.GOARCH},
		"provider":                  publicProviderStatus(providerStatus, false),
		"initialized_account_count": len(accounts), "initialized_accounts": accounts,
		"privacy": map[string]any{"contains_paths": false, "contains_account_names": false, "contains_content": false, "contains_secrets": false, "contains_urls_or_tokens": false},
	}
	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return nil, err
	}
	temporary, err := writeTemporaryFileNear(target, payload)
	if err != nil {
		return nil, err
	}
	if force {
		err = publishFile(temporary, target)
	} else {
		err = publishNewFile(temporary, target)
	}
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return map[string]any{"output": target, "bytes": len(payload), "sha256": hex.EncodeToString(digest[:]), "sanitized": true}, nil
}

func selectLocalAccount(selector string) (localplatform.Account, error) {
	accounts := localplatform.Accounts()
	if len(accounts) == 0 {
		return localplatform.Account{}, &commandError{typeName: "no_accounts", message: "未发现本地微信账号", hint: "请重新登录微信/打开新消息后重试；也可设置 V_LOCAL_CLI_DATA_ROOT 或 V_LOCAL_CLI_ACCOUNT_DIR。", code: 5}
	}
	account, selected, ambiguous := localplatform.Select(accounts, selector)
	if selected {
		return account, nil
	}
	if ambiguous {
		return localplatform.Account{}, &commandError{typeName: "need_account", message: "存在多个匹配账号，需要明确选择", hint: "运行 v-local-cli accounts 查看账号，再传入 --account。", code: 2}
	}
	return localplatform.Account{}, &commandError{typeName: "no_account", message: "没有找到指定账号", hint: "运行 v-local-cli accounts 查看账号列表。", code: 2}
}

func loadCandidateFile(path string) (provider.CandidateBundle, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return provider.CandidateBundle{}, err
	}
	var bundle provider.CandidateBundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		return provider.CandidateBundle{}, errors.New("候选文件不是有效 JSON")
	}
	if err := provider.ValidateBundle(&bundle); err != nil {
		return provider.CandidateBundle{}, err
	}
	return bundle, nil
}

type snapshotPublishOptions struct {
	Storage                   string
	RequireMedia              bool
	PersistSecrets            bool
	PreventCoverageRegression bool
	ProcessAccessPerformed    bool
	CredentialSource          string
}

func publishAccountSnapshot(account localplatform.Account, bundle provider.CandidateBundle, options snapshotPublishOptions) (any, error) {
	media := snapshot.ValidateMedia(account.Path, bundle.ImageKeys)
	if options.RequireMedia && media.Status != "verified" {
		return nil, &commandError{typeName: "media_key_unverified", message: "图片 AES/XOR 候选未通过真实 DAT 样本验真", hint: "请打开一条含图片的新消息后重试，或去掉 --require-media 仅发布数据库快照。", code: 5}
	}
	accountID := state.AccountID(account.Path)
	generationsRoot, err := state.EnsureGenerationsPath(accountID)
	if err != nil {
		return nil, err
	}
	previousState, previousErr := state.Load(accountID)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return nil, &commandError{
			typeName: "state_recovery_failed", message: "现有账号状态无法安全恢复",
			hint: "先运行 v-local-cli doctor；确认不再需要现有快照后，可使用 v-local-cli forget 重建。", code: 5,
		}
	}
	previousSnapshot := ""
	if previousErr == nil {
		previousSnapshot = previousState.SnapshotPath
	}
	report, generation, err := snapshot.BuildGeneration(account.DBDir, generationsRoot, bundle.DatabaseKeys, snapshot.BuildOptions{
		PreventCoverageRegression: options.PreventCoverageRegression,
		PreviousSnapshot:          previousSnapshot, CreatorVersion: Version,
	})
	if err != nil {
		var regression *snapshot.CoverageRegressionError
		if errors.As(err, &regression) {
			return nil, &commandError{
				typeName: "snapshot_coverage_regression", message: "候选快照的数据库覆盖少于当前快照，已保留当前快照",
				hint: "稍后重试 refresh；若源数据库确已删除且确认接受范围缩小，重新 setup 并显式传入 --allow-coverage-regression。", code: 5,
				details: coverageRegressionDetails(report, regression.Comparison),
			}
		}
		return nil, &commandError{typeName: "database_key_rejected", message: "没有可发布的数据库快照", hint: "请重新登录微信/打开新消息后重试；CLI 已保留上一代快照。", code: 5}
	}
	effectiveStorage := options.Storage
	warnings := []string{}
	secretsPersisted := false
	previousSecrets := provider.CandidateBundle{}
	previousSecretsExist := false
	secretsStateKnown := false
	secretsChanged := false
	if options.PersistSecrets {
		previousSecrets, previousSecretsExist, err = state.LoadSecretsOptional(accountID)
		if err == nil {
			secretsStateKnown = true
		} else {
			warnings = append(warnings, "系统凭据库当前不可读；未修改已有凭据")
		}
	}
	if options.PersistSecrets && options.Storage == "keychain" {
		verifiedBundle := provider.CandidateBundle{
			DatabaseKeys: snapshot.VerifiedKeys(bundle.DatabaseKeys, report),
		}
		if media.Status == "verified" {
			verifiedBundle.ImageKeys = bundle.ImageKeys
		}
		if !secretsStateKnown {
			effectiveStorage = "snapshot-only"
		} else if err := state.SaveSecrets(accountID, verifiedBundle); err != nil {
			effectiveStorage = "snapshot-only"
			warnings = append(warnings, "系统凭据库写入失败；已保留可查询快照，但刷新和图片导出需要重新 setup")
		} else {
			secretsPersisted = true
			secretsChanged = true
		}
	}
	if report.Summary.Skipped > 0 && options.CredentialSource == "saved_keychain" {
		warnings = append(warnings, "部分数据库没有已保存的验真密钥；已发布可解密范围，需要完整覆盖时重新 setup")
	}
	accountState := state.AccountState{
		AccountID: accountID, AccountName: account.Name, AccountPath: account.Path,
		SnapshotPath: generation.Path, GenerationID: generation.ID, SnapshotManifestSHA256: generation.ManifestSHA256,
		SnapshotCreatedAt: generation.CreatedAt,
		Storage:           effectiveStorage, Database: report.Summary, Media: media,
	}
	if err := state.Save(&accountState); err != nil {
		if secretsChanged {
			if previousSecretsExist {
				_ = state.SaveSecrets(accountID, previousSecrets)
			} else {
				_ = state.DeleteSecrets(accountID)
			}
		}
		_ = os.RemoveAll(generation.Path)
		return nil, &commandError{
			typeName: "state_commit_failed", message: "快照状态提交失败，已回滚新代际",
			hint: "上一代快照保持不变；运行 v-local-cli doctor 后重试。", code: 5,
		}
	}
	if options.PersistSecrets && options.Storage == "snapshot-only" && secretsStateKnown && previousSecretsExist {
		if err := state.DeleteSecrets(accountID); err != nil {
			warnings = append(warnings, "系统凭据库中的旧密钥未能删除；refresh 已禁用，可用 v-local-cli forget 清除全部本地数据")
		}
	}
	if cleanupErr := snapshot.CleanupGenerations(generationsRoot, generation.Path, previousSnapshot); cleanupErr != nil {
		warnings = append(warnings, "旧快照代际暂未清理；可稍后运行 v-local-cli gc")
	}
	status := "ready"
	if report.Summary.Failed > 0 || report.Summary.Skipped > 0 || media.Status != "verified" || len(warnings) > 0 {
		status = "partial"
	}
	return map[string]any{
		"status": status, "account": publicAccountState(accountState, false), "database": report,
		"media": media, "storage": effectiveStorage, "warnings": warnings,
		"credential_source":        options.CredentialSource,
		"process_access_performed": options.ProcessAccessPerformed,
		"secrets_persisted":        secretsPersisted, "next": "v-local-cli contacts",
	}, nil
}

func coverageRegressionDetails(report snapshot.Report, comparison snapshot.CoverageComparison) map[string]any {
	statuses := make(map[string]snapshot.DatabaseResult, len(report.Results))
	for _, result := range report.Results {
		statuses[strings.ToLower(filepath.ToSlash(result.Database))] = result
	}
	missing := make([]map[string]string, 0, len(comparison.MissingDatabases))
	for _, database := range comparison.MissingDatabases {
		status := "source_missing"
		reason := "source_database_not_discovered"
		if result, found := statuses[database]; found {
			status = result.Status
			switch result.Reason {
			case "no_key":
				reason = "saved_key_missing"
			case "stable_copy_database_failed", "stable_copy_wal_failed", "decrypt_or_wal_validation_failed":
				reason = result.Reason
			default:
				reason = "decryption_or_stable_read_failed"
			}
		}
		missing = append(missing, map[string]string{"database": database, "status": status, "reason": reason})
	}
	return map[string]any{
		"comparison":        comparison,
		"candidate_summary": report.Summary,
		"missing":           missing,
	}
}

func acquireSnapshotTransaction(accountID string) (*state.AccountLock, error) {
	lock, err := state.AcquireAccountLock(accountID)
	if errors.Is(err, state.ErrAccountBusy) {
		return nil, &commandError{
			typeName: "snapshot_busy", message: "该账号已有 setup 或 refresh 正在运行",
			hint: "等待当前快照事务完成后重试；不要并发启动多个刷新。", code: 5,
		}
	}
	if err != nil {
		return nil, err
	}
	return lock, nil
}

func keyProviderCommandError(err error) *commandError {
	var acquisition *provider.AcquisitionError
	if !errors.As(err, &acquisition) {
		return &commandError{
			typeName: "key_provider_failed", message: "密钥提供器未能返回有效候选",
			hint: "请保持微信登录并打开一条新消息后重试。", code: 5,
		}
	}
	switch acquisition.Reason {
	case "wechat_not_running":
		return &commandError{
			typeName: "wechat_not_running", message: "没有发现正在运行的微信主进程",
				hint: "请手动启动并登录微信，然后重新运行同一条 setup 命令。CLI/Provider 不会自动启动微信。", code: 5,
		}
	case "process_access_denied":
		if acquisition.Platform != "darwin" {
			return &commandError{
				typeName: "key_provider_permission_denied", message: "密钥提供器无法读取目标进程",
				hint: "请确认当前用户有权读取微信进程，并重新运行 setup。", code: 5,
			}
		}
		if acquisition.HelperStatus == "not_installed" {
			return &commandError{
				typeName: "key_provider_helper_missing", message: "macOS 未允许直接读取微信，且自动 helper 未安装",
				hint: "运行一次 npx @zanescope/v-local-key-provider@latest install；安装器会同时配置 helper，然后重新运行 setup。", code: 5,
			}
		}
		if acquisition.HelperStatus == "launch_failed" || acquisition.HelperStatus == "response_failed" {
			return &commandError{
				typeName: "key_provider_helper_failed", message: "macOS 自动 helper 未能正常启动",
				hint: "重新运行一次 npx @zanescope/v-local-key-provider@latest install，然后重新运行 setup。", code: 5,
			}
		}
		return &commandError{
			typeName: "key_provider_permission_denied", message: "macOS 未允许已安装的 helper 读取微信进程",
			hint: "保持微信登录；Provider 会自动尝试普通 helper，失败时再请求一次管理员授权，无需手工运行 helper 或处理候选文件。", code: 5,
		}
	case "sip_required":
		return &commandError{
			typeName: "key_provider_sip_required", message: "macOS 的 SIP 仍开启，未能以兼容模式读取微信进程",
			hint: "无 Developer ID 的兼容模式需要你在恢复模式临时关闭 SIP；回到桌面后先运行本次 setup，保持终端窗口运行，看到命令尚未返回提示符时，从“应用程序”打开微信并完成登录。CLI/Provider 不会自动修改 SIP，也不会自动启动、退出或重启微信。如果不希望更改系统安全设置，请改用 --keys FILE 导入已取得的候选。", code: 5,
		}
	case "hook_trigger_required":
		return &commandError{
			typeName: "key_provider_hook_trigger_required", message: "动态捕获已安装，但微信尚未触发数据库调用",
				hint: "当前数据库已经打开，普通切换会话不一定重新触发。请先完全退出微信，再启动下一次 setup；保持终端窗口运行，看到命令尚未返回提示符时，从“应用程序”重新打开微信并完成账号登录。CLI/Provider 不会自动启动或重启微信，也不需要手工运行 helper 或 lldb。", code: 5,
		}
	case "hook_restart_required":
		return &commandError{
			typeName: "key_provider_hook_restart_required", message: "微信需要在动态捕获前重新打开数据库",
				hint: "请先完全退出微信，再启动下一次 setup；保持终端窗口运行，看到命令尚未返回提示符时，从“应用程序”重新打开微信并完成账号登录。CLI/Provider 不会自动启动或重启微信，也不需要手工运行 helper 或 lldb。", code: 5,
		}
	case "deadline_exhausted":
		return &commandError{
			typeName: "key_provider_timeout", message: "密钥候选扫描在时限内未完成",
			hint: "保持微信登录并打开一条新消息后重新运行同一条 setup 命令。", code: 5,
		}
	default:
		return &commandError{
			typeName: "key_provider_no_candidates", message: "没有找到通过本地样本验证的密钥候选",
			hint: "保持微信登录并打开一条新消息后重新运行同一条 setup 命令。", code: 5,
		}
	}
}

func runSetup(args []string) (any, error) {
	set := flag.NewFlagSet("setup", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	dryRun := set.Bool("dry-run", false, "只运行预检")
	accountName := set.String("account", "", "账号目录名或唯一子串")
	providerPath := set.String("provider", "", "显式指定密钥提供器路径")
	allowKeyAccess := set.Bool("allow-key-access", false, "明确授权调用独立密钥提供器")
	keysFile := set.String("keys", "", "读取用户已有的候选 JSON 文件")
	storage := set.String("storage", "keychain", "keychain 或 snapshot-only")
	requireMedia := set.Bool("require-media", false, "要求图片 AES/XOR 候选通过真实样本验真")
	allowCoverageRegression := set.Bool("allow-coverage-regression", false, "明确允许用更少数据库覆盖现有快照")
	showPaths := set.Bool("show-paths", false, "在预检结果中显示本机绝对路径")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：" + setupUsage)
	}
	if *storage != "keychain" && *storage != "snapshot-only" {
		return nil, invalidArguments("--storage 只支持 keychain 或 snapshot-only")
	}
	if *keysFile != "" && *allowKeyAccess {
		return nil, invalidArguments("--keys 与 --allow-key-access 不能同时使用")
	}
	account, err := selectLocalAccount(*accountName)
	if err != nil {
		return nil, err
	}
	if *dryRun {
		return map[string]any{
			"status": "planned", "account": publicLocalAccount(account, *showPaths), "key_provider": publicProviderStatus(provider.Current(*providerPath), *showPaths),
			"key_access_authorized": false, "process_access_performed": false,
			"secrets_persisted": false, "storage": *storage, "paths_included": *showPaths,
			"prevents_coverage_regression": !*allowCoverageRegression,
		}, nil
	}
	lock, err := acquireSnapshotTransaction(state.AccountID(account.Path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	var bundle provider.CandidateBundle
	credentialSource := "provider"
	if *keysFile != "" {
		credentialSource = "candidate_file"
		bundle, err = loadCandidateFile(*keysFile)
		if err != nil {
			return nil, &commandError{typeName: "invalid_key_bundle", message: "候选密钥文件无效", hint: "检查 JSON 格式、数据库候选名称和密钥长度。", code: 3}
		}
	} else {
		if !*allowKeyAccess {
			return nil, &commandError{typeName: "key_access_not_authorized", message: "尚未授权读取本机微信进程中的密钥候选", hint: "确认风险后传入 --allow-key-access，或使用 --keys FILE。", code: 3}
		}
		bundle, err = provider.Acquire(context.Background(), *providerPath, account)
		if errors.Is(err, provider.ErrComponentMissing) {
			return nil, &commandError{
				typeName: "key_acquisition_component_missing",
				message:  "未安装可选的密钥获取组件",
				hint:     "运行 npx @zanescope/v-local-key-provider@latest install；macOS 安装器会同时配置自动 helper。也可用 setup --keys FILE 导入自备候选。",
				code:     4,
			}
		}
		if err != nil {
			return nil, keyProviderCommandError(err)
		}
	}
	return publishAccountSnapshot(account, bundle, snapshotPublishOptions{
		Storage: *storage, RequireMedia: *requireMedia, PersistSecrets: true,
		PreventCoverageRegression: !*allowCoverageRegression,
		ProcessAccessPerformed:    *allowKeyAccess, CredentialSource: credentialSource,
	})
}

func initializedLocalAccount(value state.AccountState) (localplatform.Account, error) {
	accounts := localplatform.Accounts()
	if len(accounts) == 0 {
		return localplatform.Account{}, &commandError{typeName: "no_accounts", message: "未发现本地微信账号", hint: "请重新登录微信/打开新消息后重试；也可设置 V_LOCAL_CLI_DATA_ROOT 或 V_LOCAL_CLI_ACCOUNT_DIR。", code: 5}
	}
	for _, account := range accounts {
		left, leftErr := filepath.Abs(account.Path)
		right, rightErr := filepath.Abs(value.AccountPath)
		if leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) && state.AccountID(account.Path) == value.AccountID {
			return account, nil
		}
	}
	return localplatform.Account{}, &commandError{
		typeName: "refresh_account_unavailable", message: "已初始化账号的本地目录当前不可用",
		hint: "运行 v-local-cli accounts 检查账号目录；请重新登录微信/打开新消息后重试。", code: 5,
	}
}

type storedSecretsLoader func(string) (provider.CandidateBundle, error)

func runRefreshWithSecrets(args []string, loadSecrets storedSecretsLoader) (any, error) {
	set := flag.NewFlagSet("refresh", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	accountName := set.String("account", "", "已初始化账号")
	requireMedia := set.Bool("require-media", false, "要求已保存的图片密钥继续通过真实样本验真")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli refresh [--account NAME] [--require-media]")
	}
	initialized, err := resolveInitializedAccount(*accountName)
	if err != nil {
		return nil, err
	}
	if initialized.Storage != "keychain" {
		return nil, &commandError{
			typeName: "refresh_credentials_unavailable", message: "当前账号没有可用于刷新的已保存验真密钥",
			hint: "使用 --storage keychain 重新完成 setup；只有 setup --allow-key-access 才需要进程访问授权。", code: 5,
		}
	}
	account, err := initializedLocalAccount(initialized)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSnapshotTransaction(initialized.AccountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	bundle, err := loadSecrets(initialized.AccountID)
	if err != nil || provider.ValidateBundle(&bundle) != nil {
		return nil, &commandError{
			typeName: "refresh_credentials_unavailable", message: "无法读取当前账号的已保存验真密钥",
			hint: "请在最初 setup 的同一桌面用户身份下重试，或使用 --storage keychain 重新完成 setup。", code: 5,
		}
	}
	return publishAccountSnapshot(account, bundle, snapshotPublishOptions{
		Storage: "keychain", RequireMedia: *requireMedia,
		PreventCoverageRegression: true,
		CredentialSource:          "saved_keychain", ProcessAccessPerformed: false,
	})
}

func runRefresh(args []string) (any, error) {
	return runRefreshWithSecrets(args, state.LoadSecrets)
}

func runForget(args []string) (any, error) {
	set := flag.NewFlagSet("forget", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	dryRun := set.Bool("dry-run", false, "只显示删除范围")
	confirmed := set.Bool("yes", false, "明确确认删除本地快照、状态和已保存密钥")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli forget --account NAME [--dry-run | --yes]")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	plan := map[string]any{
		"account":     map[string]any{"account_id": value.AccountID, "account_name": value.AccountName},
		"deletes":     []string{"plaintext_snapshots", "account_state", "saved_keychain_secrets", "unfinished_snapshot_stages"},
		"recoverable": false,
	}
	if *dryRun {
		plan["status"] = "planned"
		plan["confirmation_required"] = true
		return plan, nil
	}
	if !*confirmed {
		return nil, &commandError{
			typeName: "confirmation_required", message: "删除本地账号数据需要明确确认",
			hint: "先运行 v-local-cli forget --account NAME --dry-run 查看范围，确认后传入 --yes。", code: 3,
		}
	}
	lock, err := acquireSnapshotTransaction(value.AccountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	if err := state.DeleteSecrets(value.AccountID); err != nil {
		return nil, &commandError{
			typeName: "keychain_delete_failed", message: "无法从系统凭据库删除已保存密钥",
			hint: "未删除快照或状态；检查当前桌面用户的凭据库后重试。", code: 5,
		}
	}
	if err := state.DeleteAccountData(value.AccountID); err != nil {
		return nil, &commandError{
			typeName: "account_data_delete_failed", message: "本地账号数据删除失败",
			hint: "部分数据可能仍在 v-local-cli 私有目录中；运行 doctor 后重试。", code: 5,
		}
	}
	plan["status"] = "deleted"
	plan["confirmation_required"] = false
	return plan, nil
}

func runGC(args []string) (any, error) {
	set := flag.NewFlagSet("gc", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	dryRun := set.Bool("dry-run", false, "只显示将清理的代际")
	if noExtraArguments(set, args) != nil {
		return nil, invalidArguments("用法：v-local-cli gc [--account NAME] [--dry-run]")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	lock, err := acquireSnapshotTransaction(value.AccountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()
	root, err := state.GenerationsPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	report, err := snapshot.GarbageCollect(root, value.SnapshotPath, 1, *dryRun)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"account":                     map[string]any{"account_id": value.AccountID, "account_name": value.AccountName},
		"retained_current_generation": value.GenerationID, "retained_previous_generations": 1,
		"result": report,
	}, nil
}

func resolveInitializedAccount(selector string) (state.AccountState, error) {
	value, err := state.Select(selector)
	if err == nil {
		return value, nil
	}
	if err.Error() == "need_account" {
		return state.AccountState{}, &commandError{typeName: "need_account", message: "存在多个已初始化账号，需要明确选择", hint: "传入 --account NAME。", code: 2}
	}
	return state.AccountState{}, &commandError{typeName: "not_initialized", message: "账号尚未初始化", hint: "先运行 v-local-cli setup --dry-run，再按提示完成 setup。", code: 5}
}

func resolveQueryAccount(selector string, fresh bool) (state.AccountState, error) {
	value, err := resolveInitializedAccount(selector)
	if err != nil || !fresh {
		return value, err
	}
	if _, err := runRefreshWithSecrets([]string{"--account", value.AccountName}, state.LoadSecrets); err != nil {
		return state.AccountState{}, err
	}
	return resolveInitializedAccount(value.AccountID)
}

func runContacts(args []string) (any, error) {
	set := flag.NewFlagSet("contacts", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	limit := set.Int("limit", 100, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) > 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli contacts [--account NAME] [--limit N] [关键词]")
	}
	keyword := ""
	if len(set.Args()) == 1 {
		keyword = set.Args()[0]
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	items, err := store.Contacts(value.SnapshotPath, keyword, *limit)
	if err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{"account": value.AccountName, "items": items, "count": len(items), "query": keyword}, value), nil
}

func runHistory(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("history", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	limit := set.Int("limit", 200, "最多返回条数")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli history [--account NAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <username>")
	}
	window, err := resolveTimeWindow(set.Args()[0], *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	items, err := store.HistoryWindow(value.SnapshotPath, set.Args()[0], window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err == nil {
		err = attachExistingVoiceTranscripts(value, items)
	}
	data := map[string]any{"account": value.AccountName, "chat": set.Args()[0], "items": items, "count": len(items)}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), err
}

func runSearch(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("search", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	chat := set.String("chat", "", "限定会话 username")
	limit := set.Int("limit", 200, "最多返回条数")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 1000 {
		return nil, invalidArguments("用法：v-local-cli search [--chat USERNAME] [--account NAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>")
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
	items, err := store.SearchWindow(value.SnapshotPath, set.Args()[0], *chat, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err == nil {
		err = attachExistingVoiceTranscripts(value, items)
	}
	data := map[string]any{
		"account": value.AccountName, "query": set.Args()[0], "chat": *chat,
		"items": items, "count": len(items),
		"coverage": map[string]any{"source": "local_plaintext_snapshot", "backend": "decoded_scan", "complete": false},
	}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), err
}

func runStats(args []string) (any, error) {
	set := flag.NewFlagSet("stats", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	top := set.Int("top", 20, "群成员排行条数；0 表示全部")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *top < 0 || *top > 5000 {
		return nil, invalidArguments("用法：v-local-cli stats [--account NAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--top N] <username>")
	}
	chat := set.Args()[0]
	window, err := resolveTimeWindow(chat, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	statistics, err := store.Stats(value.SnapshotPath, chat, window.StartTimestamp, window.EndTimestamp, *top)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"account": value.AccountName, "stats": statistics}
	return withGeneration(outputWithTimeWindow(data, window, false), value), nil
}

func runMomentContacts(args []string) (any, error) {
	set := flag.NewFlagSet("moments-contacts", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	limit := set.Int("limit", 100, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) > 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli moments-contacts [--account NAME] [--limit N] [关键词]")
	}
	keyword := ""
	if len(set.Args()) == 1 {
		keyword = set.Args()[0]
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.MomentContacts(value.SnapshotPath, keyword, *limit)
	if err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{"account": value.AccountName, "query": keyword, "result": report}, value), nil
}

func momentMediaOptions(account state.AccountState) store.MomentMediaOptions {
	options := store.MomentMediaOptions{AccountPath: account.AccountPath, SnapshotPath: account.SnapshotPath}
	if bundle, err := state.LoadSecrets(account.AccountID); err == nil && bundle.ImageKeys != nil {
		options.AESKey = bundle.ImageKeys.AES
		options.XORKey = bundle.ImageKeys.XOR
		options.KeysAvailable = true
	}
	return options
}

func attachMomentMedia(report *store.MomentReport, account state.AccountState, requested bool) {
	if !requested {
		return
	}
	options := momentMediaOptions(account)
	resolution := store.ResolveMomentMedia(report.Items, options)
	report.Coverage["media_resolution"] = resolution
	report.Coverage["verified_local_media"] = resolution.VerifiedLocalMedia
}

func runMoments(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("moments", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多返回条数")
	resolveMedia := set.Bool("resolve-media", false, "用强证据解析本地朋友圈及评论媒体")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli moments [--account NAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--resolve-media] <username>")
	}
	username := set.Args()[0]
	window, err := resolveTimeWindow(username, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	window.ChatType = "moment_contact"
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.Moments(value.SnapshotPath, username, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	attachMomentMedia(&report, value, *resolveMedia)
	data := map[string]any{"account": value.AccountName, "contact": username, "items": report.Items, "count": len(report.Items), "coverage": report.Coverage}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}

func runMomentSearch(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("moments-search", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	contact := set.String("contact", "", "限定联系人 username")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多返回条数")
	resolveMedia := set.Bool("resolve-media", false, "用强证据解析命中记录的原帖及评论媒体")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli moments-search [--account NAME] [--contact USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--resolve-media] <关键词>")
	}
	window, err := resolveTimeWindow(*contact, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	if *contact == "" {
		window.ChatType = "moments_cross_contact"
	} else {
		window.ChatType = "moment_contact"
	}
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.SearchMoments(value.SnapshotPath, set.Args()[0], *contact, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	attachMomentMedia(&report, value, *resolveMedia)
	data := map[string]any{
		"account": value.AccountName, "query": set.Args()[0], "contact": *contact,
		"items": report.Items, "count": len(report.Items), "coverage": report.Coverage,
	}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}

func runOfficialAccounts(args []string) (any, error) {
	set := flag.NewFlagSet("official-accounts", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	limit := set.Int("limit", 100, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) > 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli official-accounts [--account NAME] [--limit N] [关键词]")
	}
	keyword := ""
	if len(set.Args()) == 1 {
		keyword = set.Args()[0]
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.OfficialAccounts(value.SnapshotPath, keyword, *limit)
	if err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{"account": value.AccountName, "query": keyword, "result": report}, value), nil
}

func runOfficialHistory(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("official-history", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli official-history [--account NAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <gh_username>")
	}
	publisher := set.Args()[0]
	window, err := resolveTimeWindow(publisher, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	window.ChatType = "official_account"
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.OfficialHistory(value.SnapshotPath, publisher, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"account": value.AccountName, "publisher": publisher, "items": report.Items, "count": len(report.Items), "coverage": report.Coverage}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}

func runOfficialSearch(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("official-search", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	publisher := set.String("publisher", "", "限定公众号 gh_username")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli official-search [--account NAME] [--publisher GH_USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>")
	}
	window, err := resolveTimeWindow(*publisher, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	if *publisher == "" {
		window.ChatType = "official_cross_account"
	} else {
		window.ChatType = "official_account"
	}
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.SearchOfficial(value.SnapshotPath, set.Args()[0], *publisher, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"account": value.AccountName, "query": set.Args()[0], "publisher": *publisher,
		"items": report.Items, "count": len(report.Items), "coverage": report.Coverage,
	}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}

func runExportMedia(args []string) (any, error) {
	set := flag.NewFlagSet("export-media", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	output := set.String("output", "", "输出文件")
	force := set.Bool("force", false, "覆盖已存在的输出文件")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || *output == "" {
		return nil, invalidArguments("用法：v-local-cli export-media --output FILE [--account NAME] [--force] <input.dat>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	target, err := prepareOutputTarget(*output, *force)
	if err != nil {
		return nil, err
	}
	bundle, err := state.LoadSecrets(value.AccountID)
	if err != nil || bundle.ImageKeys == nil {
		return nil, &commandError{typeName: "need_media_keys", message: "系统凭据库中没有已验真的图片密钥", hint: "重新运行 v-local-cli setup --storage keychain；如无图片样本，先在微信打开一条图片消息。", code: 5}
	}
	inputInfo, err := os.Stat(set.Args()[0])
	if err != nil {
		return nil, err
	}
	if inputInfo.Size() > 64*1024*1024 {
		return nil, &commandError{typeName: "media_too_large", message: "输入文件超过 64 MiB 安全上限", code: 3}
	}
	input, err := os.ReadFile(set.Args()[0])
	if err != nil {
		return nil, err
	}
	plain, format, err := cryptoutil.DecryptImageDAT(input, bundle.ImageKeys.AES, bundle.ImageKeys.XOR)
	if err != nil {
		return nil, &commandError{typeName: "media_decrypt_failed", message: "图片解密或容器验真失败", hint: err.Error(), code: 5}
	}
	temporary, err := writeTemporaryFileNear(target, plain)
	if err != nil {
		return nil, err
	}
	if *force {
		err = publishFile(temporary, target)
	} else {
		err = publishNewFile(temporary, target)
	}
	if err != nil {
		if !*force && os.IsExist(err) {
			return nil, outputExistsError()
		}
		return nil, err
	}
	return outputWithGeneration(map[string]any{"input": set.Args()[0], "output": target, "format": format, "bytes": len(plain)}, value), nil
}

func momentMediaCommandError(err error) error {
	var exportErr *store.MomentMediaExportError
	if !errors.As(err, &exportErr) {
		return err
	}
	switch exportErr.Kind {
	case "moment_media_not_found":
		return &commandError{typeName: exportErr.Kind, message: "没有找到对应的朋友圈媒体证据", hint: "重新运行 moments 或 moments-search，使用当前快照返回的 media.evidence_id。", code: 5}
	case "moment_media_identity_conflict", "moment_media_ambiguous":
		return &commandError{typeName: exportErr.Kind, message: "朋友圈媒体证据存在身份冲突或多义性", hint: "刷新本地快照后重新获取 media.evidence_id；CLI 不会在冲突时猜测。", code: 5}
	case "moment_media_kind_unsupported":
		return &commandError{typeName: exportErr.Kind, message: "当前独立导出命令只支持朋友圈图片和视频", hint: "实况照片仍需独立绑定其视频描述符，当前版本不会猜测。", code: 5}
	case "moment_media_network_authorization_required":
		return &commandError{typeName: exportErr.Kind, message: "本地没有可验真的媒体缓存，且本次未授权联网", hint: "确认允许把该媒体记录中的临时令牌发送到其腾讯 CDN 地址后，加 --allow-network 重试。", code: 3}
	case "moment_media_remote_descriptor_missing":
		return &commandError{typeName: exportErr.Kind, message: "本地记录缺少下载或解密该媒体所需的完整描述符", hint: "刷新微信本地数据后重新执行查询；CLI 不会补造令牌或密钥。", code: 5}
	case "moment_media_remote_url_rejected":
		return &commandError{typeName: exportErr.Kind, message: "媒体地址不在受信任的朋友圈 CDN 范围内", hint: "CLI 已拒绝该地址，不会跟随跳转或访问内网地址。", code: 5}
	case "moment_media_download_failed":
		return &commandError{typeName: exportErr.Kind, message: "朋友圈媒体下载失败", hint: "令牌可能已过期；刷新本地快照后重新获取 media.evidence_id 再试。", code: 5}
	case "moment_media_download_failed_authorization_rejected", "moment_media_download_failed_resource_unavailable":
		return &commandError{typeName: exportErr.Kind, message: "CDN 已拒绝该媒体请求或资源已失效", hint: "临时令牌或资源可能已过期；在微信产生新的本地记录后重新执行 setup，再获取新的 media.evidence_id。", code: 5}
	case "moment_media_download_failed_dns_failed", "moment_media_download_failed_connection_failed", "moment_media_download_failed_request_failed":
		return &commandError{typeName: exportErr.Kind, message: "无法建立受限的朋友圈 CDN 连接", hint: "检查当前网络和 DNS 后重试；CLI 不会使用环境代理或跟随跳转。", code: 5}
	case "moment_media_download_failed_direct_dns_failed":
		return &commandError{typeName: exportErr.Kind, message: "系统 DNS 返回 fake-IP，且受限的加密 DNS 回退失败", hint: "请将朋友圈 CDN 域名设为真实 DNS 解析，或允许直连 DNSPod DoT 后重试。", code: 5}
	case "moment_media_download_failed_non_public_address", "moment_media_download_failed_invalid_address", "moment_media_download_failed_request_build_failed":
		return &commandError{typeName: exportErr.Kind, message: "CDN 地址未通过公网连接安全检查", hint: "CLI 已拒绝该连接，不会尝试访问内网或非受信任地址。", code: 5}
	case "moment_media_download_failed_synthetic_proxy_address":
		return &commandError{typeName: exportErr.Kind, message: "DNS 回退仍返回了代理或 TUN 使用的合成地址", hint: "CLI 不会将 CDN token 发送到合成地址。请将受信任的朋友圈 CDN 域名设为真实 DNS 解析后重试。", code: 5}
	case "moment_media_download_failed_redirect_rejected":
		return &commandError{typeName: exportErr.Kind, message: "CDN 返回了不允许跟随的跳转", hint: "CLI 已按安全策略停止；刷新本地快照后使用新的 media.evidence_id 重试。", code: 5}
	case "moment_media_download_failed_rate_limited", "moment_media_download_failed_http_status":
		return &commandError{typeName: exportErr.Kind, message: "CDN 未返回可用的媒体响应", hint: "稍后重试；若仍失败，刷新本地快照后重新获取 media.evidence_id。", code: 5}
	case "moment_media_download_failed_response_read_failed", "moment_media_download_failed_response_size_invalid":
		return &commandError{typeName: exportErr.Kind, message: "CDN 响应未通过读取或大小检查", hint: "没有生成输出文件；稍后重试。", code: 5}
	case "moment_media_verify_failed_container":
		return &commandError{typeName: exportErr.Kind, message: "CDN 数据解密后不是可识别的媒体容器", hint: "没有生成输出文件；需要复核当前微信版本的密钥与字节流契约。", code: 5}
	case "moment_media_verify_failed_payload_size":
		return &commandError{typeName: exportErr.Kind, message: "CDN 媒体响应为空或超过安全上限", hint: "没有生成输出文件。", code: 5}
	case "moment_media_local_unavailable", "moment_media_verify_failed":
		return &commandError{typeName: exportErr.Kind, message: "朋友圈媒体没有通过容器或摘要验真", hint: "没有生成输出文件；刷新本地快照后重试。", code: 5}
	default:
		return &commandError{typeName: "moment_media_export_failed", message: "朋友圈媒体导出失败", code: 5}
	}
}

func publishNewFile(temporary, target string) error {
	if err := os.Link(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	_ = os.Remove(temporary)
	return nil
}

func outputExistsError() error {
	return &commandError{typeName: "output_exists", message: "输出文件已存在", hint: "更换 --output，或明确传入 --force 覆盖。", code: 3}
}

func prepareOutputTarget(output string, force bool) (string, error) {
	target, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(target); statErr == nil && info.IsDir() {
		return "", &commandError{typeName: "invalid_output", message: "输出路径是目录", hint: "--output 必须指向具体文件。", code: 2}
	} else if statErr == nil {
		reparse, reparseErr := outputPathIsReparsePoint(target)
		if reparseErr != nil {
			return "", reparseErr
		}
		if reparse {
			return "", &commandError{typeName: "invalid_output", message: "输出路径是符号链接或重解析点", hint: "--output 必须指向普通文件路径；不要通过链接覆盖目标。", code: 2}
		}
		if !info.Mode().IsRegular() {
			return "", &commandError{typeName: "invalid_output", message: "输出路径不是普通文件", hint: "--output 必须指向普通文件。", code: 2}
		}
		if !force {
			return "", outputExistsError()
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	return target, nil
}

func createTemporaryFileNear(target string) (*os.File, string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), ".v-local-cli-output-*.tmp")
	if err != nil {
		return nil, "", err
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, "", err
	}
	return file, path, nil
}

func writeTemporaryFileNear(target string, payload []byte) (string, error) {
	file, path, err := createTemporaryFileNear(target)
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remove = false
	return path, nil
}

func runExportMomentMedia(args []string) (any, error) {
	set := flag.NewFlagSet("export-moment-media", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	output := set.String("output", "", "输出图片文件")
	allowNetwork := set.Bool("allow-network", false, "明确授权本次访问媒体描述符指向的受限 CDN")
	force := set.Bool("force", false, "覆盖已存在的输出文件")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || strings.TrimSpace(*output) == "" {
		return nil, invalidArguments("用法：v-local-cli export-moment-media --output FILE [--account NAME] [--allow-network] [--force] <media_evidence_id>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	// 与 export、export-media 共用同一套输出校验，其中包含符号链接／重解析点和普通文件判定。
	target, err := prepareOutputTarget(*output, *force)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	temporaryDirectory, err := state.EnsureExportTempPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	artifact, err := store.ExportMomentMedia(ctx, value.SnapshotPath, set.Args()[0], store.MomentMediaExportOptions{
		MomentMediaOptions: momentMediaOptions(value),
		AllowNetwork:       *allowNetwork,
		TemporaryDirectory: temporaryDirectory,
	})
	if err != nil {
		return nil, momentMediaCommandError(err)
	}
	if artifact.RemoveAfterRead && artifact.Path != "" {
		defer os.Remove(artifact.Path)
	}
	file, temporary, err := createTemporaryFileNear(target)
	if err != nil {
		return nil, err
	}
	var source io.Reader = bytes.NewReader(artifact.Data)
	var sourceFile *os.File
	if artifact.Path != "" {
		sourceFile, err = os.Open(artifact.Path)
		if err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return nil, err
		}
		source = sourceFile
	}
	written, writeErr := io.Copy(file, source)
	if sourceFile != nil {
		_ = sourceFile.Close()
	}
	if writeErr == nil && written != int64(artifact.Bytes) {
		writeErr = io.ErrShortWrite
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		if writeErr != nil {
			return nil, writeErr
		}
		return nil, closeErr
	}
	if *force {
		err = publishFile(temporary, target)
	} else {
		err = publishNewFile(temporary, target)
	}
	if err != nil {
		if !*force && os.IsExist(err) {
			return nil, &commandError{typeName: "output_exists", message: "输出文件已存在", hint: "更换 --output，或明确传入 --force 覆盖。", code: 3}
		}
		return nil, err
	}
	return outputWithGeneration(map[string]any{
		"account": value.AccountName, "output": target,
		"evidence_id": artifact.EvidenceID, "media_kind": artifact.Kind, "format": artifact.Format,
		"bytes": artifact.Bytes, "content_md5": artifact.ContentMD5,
		"container_validation": artifact.ContainerValidation, "decryption_scope": artifact.DecryptionScope,
		"descriptor_md5_status": artifact.DescriptorMD5Status, "descriptor_size_status": artifact.DescriptorSizeStatus,
		"source": artifact.Source, "resolution_status": artifact.ResolutionStatus,
		"verified_by": artifact.VerifiedBy, "network_access_performed": artifact.NetworkAccessPerformed,
	}, value), nil
}

func runExport(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("export", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	chat := set.String("chat", "", "搜索时限定会话 username")
	output := set.String("output", "", "输出文件")
	format := set.String("format", "jsonl", "json 或 jsonl")
	limit := set.Int("limit", 1000, "最多导出条数")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	force := set.Bool("force", false, "覆盖已存在的输出文件")
	if err := set.Parse(args); err != nil || len(set.Args()) != 2 || *output == "" || (*format != "json" && *format != "jsonl") || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli export --output FILE [--format json|jsonl] [--account NAME] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--force] <history|search> <值>")
	}
	mode, query := set.Args()[0], set.Args()[1]
	windowChat := *chat
	if mode == "history" {
		windowChat = query
	} else if mode != "search" {
		return nil, invalidArguments("导出类型只支持 history 或 search")
	}
	window, err := resolveTimeWindow(windowChat, *start, *end, *all, time.Now())
	if err != nil {
		return nil, err
	}
	effectiveLimit := effectiveResultLimit(*all, limitExplicit, *limit)
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	target, err := prepareOutputTarget(*output, *force)
	if err != nil {
		return nil, err
	}
	file, temporary, err := createTemporaryFileNear(target)
	if err != nil {
		return nil, err
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	hash := sha256.New()
	counter := &countingWriter{writer: io.MultiWriter(file, hash)}
	count := 0
	if effectiveLimit == 0 {
		temporaryDirectory, tempErr := state.EnsureExportTempPath(value.AccountID)
		if tempErr != nil {
			return nil, tempErr
		}
		count, err = store.StreamExportWindow(counter, temporaryDirectory, value.SnapshotPath, mode, query, *chat, window.StartTimestamp, window.EndTimestamp, *format)
	} else {
		var items []store.Message
		if mode == "history" {
			items, err = store.HistoryWindow(value.SnapshotPath, query, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
		} else {
			items, err = store.SearchWindow(value.SnapshotPath, query, *chat, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
		}
		if err == nil {
			count = len(items)
			encoder := json.NewEncoder(counter)
			encoder.SetEscapeHTML(false)
			if *format == "json" {
				encoder.SetIndent("", "  ")
				err = encoder.Encode(map[string]any{"items": items, "count": count})
			} else {
				for _, item := range items {
					if err = encoder.Encode(item); err != nil {
						break
					}
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if *force {
		err = publishFile(temporary, target)
	} else {
		err = publishNewFile(temporary, target)
	}
	if err != nil {
		if !*force && os.IsExist(err) {
			return nil, outputExistsError()
		}
		return nil, err
	}
	removeTemporary = false
	data := map[string]any{
		"mode": mode, "query": query, "output": target, "format": *format,
		"count": count, "bytes": counter.bytes, "sha256": hex.EncodeToString(hash.Sum(nil)),
		"streamed": effectiveLimit == 0,
	}
	return withGeneration(outputWithQueryMetadata(data, window, false, effectiveLimit, limitExplicit), value), nil
}

func publishFile(temporary, target string) error {
	backup := ""
	movedOld := false
	if _, err := os.Lstat(target); err == nil {
		placeholder, reserveErr := os.CreateTemp(filepath.Dir(target), ".v-local-cli-backup-*.old")
		if reserveErr != nil {
			_ = os.Remove(temporary)
			return reserveErr
		}
		backup = placeholder.Name()
		if closeErr := placeholder.Close(); closeErr != nil {
			_ = os.Remove(backup)
			_ = os.Remove(temporary)
			return closeErr
		}
		if removeErr := os.Remove(backup); removeErr != nil {
			_ = os.Remove(temporary)
			return removeErr
		}
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(temporary)
			return err
		}
		movedOld = true
	}
	if err := os.Rename(temporary, target); err != nil {
		if movedOld {
			_ = os.Rename(backup, target)
		}
		_ = os.Remove(temporary)
		return err
	}
	if movedOld {
		_ = os.Remove(backup)
	}
	return nil
}

func commandSchemas() map[string]any {
	return map[string]any{
		"accounts":          map[string]any{"usage": "v-local-cli accounts [--show-paths]", "read_only": true, "paths_default": "redacted"},
		"status":            map[string]any{"usage": "v-local-cli status [--show-paths]", "read_only": true, "paths_default": "redacted"},
		"provider":          map[string]any{"usage": "v-local-cli provider status [--path FILE] [--show-paths]", "read_only": true, "paths_default": "redacted"},
		"install":           map[string]any{"usage": "v-local-cli install [--dry-run] [--skip-skill] [--show-paths]", "installs_bundled_skill": true, "external_installer": false},
		"doctor":            map[string]any{"usage": "v-local-cli doctor [--provider FILE] [--show-paths] [--bundle FILE] [--force]", "read_only_without_bundle": true, "paths_default": "redacted", "bundle_sanitized": true},
		"capabilities":      map[string]any{"usage": "v-local-cli capabilities", "read_only": true, "separates_build_and_data_layout_validation": true},
		"setup":             map[string]any{"usage": setupUsage, "reads_process_only_with_authorization": true, "account_lock": true, "prevents_coverage_regression": true, "storage_values": []string{"keychain", "snapshot-only"}, "media_validation_optional": true, "paths_default": "redacted"},
		"refresh":           map[string]any{"usage": "v-local-cli refresh [--account NAME] [--require-media]", "reads_saved_keychain": true, "reads_process": false, "network": false, "writes_snapshot": true, "modifies_saved_secrets": false, "account_lock": true, "prevents_coverage_regression": true},
		"forget":            map[string]any{"usage": "v-local-cli forget --account NAME [--dry-run | --yes]", "destructive": true, "requires_confirmation": true},
		"gc":                map[string]any{"usage": "v-local-cli gc [--account NAME] [--dry-run]", "retains_current_and_previous_generation": true},
		"contacts":          map[string]any{"usage": "v-local-cli contacts [--account NAME] [--fresh] [--limit N] [关键词]", "read_only": true, "fresh_snapshot": true},
		"history":           map[string]any{"usage": "v-local-cli history [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <username>", "read_only": true, "fresh_snapshot": true, "default_time_window": "contact_month_or_group_day", "all_without_limit": "unbounded", "structured_non_text": true, "fields": "base_type,sub_type,type_label,details,reply_to,mentions,voice_duration_ms,voice_transcript,sender_username,sender_nickname,sender_group_nickname,sender_identity,is_from_me,media_md5", "red_packet_fields": "receive_status,receive_status_label,receive_status_code,packet_status,message_timestamp,message_time,message_date,receive_timestamp,receive_time,receive_date,receive_time_status,amount,amount_minor_units,amount_currency,amount_status,amount_source,amount_kind"},
		"search":            map[string]any{"usage": "v-local-cli search [--chat USERNAME] [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>", "read_only": true, "fresh_snapshot": true, "default_time_window": "selected_chat_or_cross_chat_day", "all_without_limit": "unbounded", "searches_structured_non_text": true},
		"voice-status":      map[string]any{"usage": "v-local-cli voice-status [--account NAME] [--fresh] [--engine FILE | --asr-provider FILE] [--model PATH] [--show-paths]", "read_only": true, "fresh_snapshot": true, "preferred_source": "wechat_existing_index", "optional_dependencies": []string{"whisper.cpp", "SenseVoice via v-local-cli-asr/1 provider"}, "automatic_download": false},
		"voice-transcribe":  map[string]any{"usage": "v-local-cli voice-transcribe [--account NAME] [--fresh] [--engine FILE | --asr-provider FILE] [--model PATH] [--language zh] [--force] <voice_evidence_id>", "fresh_snapshot": true, "preferred_source": "wechat_existing_index", "writes_private_cache_only_for_fallback": true, "wechat_private_api": false, "network": false, "silk_decoder_bundled": true, "asr_provider_protocol": "v-local-cli-asr/1"},
		"voice-search":      map[string]any{"usage": "v-local-cli voice-search [--account NAME] [--fresh] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--cached-only] [--engine FILE | --asr-provider FILE] [--model PATH] [--language zh] <关键词>", "fresh_snapshot": true, "preferred_source": "wechat_existing_index", "writes_private_cache_unless_cached_only": true, "wechat_private_api": false, "all_without_limit": "unbounded", "network": false, "asr_provider_protocol": "v-local-cli-asr/1"},
		"ocr-status":        map[string]any{"usage": "v-local-cli ocr-status [--account NAME] [--fresh] [--show-paths]", "read_only": true, "fresh_snapshot": true, "source": "wechat_index_probe+v-local-cli_private_cache", "external_dependency": false, "private_ipc": false, "network": false},
		"ocr-file":          map[string]any{"usage": "v-local-cli ocr-file [--allow-private-ipc] <local_image>", "reads_local_image": true, "experimental": true, "external_dependency": false, "uses_installed_wechat_files": true, "bundles_wechat_files": false, "private_ipc_requires_flag": "allow-private-ipc", "network_requested_by_cli": false, "subprocess_sandboxed": false, "vendor_no_sandbox_switch": true},
		"ocr-recognize":     map[string]any{"usage": "v-local-cli ocr-recognize [--account NAME] [--fresh] [--allow-private-ipc] [--force] <image_evidence_id>", "fresh_snapshot": true, "reads_verified_chat_image": true, "writes_private_cache": true, "stores_original_image": false, "external_dependency": false, "private_ipc_requires_flag": "allow-private-ipc", "network_requested_by_cli": false, "subprocess_sandboxed": false, "vendor_no_sandbox_switch": true},
		"ocr-read":          map[string]any{"usage": "v-local-cli ocr-read [--account NAME] [--fresh] <image_evidence_id>", "read_only": true, "fresh_snapshot": true, "source": "wechat_index_probe+v-local-cli_private_cache", "generates_new_results": false, "private_ipc": false, "network": false},
		"ocr-search":        map[string]any{"usage": "v-local-cli ocr-search [--account NAME] [--fresh] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>", "read_only": true, "fresh_snapshot": true, "source": "wechat_index_probe+v-local-cli_private_cache", "all_without_limit": "unbounded", "private_ipc": false, "network": false},
		"stats":             map[string]any{"usage": "v-local-cli stats [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--top N] <username>", "read_only": true, "fresh_snapshot": true, "loads_message_content": false},
		"moments-contacts":  map[string]any{"usage": "v-local-cli moments-contacts [--account NAME] [--fresh] [--limit N] [关键词]", "read_only": true, "fresh_snapshot": true, "scope": "locally_retained_only"},
		"moments":           map[string]any{"usage": "v-local-cli moments [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--resolve-media] <username>", "read_only": true, "fresh_snapshot": true, "all_without_limit": "unbounded", "remote_fetch": false, "includes": "post,visible_likes,visible_comments,replies,comment_media", "interaction_scope": "locally_retained_visible_only", "complete_interaction_history": false},
		"moments-search":    map[string]any{"usage": "v-local-cli moments-search [--account NAME] [--fresh] [--contact USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--resolve-media] <关键词>", "read_only": true, "fresh_snapshot": true, "all_without_limit": "unbounded", "remote_fetch": false, "searches_interactions": true, "interaction_scope": "locally_retained_visible_only", "complete_interaction_history": false},
		"official-accounts": map[string]any{"usage": "v-local-cli official-accounts [--account NAME] [--fresh] [--limit N] [关键词]", "read_only": true, "fresh_snapshot": true, "scope": "locally_known_only"},
		"official-history":  map[string]any{"usage": "v-local-cli official-history [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <gh_username>", "read_only": true, "fresh_snapshot": true, "all_without_limit": "unbounded", "content_level": "card_metadata", "remote_fetch": false},
		"official-search":   map[string]any{"usage": "v-local-cli official-search [--account NAME] [--fresh] [--publisher GH_USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>", "read_only": true, "fresh_snapshot": true, "all_without_limit": "unbounded", "content_level": "card_metadata", "remote_fetch": false},
		"official-article":  map[string]any{"usage": "v-local-cli official-article [--account NAME] [--fresh] [--allow-network] <publication_evidence_id>", "read_only": true, "fresh_snapshot": true, "network_default": false, "network_requires_flag": "allow-network", "destination": "mp.weixin.qq.com", "redirects": false, "cookies": false, "tun_fake_ip_dns_fallback": false, "content_level": "remote_article_plain_text"},
		"export":            map[string]any{"usage": "v-local-cli export --output FILE [--format json|jsonl] [--account NAME] [--fresh] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] [--force] <history|search> <值>", "fresh_snapshot": true, "writes_output": true, "output_exists_default": "reject", "all_without_limit": "unbounded"},
		"export-media":      map[string]any{"usage": "v-local-cli export-media --output FILE [--account NAME] [--fresh] [--force] <input.dat>", "fresh_snapshot": true, "writes_output": true, "output_exists_default": "reject"},
		"export-moment-media": map[string]any{
			"usage":                 "v-local-cli export-moment-media --output FILE [--account NAME] [--fresh] [--allow-network] [--force] <media_evidence_id>",
			"fresh_snapshot":        true,
			"writes_output":         true,
			"local_first":           true,
			"media_kinds":           []string{"image", "video"},
			"network_default":       false,
			"network_requires_flag": "allow-network",
			"redirects":             false,
			"container_validation":  "strict",
			"remote_formats":        []string{"jpg", "png", "gif", "mp4"},
			"remote_webp_wxgf":      false,
			"decryption_scopes":     []string{"not_required", "full_payload", "prefix_131072", "local_cache"},
			"max_image_bytes":       67108864,
			"max_video_bytes":       536870912,
		},
		"schema": map[string]any{"usage": "v-local-cli schema [command]", "read_only": true, "response_schema_version": responseSchemaVersion},
	}
}

func runSchema(args []string) (any, error) {
	if len(args) > 1 {
		return nil, invalidArguments("用法：v-local-cli schema [command]")
	}
	commands := commandSchemas()
	if len(args) == 1 {
		value, found := commands[args[0]]
		if !found {
			return nil, &commandError{typeName: "unknown_command", message: "schema 中没有该命令", code: 2}
		}
		return map[string]any{"contract_version": responseSchemaVersion, "command": args[0], "schema": value}, nil
	}
	return map[string]any{"contract_version": responseSchemaVersion, "commands": commands}, nil
}

func invalidArguments(hint string) *commandError {
	return &commandError{typeName: "invalid_arguments", message: "命令参数无效", hint: hint, code: 2}
}

func writeHelp(writer io.Writer) {
	fmt.Fprintln(writer, "v-local-cli：纯 Go 的本地微信只读查询工具")
	fmt.Fprintln(writer, "")
	order := []string{
		"accounts", "status", "provider", "install", "doctor", "capabilities", "setup", "refresh", "forget", "gc",
		"contacts", "history", "search", "voice-status", "voice-transcribe", "voice-search", "ocr-status", "ocr-file",
		"ocr-recognize", "ocr-read", "ocr-search", "stats", "moments-contacts", "moments", "moments-search",
		"official-accounts", "official-history", "official-search", "official-article", "export", "export-media",
		"export-moment-media", "schema",
	}
	commands := commandSchemas()
	for _, name := range order {
		definition := commands[name].(map[string]any)
		fmt.Fprintf(writer, "  %s\n", definition["usage"].(string))
	}
	fmt.Fprintln(writer, "  v-local-cli --version")
}

func writeJSON(writer io.Writer, value envelope) {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(writer io.Writer, err *commandError) {
	writeJSON(writer, envelope{SchemaVersion: responseSchemaVersion, OK: false, Error: &errorValue{Type: err.typeName, Message: err.message, Hint: err.hint, Details: err.details}, Meta: map[string]any{
		"version": Version, "runtime": "go", "snapshot_created_at": nil, "snapshot_age_seconds": nil,
	}})
}
