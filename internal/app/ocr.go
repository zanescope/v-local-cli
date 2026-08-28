package app

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
	"github.com/zanescope/v-local-cli/internal/nativeocr"
	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
)

var currentNativeOCR = nativeocr.Current
var recognizeNativeOCR = nativeocr.Recognize

func recognizeTemporaryChatImage(ctx context.Context, directory, format string, payload []byte) (result nativeocr.Result, invoked bool, operationErr, cleanupErr error) {
	temporary, err := os.CreateTemp(directory, "v-local-cli-chat-ocr-*."+format)
	if err != nil {
		return result, false, err, nil
	}
	temporaryPath := temporary.Name()
	defer func() {
		cleanupErr = removeTemporaryFiles(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return result, false, err, nil
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return result, false, err, nil
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return result, false, err, nil
	}
	if err := temporary.Close(); err != nil {
		return result, false, err, nil
	}
	result, operationErr = recognizeNativeOCR(ctx, temporaryPath)
	return result, true, operationErr, nil
}

func runOCRStatus(args []string) (any, error) {
	set := flag.NewFlagSet("ocr-status", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	showPaths := set.Bool("show-paths", false, "显示微信安装路径")
	if err := noExtraArguments(set, args); err != nil {
		return nil, invalidArguments("用法：v-local-cli ocr-status [--account NAME] [--show-paths]")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	status, err := store.WeChatTextIndexStatus(value.SnapshotPath)
	if err != nil {
		return nil, err
	}
	native := currentNativeOCR(*showPaths)
	cachePath, err := state.OCRTextPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	cacheRows, err := store.OCRTextCount(cachePath)
	if err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{
		"account": value.AccountName, "backend": "wechat_index_probe+v-local-cli_private_cache",
		"cached_text_present": cacheRows > 0 || status.ImageIndexedRows > 0,
		"private_cache_rows":  cacheRows, "wechat_index_probe_rows": status.ImageIndexedRows,
		"wechat_index_tables": status.ImageIndexTables, "external_dependency": false,
		"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
		"can_generate_new_results": native.Available, "native_experimental": native,
	}, value), nil
}

func ocrTextFromWeChat(message store.Message, indexed store.WeChatIndexedText) store.OCRText {
	return store.OCRText{
		EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID,
		ServerID: message.ServerID, Timestamp: message.Timestamp, SortKey: message.SortKey,
		Text: indexed.Text, Engine: "wechat-existing-index", Source: "wechat_existing_index",
	}
}

func loadValidatedOCRCache(value state.AccountState, message store.Message, cachePath string, verifyDigest bool) (store.OCRText, bool, string, error) {
	item, found, err := store.LoadOCRText(cachePath, message.EvidenceID)
	if err != nil || !found {
		return item, found, "miss", err
	}
	if item.Chat != message.Chat || item.LocalID != message.LocalID || item.ServerID != message.ServerID || item.Timestamp != message.Timestamp {
		if err := store.DeleteOCRText(cachePath, message.EvidenceID); err != nil {
			return store.OCRText{}, false, "metadata_mismatch", err
		}
		return store.OCRText{}, false, "metadata_mismatch_removed", nil
	}
	if !verifyDigest {
		return item, true, "metadata_verified", nil
	}
	bundle, _, err := state.LoadSecretsOptional(value.AccountID)
	if err != nil {
		return store.OCRText{}, false, "digest_unavailable", err
	}
	aesKey, xorKey := "", 0
	if bundle.ImageKeys != nil {
		aesKey, xorKey = bundle.ImageKeys.AES, bundle.ImageKeys.XOR
	}
	image, err := store.ResolveChatImage(value.SnapshotPath, value.AccountPath, message.EvidenceID, aesKey, xorKey)
	if err != nil {
		return item, true, "digest_unavailable", nil
	}
	if !strings.EqualFold(image.SHA256, item.ImageSHA256) {
		if err := store.DeleteOCRText(cachePath, message.EvidenceID); err != nil {
			return store.OCRText{}, false, "digest_mismatch", err
		}
		return store.OCRText{}, false, "digest_mismatch_removed", nil
	}
	return item, true, "digest_verified", nil
}

func runOCRRecognize(args []string) (any, error) {
	set := flag.NewFlagSet("ocr-recognize", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	allowPrivateIPC := set.Bool("allow-private-ipc", false, "允许启动已安装微信的私有 OCR 组件")
	force := set.Bool("force", false, "忽略已有文字并重新识别")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli ocr-recognize [--account NAME] [--allow-private-ipc] [--force] <image_evidence_id>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	message, err := store.FindImageMessage(value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, &commandError{typeName: "image_evidence_unavailable", message: err.Error(), hint: "先用 history 取得 kind=image 的 evidence_id。", code: 5}
	}
	cachePath, err := state.OCRTextPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	if !*force {
		if indexed, indexErr := store.WeChatOCRTexts(value.SnapshotPath, []store.Message{message}); indexErr != nil {
			return nil, indexErr
		} else if existing, found := indexed[message.EvidenceID]; found {
			return outputWithGeneration(map[string]any{
				"account": value.AccountName, "item": ocrTextFromWeChat(message, existing),
				"cache_status": "not_written", "source": "wechat_existing_index",
				"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
			}, value), nil
		}
		if cached, found, _, loadErr := loadValidatedOCRCache(value, message, cachePath, true); loadErr != nil {
			return nil, loadErr
		} else if found {
			return outputWithGeneration(map[string]any{
				"account": value.AccountName, "item": cached, "cache_status": "hit",
				"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
			}, value), nil
		}
	}
	status := currentNativeOCR(false)
	if !status.Available {
		return nil, &commandError{typeName: "wechat_native_ocr_unavailable", message: "当前环境没有兼容的微信原生 OCR 组件", hint: status.Reason, code: 5}
	}
	if !*allowPrivateIPC {
		return nil, &commandError{
			typeName: "wechat_native_ocr_authorization_required", message: "识别这条聊天图片需要启动已安装微信的私有 OCR 子进程",
			hint: "用户明确同意本次处理这一个 image_evidence_id 后，增加 --allow-private-ipc；授权不会扩展到其他图片。",
			details: map[string]any{
				"evidence_id": message.EvidenceID, "source": "verified_local_chat_image",
				"wechat_version": status.WeChatVersion, "external_dependency": false,
				"private_ipc_invoked": false, "network_requested_by_cli": false,
				"subprocess_sandboxed": false, "vendor_no_sandbox_switch": true,
			}, code: 3,
		}
	}
	bundle, _, err := state.LoadSecretsOptional(value.AccountID)
	if err != nil {
		return nil, err
	}
	aesKey, xorKey := "", 0
	if bundle.ImageKeys != nil {
		aesKey, xorKey = bundle.ImageKeys.AES, bundle.ImageKeys.XOR
	}
	image, err := store.ResolveChatImage(value.SnapshotPath, value.AccountPath, message.EvidenceID, aesKey, xorKey)
	if err != nil {
		return nil, &commandError{
			typeName: "chat_image_unavailable", message: "无法从本地资源中验真这条聊天图片",
			hint:    "先在微信中打开该图片，运行 refresh 后重试；加密 DAT 还需要 setup 保存已验真的图片密钥。",
			details: map[string]any{"reason": err.Error(), "private_ipc_invoked": false, "network_performed": false}, code: 5,
		}
	}
	temporaryDirectory, err := state.EnsureExportTempPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	result, invoked, recognizeErr, removeErr := recognizeTemporaryChatImage(ctx, temporaryDirectory, image.Format, image.Data)
	cancel()
	if recognizeErr != nil {
		if !invoked && removeErr == nil {
			return nil, recognizeErr
		}
		return nil, &commandError{
			typeName: "wechat_native_ocr_failed", message: "微信原生 OCR 实验后端执行失败",
			hint:    "微信升级后私有协议可能变化；可运行 ocr-status 检查当前版本。",
			details: map[string]any{"wechat_version": status.WeChatVersion, "temporary_image_removed": removeErr == nil, "network_requested_by_cli": false}, code: 5,
		}
	}
	if removeErr != nil {
		return nil, &commandError{typeName: "ocr_temporary_cleanup_failed", message: "OCR 完成，但临时明文图片清理失败", hint: "先不要继续处理其他图片；运行 doctor 检查私有临时目录。", code: 5}
	}
	item := store.OCRText{
		EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID,
		ServerID: message.ServerID, Timestamp: message.Timestamp, SortKey: message.SortKey,
		Text: result.Text, ImageSHA256: image.SHA256, Engine: "wechat-native-ocr",
		EngineVersion: status.WeChatVersion, Source: "v-local-cli_private_cache",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.SaveOCRText(cachePath, item); err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{
		"account": value.AccountName, "item": item, "cache_status": "written",
		"image":       map[string]any{"format": image.Format, "bytes": image.Bytes, "verified_by": image.VerifiedBy},
		"recognition": result, "temporary_image_removed": true, "external_dependency": false,
		"network_performed": false, "subprocess_sandboxed": false, "vendor_no_sandbox_switch": true,
	}, value), nil
}

func runOCRFile(args []string) (any, error) {
	set := flag.NewFlagSet("ocr-file", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	allowPrivateIPC := set.Bool("allow-private-ipc", false, "允许启动已安装微信的私有 OCR 组件")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli ocr-file [--allow-private-ipc] <local_image>")
	}
	absolute, err := filepath.Abs(set.Args()[0])
	if err != nil {
		return nil, invalidArguments("本地图片路径无效")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 64*1024*1024 {
		return nil, &commandError{typeName: "ocr_input_invalid", message: "OCR 输入必须是 64 MiB 内的普通本地图片文件", code: 3}
	}
	payload, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	validation, validationErr := cryptoutil.ValidateImageStructure(payload)
	if validationErr != nil || (validation.Format != "jpg" && validation.Format != "png" && validation.Format != "gif") {
		return nil, &commandError{typeName: "ocr_input_invalid", message: "OCR 输入只接受验真的 JPEG、PNG 或 GIF", code: 3}
	}
	format := validation.Format
	status := currentNativeOCR(false)
	if !status.Available {
		return nil, &commandError{typeName: "wechat_native_ocr_unavailable", message: "当前环境没有兼容的微信原生 OCR 组件", hint: status.Reason, code: 5}
	}
	if !*allowPrivateIPC {
		return nil, &commandError{
			typeName: "wechat_native_ocr_authorization_required", message: "生成新的 OCR 需要启动已安装微信的私有 OCR 子进程",
			hint: "确认接受当前微信版本耦合后，增加 --allow-private-ipc；也可只用 ocr-read/ocr-search 读取微信已有索引。",
			details: map[string]any{
				"source": "installed_wechat_package", "wechat_version": status.WeChatVersion,
				"input": "one_local_image", "external_dependency": false, "repository_bundles_wechat_files": false,
				"private_ipc_invoked": false, "network_requested_by_cli": false,
				"subprocess_sandboxed": false, "vendor_no_sandbox_switch": true,
			}, code: 3,
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := recognizeNativeOCR(ctx, absolute)
	if err != nil {
		return nil, &commandError{
			typeName: "wechat_native_ocr_failed", message: "微信原生 OCR 实验后端执行失败",
			hint:    "微信升级后私有协议可能变化；继续使用已有索引，或等待适配当前版本。",
			details: map[string]any{"wechat_version": status.WeChatVersion, "private_ipc_invoked": true, "network_requested_by_cli": false}, code: 5,
		}
	}
	return map[string]any{
		"item": result, "format": format, "container_validation": validation.Method,
		"source_bytes": info.Size(), "input_path_included": false,
		"external_dependency": false, "repository_bundles_wechat_files": false,
		"subprocess_sandboxed": false, "vendor_no_sandbox_switch": true,
	}, nil
}

func runOCRRead(args []string) (any, error) {
	set := flag.NewFlagSet("ocr-read", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli ocr-read [--account NAME] <image_evidence_id>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	message, err := store.FindImageMessage(value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, &commandError{typeName: "image_evidence_unavailable", message: err.Error(), hint: "先用 history 取得 kind=image 的 evidence_id。", code: 5}
	}
	indexed, err := store.WeChatOCRTexts(value.SnapshotPath, []store.Message{message})
	if err != nil {
		return nil, err
	}
	if item, found := indexed[message.EvidenceID]; found {
		return outputWithGeneration(map[string]any{
			"account": value.AccountName, "item": ocrTextFromWeChat(message, item), "backend": "wechat_existing_index",
			"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
		}, value), nil
	}
	cachePath, err := state.OCRTextPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	if item, found, validation, loadErr := loadValidatedOCRCache(value, message, cachePath, true); loadErr != nil {
		return nil, loadErr
	} else if found {
		return outputWithGeneration(map[string]any{
			"account": value.AccountName, "item": item, "backend": "v-local-cli_private_cache", "cache_validation": validation,
			"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
		}, value), nil
	} else {
		return nil, &commandError{
			typeName: "ocr_text_not_cached", message: "微信兼容索引和 v-local-cli 私有缓存中都没有这张图片的文字",
			hint:    "先运行 ocr-recognize；获得本次私有 IPC 明确授权后，对同一 evidence_id 增加 --allow-private-ipc。",
			details: map[string]any{"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false}, code: 5,
		}
	}
}

func runOCRSearch(args []string) (any, error) {
	limitExplicit := flagProvided(args, "limit")
	set := flag.NewFlagSet("ocr-search", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	chat := set.String("chat", "", "限定会话 username")
	start := set.String("start", "", "开始日期 YYYY-MM-DD")
	end := set.String("end", "", "结束日期 YYYY-MM-DD")
	all := set.Bool("all", false, "取消默认日期范围")
	limit := set.Int("limit", 200, "最多扫描和返回的图片条数")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || strings.TrimSpace(set.Args()[0]) == "" || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli ocr-search [--account NAME] [--chat USERNAME] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--all] [--limit N] <关键词>")
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
	keyword := strings.ToLower(strings.TrimSpace(set.Args()[0]))
	cachePath, err := state.OCRTextPath(value.AccountID)
	if err != nil {
		return nil, err
	}
	cached, err := store.SearchOCRTexts(cachePath, keyword, *chat, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
	if err != nil {
		return nil, err
	}
	items := []store.OCRText{}
	staleRemoved := 0
	for _, cachedItem := range cached {
		message, findErr := store.FindImageMessage(value.SnapshotPath, cachedItem.EvidenceID)
		if findErr != nil {
			if err := store.DeleteOCRText(cachePath, cachedItem.EvidenceID); err != nil {
				return nil, err
			}
			staleRemoved++
			continue
		}
		validated, found, _, validateErr := loadValidatedOCRCache(value, message, cachePath, false)
		if validateErr != nil {
			return nil, validateErr
		}
		if !found {
			staleRemoved++
			continue
		}
		items = append(items, validated)
		if effectiveLimit > 0 && len(items) >= effectiveLimit {
			break
		}
	}
	status, err := store.WeChatTextIndexStatus(value.SnapshotPath)
	if err != nil {
		return nil, err
	}
	wechatHits, wechatCandidates := 0, 0
	if status.ImageIndexedRows > 0 && (effectiveLimit == 0 || len(items) < effectiveLimit) {
		messages, imageErr := store.ImageMessages(value.SnapshotPath, *chat, window.StartTimestamp, window.EndTimestamp, effectiveLimit)
		if imageErr != nil {
			return nil, imageErr
		}
		wechatCandidates = len(messages)
		indexed, indexErr := store.WeChatOCRTexts(value.SnapshotPath, messages)
		if indexErr != nil {
			return nil, indexErr
		}
		seen := map[string]bool{}
		for _, item := range items {
			seen[item.EvidenceID] = true
		}
		for _, message := range messages {
			item, found := indexed[message.EvidenceID]
			if found && !seen[message.EvidenceID] && strings.Contains(strings.ToLower(item.Text), keyword) {
				items = append(items, ocrTextFromWeChat(message, item))
				wechatHits++
			}
			if effectiveLimit > 0 && len(items) >= effectiveLimit {
				break
			}
		}
	}
	data := map[string]any{
		"account": value.AccountName, "query": set.Args()[0], "chat": *chat, "items": items, "count": len(items),
		"ocr_source_coverage": map[string]any{
			"scope": "private_cache_first+wechat_index_probe", "private_cache_candidates": len(cached),
			"private_cache_hits": len(items) - wechatHits, "stale_cache_removed": staleRemoved,
			"wechat_probe_rows": status.ImageIndexedRows, "wechat_candidate_images_scanned": wechatCandidates,
			"wechat_hits":             wechatHits,
			"candidate_limit_applied": effectiveLimit > 0, "complete": false,
			"engine_invoked": false, "private_ipc_invoked": false, "network_performed": false,
		},
	}
	return withGeneration(outputWithQueryMetadata(data, window, true, effectiveLimit, limitExplicit), value), nil
}
