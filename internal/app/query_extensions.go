package app

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zanescope/v-local-cli/internal/inbox"
	"github.com/zanescope/v-local-cli/internal/messageindex"
	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
)

func privateStateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if root, rootErr := state.Home(); rootErr == nil && root != "" {
		clean := filepath.Clean(root)
		message = strings.ReplaceAll(message, clean, "[private_state]")
		message = strings.ReplaceAll(message, filepath.ToSlash(clean), "[private_state]")
	}
	return message
}

func stableContactIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "wxid_") || strings.HasPrefix(value, "gh_") ||
		strings.Contains(value, "@") || strings.HasPrefix(value, "filehelper")
}

func resolvedContact(root, input string) (store.ContactMatch, error) {
	match, err := store.ResolveContact(root, input)
	if err == nil {
		return match, nil
	}
	var ambiguous *store.AmbiguousContactError
	if errors.As(err, &ambiguous) {
		return store.ContactMatch{}, &commandError{
			typeName: "ambiguous_contact", message: "联系人名称存在歧义",
			hint: "从 candidates 中选择稳定 username 后重试。", details: ambiguous, code: 2,
		}
	}
	if errors.Is(err, store.ErrContactNotFound) && (stableContactIdentifier(input) || store.ChatExists(root, input)) {
		return store.ContactMatch{Contact: store.Contact{Username: input, Display: input}}, nil
	}
	if errors.Is(err, store.ErrContactNotFound) {
		return store.ContactMatch{}, &commandError{
			typeName: "contact_not_found", message: "没有找到联系人",
			hint: "运行 contacts 或 resolve-contact 查看可用 username。", details: map[string]any{"input": input}, code: 2,
		}
	}
	return store.ContactMatch{}, err
}

func runResolveContact(args []string) (any, error) {
	set := flag.NewFlagSet("resolve-contact", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli resolve-contact [--account NAME] [--fresh] <名称或username>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	match, err := resolvedContact(value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, err
	}
	return outputWithGeneration(map[string]any{
		"account": value.AccountName, "input": set.Args()[0], "match": match,
	}, value), nil
}

func runSessions(args []string, unreadOnly bool) (any, error) {
	command := "sessions"
	if unreadOnly {
		command = "unread"
	}
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	kind := set.String("kind", "", "person、group 或 official")
	limit := set.Int("limit", 100, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 || *limit < 1 || *limit > 5000 ||
		(*kind != "" && *kind != "person" && *kind != "group" && *kind != "official") {
		return nil, invalidArguments("用法：v-local-cli " + command + " [--account NAME] [--fresh] [--kind person|group|official] [--limit N]")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.Sessions(value.SnapshotPath, unreadOnly, *kind, *limit)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"account": value.AccountName, "items": report.Items, "count": len(report.Items),
		"coverage": report.Coverage, "unread_only": unreadOnly,
	}
	return withGeneration(commandOutput{data: data, meta: map[string]any{"untrusted": true}}, value), nil
}

func runMembers(args []string) (any, error) {
	set := flag.NewFlagSet("members", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli members [--account NAME] [--fresh] <群username或名称>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	match, err := resolvedContact(value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(match.Contact.Username, "@chatroom") {
		return nil, &commandError{typeName: "not_group_chat", message: "所选联系人不是群聊", hint: "传入群聊 username 或无歧义的群名称。", code: 2}
	}
	report, err := store.Members(value.SnapshotPath, match.Contact.Username)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"account": value.AccountName, "chat": match.Contact.Username, "display": match.Contact.Display,
		"items": report.Items, "count": len(report.Items), "coverage": report.Coverage,
	}
	return withGeneration(commandOutput{data: data, meta: map[string]any{"untrusted": true}}, value), nil
}

func runFavorites(args []string) (any, error) {
	set := flag.NewFlagSet("favorites", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	kind := set.String("kind", "", "收藏类型")
	limit := set.Int("limit", 100, "最多返回条数")
	if err := set.Parse(args); err != nil || len(set.Args()) > 1 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli favorites [--account NAME] [--fresh] [--kind text|image|voice|video|article|location|mini_program|chat_record|contact_card|other] [--limit N] [关键词]")
	}
	allowedKinds := map[string]bool{"": true, "text": true, "image": true, "voice": true, "video": true, "article": true, "location": true, "mini_program": true, "chat_record": true, "contact_card": true, "other": true}
	if !allowedKinds[*kind] {
		return nil, invalidArguments("--kind 值无效")
	}
	keyword := ""
	if len(set.Args()) == 1 {
		keyword = set.Args()[0]
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	report, err := store.Favorites(value.SnapshotPath, keyword, *kind, *limit)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"account": value.AccountName, "query": keyword, "kind": *kind,
		"items": report.Items, "count": len(report.Items), "coverage": report.Coverage,
	}
	return withGeneration(commandOutput{data: data, meta: map[string]any{"untrusted": true}}, value), nil
}

func runIndex(args []string) (any, error) {
	set := flag.NewFlagSet("index", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	force := set.Bool("force", false, "重建当前 generation 索引")
	showPaths := set.Bool("show-paths", false, "显示私有索引路径")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 || (set.Args()[0] != "status" && set.Args()[0] != "build") {
		return nil, invalidArguments("用法：v-local-cli index [--account NAME] [--force] [--show-paths] <status|build>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	action := set.Args()[0]
	if action == "status" {
		status, err := messageindex.Inspect(value)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"account": value.AccountName, "status": status}
		if *showPaths {
			path, _ := messageindex.DatabasePath(value.AccountID, value.GenerationID)
			result["path"] = path
		}
		return outputWithGeneration(result, value), nil
	}
	lock, err := acquireSnapshotTransaction(value.AccountID)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	value, err = resolveInitializedAccount(value.AccountID)
	if err != nil {
		return nil, err
	}
	report, err := messageindex.Build(value, *force)
	if err != nil {
		return nil, &commandError{typeName: "index_build_failed", message: "generation 消息索引构建失败", hint: privateStateError(err), code: 5}
	}
	if !*showPaths {
		report.Path = ""
	}
	return outputWithGeneration(map[string]any{"account": value.AccountName, "result": report}, value), nil
}

func ensureCurrentIndex(value state.AccountState) error {
	status, err := messageindex.Inspect(value)
	if err == nil && status.Valid && status.Manifest != nil && status.Manifest.Coverage.Complete {
		return nil
	}
	lock, err := acquireSnapshotTransaction(value.AccountID)
	if err != nil {
		return err
	}
	defer lock.Release()
	current, err := resolveInitializedAccount(value.AccountID)
	if err != nil {
		return err
	}
	report, err := messageindex.Build(current, false)
	if err != nil {
		return err
	}
	if !report.Manifest.Coverage.Complete {
		return errors.New("消息索引 coverage 不完整，拒绝推进精确增量游标")
	}
	return nil
}

func runNewMessages(args []string) (any, error) {
	set := flag.NewFlagSet("new-messages", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	consumer := set.String("consumer", "default", "独立消费游标名称")
	start := set.String("start", "now", "新 consumer 从 now 或 beginning 开始")
	limit := set.Int("limit", 200, "单批最多消息数")
	ack := set.String("ack", "", "确认已成功处理的 batch_id")
	statusOnly := set.Bool("status", false, "只查看 consumer 游标")
	deleteConsumer := set.Bool("delete", false, "删除 consumer 游标")
	yes := set.Bool("yes", false, "确认删除 consumer 游标")
	if err := set.Parse(args); err != nil || len(set.Args()) != 0 || *limit < 1 || *limit > 5000 {
		return nil, invalidArguments("用法：v-local-cli new-messages [--account NAME] [--fresh] [--consumer NAME] [--start now|beginning] [--limit N] [--ack BATCH_ID | --status | --delete --yes]")
	}
	modes := 0
	if *ack != "" {
		modes++
	}
	if *statusOnly {
		modes++
	}
	if *deleteConsumer {
		modes++
	}
	if modes > 1 || (*deleteConsumer && !*yes) || (*yes && !*deleteConsumer) {
		return nil, invalidArguments("--ack、--status、--delete 互斥；删除必须同时传入 --delete --yes")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	if *deleteConsumer {
		if err := inbox.Delete(value.AccountID, *consumer); err != nil {
			return nil, &commandError{typeName: "cursor_delete_failed", message: "增量 consumer 删除失败", hint: privateStateError(err), code: 5}
		}
		return outputWithGeneration(map[string]any{"consumer": *consumer, "status": "deleted"}, value), nil
	}
	if *statusOnly {
		cursor, err := inbox.Get(value.AccountID, *consumer)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, &commandError{typeName: "cursor_not_found", message: "增量 consumer 不存在", hint: "去掉 --status 首次轮询时会创建它。", code: 2}
			}
			return nil, err
		}
		return outputWithGeneration(map[string]any{"cursor": cursor}, value), nil
	}
	if *ack != "" {
		cursor, err := inbox.Ack(value.AccountID, *consumer, *ack)
		if err != nil {
			return nil, &commandError{typeName: "cursor_ack_failed", message: "增量批次确认失败", hint: privateStateError(err), code: 5}
		}
		return outputWithGeneration(map[string]any{"consumer": *consumer, "acknowledged": *ack, "cursor": cursor}, value), nil
	}
	if err := ensureCurrentIndex(value); err != nil {
		return nil, &commandError{typeName: "index_required", message: "new-messages 需要当前 generation 的完整消息索引", hint: privateStateError(err), code: 5}
	}
	result, current, stage, err := inbox.PollOrCreate(value.AccountID, *consumer, *start, *limit)
	if err != nil {
		var indexUnavailable *inbox.IndexUnavailableError
		if errors.As(err, &indexUnavailable) {
			return nil, &commandError{typeName: "index_required", message: "new-messages 需要 base/target generation 的完整消息索引", hint: privateStateError(err), code: 5}
		}
		if stage == "create" {
			return nil, &commandError{typeName: "cursor_create_failed", message: "增量 consumer 创建失败", hint: privateStateError(err), code: 5}
		}
		return nil, &commandError{typeName: "cursor_poll_failed", message: "增量消息读取失败", hint: privateStateError(err), code: 5}
	}
	value = current
	return withGeneration(commandOutput{data: result, meta: map[string]any{
		"untrusted": true, "delivery_semantics": "at_least_once", "ack_required": result.AckRequired,
	}}, value), nil
}
