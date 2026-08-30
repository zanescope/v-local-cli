package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
	_ "modernc.org/sqlite"
)

type chatImageRecoveryFixture struct {
	home          string
	snapshot      string
	accountPath   string
	account       state.AccountState
	evidenceID    string
	output        string
	recovered     []byte
	directURL     string
	aesKeyHex     string
	contentMD5    string
	messageTable  string
	messageDBPath string
}

func createChatImageRecoveryFixture(t *testing.T) chatImageRecoveryFixture {
	t.Helper()
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	value := image.NewRGBA(image.Rect(0, 0, 333, 211))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	recovered := encoded.Bytes()
	contentDigest := md5.Sum(recovered)
	contentMD5 := hex.EncodeToString(contentDigest[:])
	directURL := "https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=fixture-token%3D"
	aesKeyHex := hex.EncodeToString([]byte("0123456789abcdef"))
	messageDBPath := filepath.Join(snapshot, "message", "message_0.db")
	database, err := sql.Open("sqlite", messageDBPath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte("dong_zzc"))
	messageTable := "Msg_" + hex.EncodeToString(tableDigest[:])
	descriptor := `<msg><img md5="` + strings.Repeat("b", 32) + `" cdnbigimgurl="` + directURL + `" aeskey="` + aesKeyHex +
		`" hdlength="` + strconv.Itoa(len(recovered)) + `" cdnhdwidth="333" cdnhdheight="211" originsourcemd5="` + contentMD5 + `" /></msg>`
	if _, err := database.Exec("UPDATE ["+messageTable+"] SET message_content=? WHERE local_id=12", descriptor); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	accountID := state.AccountID("chat-image-remote-recovery")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	messageDBPath = filepath.Join(snapshot, "message", "message_0.db")
	account := state.AccountState{
		AccountID: accountID, AccountName: "remote-recovery-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-remote-recovery",
		SnapshotManifestSHA256: strings.Repeat("1", 64), SnapshotCreatedAt: "2026-08-29T08:00:00Z",
		UpdatedAt: "2026-08-29T08:00:00Z", Storage: "snapshot-only",
	}
	if err := state.Save(&account); err != nil {
		t.Fatal(err)
	}
	return chatImageRecoveryFixture{
		home: home, snapshot: snapshot, accountPath: accountPath, account: account,
		evidenceID: "wechat:dong_zzc:9002", output: filepath.Join(t.TempDir(), "recovered.png"),
		recovered: recovered, directURL: directURL, aesKeyHex: aesKeyHex, contentMD5: contentMD5,
		messageTable: messageTable, messageDBPath: messageDBPath,
	}
}

func recoveryChallenge(t *testing.T, fixture chatImageRecoveryFixture) (string, map[string]any) {
	t.Helper()
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, fixture.evidenceID)
	if code != 3 {
		t.Fatalf("离线预检未返回授权 challenge：code=%d failure=%v", code, failure)
	}
	errorValue := failure["error"].(map[string]any)
	if errorValue["type"] != "chat_image_recovery_network_authorization_required" {
		t.Fatalf("离线预检错误类型异常：%v", errorValue)
	}
	details := errorValue["details"].(map[string]any)
	challenge := details["consent_challenge"].(map[string]any)
	id, _ := challenge["id"].(string)
	if id == "" || challenge["scope"] != chatImageRecoveryConsentScope || challenge["evidence_id"] != fixture.evidenceID ||
		challenge["account"] != fixture.account.AccountName || challenge["account_id"] != fixture.account.AccountID ||
		challenge["generation_id"] != fixture.account.GenerationID || challenge["snapshot_manifest_sha256"] != fixture.account.SnapshotManifestSHA256 ||
		len(challenge["candidate_descriptor_sha256"].(string)) != 64 || challenge["network_destination"] != "novac2c.cdn.weixin.qq.com" ||
		challenge["local_quality_tier"] != "medium" ||
		challenge["descriptor_expiry_known"] != false || challenge["descriptor_expiry_status"] != "unknown_without_verified_request" ||
		challenge["network_attempts_authorized"] != float64(1) || challenge["wechat_ui_automation_authorized"] != false ||
		details["network_access_performed"] != false || details["source_original_quality_status"] != "unknown" {
		t.Fatalf("结构化 challenge 未严格绑定或越权：%v", details)
	}
	return id, challenge
}

func installSuccessfulChatImageRecovery(t *testing.T, fixture chatImageRecoveryFixture, calls *int) {
	t.Helper()
	previous := recoverChatImageRemote
	t.Cleanup(func() { recoverChatImageRemote = previous })
	recoverChatImageRemote = func(_ context.Context, root, evidenceID, localTier, descriptorSHA256 string) (store.ChatImageRemoteArtifact, error) {
		*calls++
		if root != fixture.snapshot || evidenceID != fixture.evidenceID || localTier != "medium" || len(descriptorSHA256) != 64 {
			t.Fatalf("联网恢复未保持 challenge 绑定：root=%q evidence=%q local=%q descriptor=%q", root, evidenceID, localTier, descriptorSHA256)
		}
		inspection, err := store.InspectChatImageRemoteRecovery(root, evidenceID, localTier)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(fixture.recovered)
		return store.ChatImageRemoteArtifact{
			EvidenceID: evidenceID, Chat: "dong_zzc", LocalID: 12, ServerID: 9002,
			Timestamp: 1700000001, SortKey: 1700000001000, Format: "png", Bytes: len(fixture.recovered),
			Width: 333, Height: 211, SHA256: hex.EncodeToString(digest[:]), ContentMD5: fixture.contentMD5,
			QualityTier: "high", QualityBasis: "snapshot_remote_descriptor_variant", SourceOriginalQualityStatus: "unknown",
			ContainerValidation: "image.DecodeConfig+full_decode", DecryptionScope: "full_payload_aes_128_ecb_pkcs7",
			MIMEStatus: "response_mime_and_decoded_structure_consistent", DescriptorBytesStatus: "match",
			DescriptorDimensionsStatus: "match_observation_not_quality_gate", DescriptorMD5Status: "match",
			DescriptorSHA256: descriptorSHA256, MessageBindingSHA256: inspection.MessageBindingSHA256,
			RetrievedAt: "2026-08-29T08:01:00Z", NetworkAccessPerformed: true, Data: append([]byte(nil), fixture.recovered...),
		}, nil
	}
}

func TestExportChatImageRoutesDirectFullURLThroughOfflineRecoveryPreflight(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	localOutput := filepath.Join(t.TempDir(), "local.png")
	code, output, failure := runForTest("export-chat-image", "--account", fixture.account.AccountName, "--output", localOutput, fixture.evidenceID)
	if code != 0 {
		t.Fatalf("本地聊天图片导出失败：code=%d failure=%v", code, failure)
	}
	data := output["data"].(map[string]any)
	if data["quality_tier"] != "medium" || data["higher_quality_local_status"] != "missing" ||
		data["higher_quality_recovery_action"] != "run_recover_chat_image_offline_then_request_structured_consent" ||
		data["remote_acquisition_status"] != "inspect_via_recover_chat_image_with_single_attempt_consent" ||
		data["network_access_performed"] != false || data["source_original_quality_status"] != "unknown" {
		t.Fatalf("本地不足时没有路由到离线恢复预检：%v", data)
	}
}

func TestRecoverChatImageRequiresBoundConsentAndRejectsReplay(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	calls := 0
	installSuccessfulChatImageRecovery(t, fixture, &calls)
	challengeID, _ := recoveryChallenge(t, fixture)
	if calls != 0 {
		t.Fatal("未授权预检触发了联网恢复")
	}
	code, output, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	if code != 0 {
		t.Fatalf("授权后的单次恢复失败：code=%d failure=%v", code, failure)
	}
	if calls != 1 {
		t.Fatalf("单次授权触发了 %d 次恢复", calls)
	}
	data := output["data"].(map[string]any)
	if data["source_original_quality_status"] != "unknown" || data["quality_claim_scope"] != "wechat_remote_variant_only" ||
		data["network_attempts"] != float64(1) || data["authorization_consumed"] != true ||
		data["remote_descriptor_status"] != "verified_at_request_time" || data["descriptor_expiry_known"] != false ||
		data["descriptor_expiry_status"] != "unknown_future" || data["observed_at"] != "2026-08-29T08:00:00Z" ||
		data["retrieved_at"] != "2026-08-29T08:01:00Z" {
		t.Fatalf("成功响应夸大质量或丢失时效边界：%v", data)
	}
	exported, err := os.ReadFile(fixture.output)
	if err != nil || !bytes.Equal(exported, fixture.recovered) {
		t.Fatalf("恢复输出异常：bytes=%d err=%v", len(exported), err)
	}
	if err := os.Remove(fixture.output); err != nil {
		t.Fatal(err)
	}
	code, _, failure = runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	replayError := failure["error"].(map[string]any)
	replayDetails := replayError["details"].(map[string]any)
	if code == 0 || replayError["type"] != "chat_image_recovery_consent_replayed" || calls != 1 ||
		replayDetails["authorization_consumed"] != true || replayDetails["source_original_quality_status"] != "unknown" {
		t.Fatalf("授权重放未在联网前拒绝：code=%d failure=%v calls=%d", code, failure, calls)
	}
}

func TestRecoverChatImagePreflightPrunesExpiredConsentRecords(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	previousNow := chatImageRecoveryNow
	current := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	chatImageRecoveryNow = func() time.Time { return current }
	t.Cleanup(func() { chatImageRecoveryNow = previousNow })

	pendingID, _ := recoveryChallenge(t, fixture)
	usedID, _ := recoveryChallenge(t, fixture)
	if _, err := consumeChatImageRecoveryConsent(fixture.account.AccountID, usedID); err != nil {
		t.Fatal(err)
	}
	directory, err := state.EnsureRecoveryConsentPath(fixture.account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(directory, pendingID+".pending.json")
	usedPath := filepath.Join(directory, usedID+".used.json")
	if _, err := os.Lstat(pendingPath); err != nil {
		t.Fatalf("过期测试 pending 夹具缺失：%v", err)
	}
	if _, err := os.Lstat(usedPath); err != nil {
		t.Fatalf("过期测试 used 夹具缺失：%v", err)
	}

	current = current.Add(chatImageRecoveryConsentTTL + time.Second)
	currentID, _ := recoveryChallenge(t, fixture)
	for _, path := range []string{pendingPath, usedPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("签发新 challenge 后仍保留过期记录 %q：%v", path, err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != currentID+".pending.json" {
		t.Fatalf("授权目录清理后状态异常：%v", entries)
	}
}

func TestRecoverChatImageRejectsExpiredConsentAndSnapshotChangeBeforeNetwork(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		previousNow := chatImageRecoveryNow
		current := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
		chatImageRecoveryNow = func() time.Time { return current }
		t.Cleanup(func() { chatImageRecoveryNow = previousNow })
		challengeID, _ := recoveryChallenge(t, fixture)
		current = current.Add(chatImageRecoveryConsentTTL + time.Second)
		calls := 0
		installSuccessfulChatImageRecovery(t, fixture, &calls)
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		errorValue := failure["error"].(map[string]any)
		details := errorValue["details"].(map[string]any)
		if code == 0 || errorValue["type"] != "chat_image_recovery_consent_expired" || calls != 0 ||
			details["observed_at"] != fixture.account.SnapshotCreatedAt || details["retrieved_at"] != nil {
			t.Fatalf("过期授权未在联网前拒绝：code=%d failure=%v calls=%d", code, failure, calls)
		}
	})
	t.Run("clock_rollback", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		previousNow := chatImageRecoveryNow
		current := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
		chatImageRecoveryNow = func() time.Time { return current }
		t.Cleanup(func() { chatImageRecoveryNow = previousNow })
		challengeID, _ := recoveryChallenge(t, fixture)
		current = current.Add(-time.Second)
		calls := 0
		installSuccessfulChatImageRecovery(t, fixture, &calls)
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_recovery_consent_expired" || calls != 0 {
			t.Fatalf("系统时钟回退未在联网前拒绝授权：code=%d failure=%v calls=%d", code, failure, calls)
		}
	})
	t.Run("snapshot_changed", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		challengeID, _ := recoveryChallenge(t, fixture)
		fixture.account.GenerationID = "generation-after-refresh"
		fixture.account.SnapshotManifestSHA256 = strings.Repeat("2", 64)
		fixture.account.SnapshotCreatedAt = "2026-08-29T08:02:00Z"
		if err := state.Save(&fixture.account); err != nil {
			t.Fatal(err)
		}
		calls := 0
		installSuccessfulChatImageRecovery(t, fixture, &calls)
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		errorValue := failure["error"].(map[string]any)
		details := errorValue["details"].(map[string]any)
		if code == 0 || errorValue["type"] != "chat_image_recovery_snapshot_changed" || calls != 0 ||
			details["observed_at"] != "2026-08-29T08:00:00Z" || details["current_snapshot_observed_at"] != fixture.account.SnapshotCreatedAt || details["retrieved_at"] != nil {
			t.Fatalf("快照变化未在联网前拒绝：code=%d failure=%v calls=%d", code, failure, calls)
		}
	})
}

func TestRecoverChatImageRejectsOutputScopeMismatchBeforeNetwork(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	challengeID, _ := recoveryChallenge(t, fixture)
	calls := 0
	installSuccessfulChatImageRecovery(t, fixture, &calls)
	otherOutput := filepath.Join(t.TempDir(), "other.png")
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", otherOutput, "--consent", challengeID, fixture.evidenceID)
	if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_recovery_consent_scope_mismatch" || calls != 0 {
		t.Fatalf("输出目标错配未在联网前拒绝：code=%d failure=%v calls=%d", code, failure, calls)
	}
}

func TestRecoverChatImageRejectsReplacedOutputDirectoryBeforeNetwork(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	challengeID, _ := recoveryChallenge(t, fixture)
	authorizedDirectory := filepath.Dir(fixture.output)
	displacedDirectory := authorizedDirectory + "-authorized"
	if err := os.Rename(authorizedDirectory, displacedDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(authorizedDirectory)
		_ = os.Rename(displacedDirectory, authorizedDirectory)
	})
	if err := os.Mkdir(authorizedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	installSuccessfulChatImageRecovery(t, fixture, &calls)
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_recovery_consent_scope_mismatch" || calls != 0 {
		t.Fatalf("输出父目录替换未在联网前使授权失效：code=%d failure=%v calls=%d", code, failure, calls)
	}
}

func TestRecoverChatImageRejectsLinkedOutputAncestorBeforeConsent(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	root := t.TempDir()
	linked := filepath.Join(root, "linked-output")
	if err := os.Symlink(filepath.Dir(fixture.output), linked); err != nil {
		t.Skipf("当前环境不允许创建目录符号链接：%v", err)
	}
	output := filepath.Join(linked, "recovered.png")
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", output, fixture.evidenceID)
	if code == 0 || failure["error"].(map[string]any)["type"] != "invalid_output" {
		t.Fatalf("含链接祖先的恢复输出仍签发授权：code=%d failure=%v", code, failure)
	}
}

func TestRecoverChatImageSnapshotLockStopsBeforeConsentConsumption(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	calls := 0
	installSuccessfulChatImageRecovery(t, fixture, &calls)
	challengeID, _ := recoveryChallenge(t, fixture)
	lock, err := state.AcquireAccountLock(fixture.account.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	errorValue := failure["error"].(map[string]any)
	details := errorValue["details"].(map[string]any)
	if code == 0 || errorValue["type"] != "snapshot_busy" || calls != 0 || details["network_access_performed"] != false ||
		details["authorization_consumed"] != false || details["retry_policy"] != "wait_for_snapshot_transaction_then_retry_same_command" ||
		details["source_original_quality_status"] != "unknown" {
		t.Fatalf("快照事务锁竞争未在消费授权和联网前停止：code=%d failure=%v calls=%d", code, failure, calls)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	code, _, failure = runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	if code != 0 || calls != 1 {
		t.Fatalf("锁竞争结束后未能使用尚未消费的 challenge：code=%d failure=%v calls=%d", code, failure, calls)
	}
}

func TestRecoverChatImageRejectsDescriptorChangeAndLowOnlyCandidate(t *testing.T) {
	t.Run("descriptor_changed", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		challengeID, _ := recoveryChallenge(t, fixture)
		database, err := sql.Open("sqlite", fixture.messageDBPath)
		if err != nil {
			t.Fatal(err)
		}
		changed := strings.Replace(fixture.directURL, "fixture-token", "changed-token", 1)
		if _, err := database.Exec("UPDATE ["+fixture.messageTable+"] SET message_content=replace(message_content, ?, ?) WHERE local_id=12", fixture.directURL, changed); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		_ = database.Close()
		calls := 0
		installSuccessfulChatImageRecovery(t, fixture, &calls)
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_recovery_descriptor_changed" || calls != 0 {
			t.Fatalf("描述符变化未在联网前拒绝：code=%d failure=%v calls=%d", code, failure, calls)
		}
	})
	t.Run("only_lower_cache_variant", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		database, err := sql.Open("sqlite", fixture.messageDBPath)
		if err != nil {
			t.Fatal(err)
		}
		descriptor := `<msg><img md5="` + strings.Repeat("b", 32) + `" cdnthumburl="` + fixture.directURL + `" cdnthumbaeskey="` + fixture.aesKeyHex +
			`" cdnthumblength="` + strconv.Itoa(len(fixture.recovered)) + `" cdnthumbwidth="333" cdnthumbheight="211" /></msg>`
		if _, err := database.Exec("UPDATE ["+fixture.messageTable+"] SET message_content=? WHERE local_id=12", descriptor); err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		_ = database.Close()
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, fixture.evidenceID)
		if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_recovery_no_higher_variant" {
			t.Fatalf("只有低层级缓存时仍签发了联网授权：code=%d failure=%v", code, failure)
		}
	})
}

func TestRecoverChatImageReportsCDNExpiryUnknownAndCleanupFailure(t *testing.T) {
	t.Run("authorization_rejected_maybe_expired", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		challengeID, _ := recoveryChallenge(t, fixture)
		previous := recoverChatImageRemote
		t.Cleanup(func() { recoverChatImageRemote = previous })
		recoverChatImageRemote = func(context.Context, string, string, string, string) (store.ChatImageRemoteArtifact, error) {
			return store.ChatImageRemoteArtifact{}, &store.ChatImageRemoteRecoveryError{
				Kind: "chat_image_remote_authorization_rejected", DescriptorExpiryStatus: "unknown_unavailable_at_request_time", NetworkAttempted: true,
			}
		}
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		errorValue := failure["error"].(map[string]any)
		details := errorValue["details"].(map[string]any)
		if code == 0 || errorValue["type"] != "chat_image_remote_authorization_rejected" ||
			details["descriptor_expiry_known"] != false || details["descriptor_expiry_status"] != "unknown_unavailable_at_request_time" ||
			details["retry_policy"] != "new_challenge_and_new_explicit_authorization_required" ||
			details["observed_at"] != fixture.account.SnapshotCreatedAt || details["retrieved_at"] != nil {
			t.Fatalf("CDN 鉴权失败被误报成已确认过期或可复用：code=%d failure=%v", code, failure)
		}
	})
	t.Run("cleanup_failure", func(t *testing.T) {
		fixture := createChatImageRecoveryFixture(t)
		calls := 0
		installSuccessfulChatImageRecovery(t, fixture, &calls)
		challengeID, _ := recoveryChallenge(t, fixture)
		previousRemove := removeChatImageRecoveryTemporary
		removeChatImageRecoveryTemporary = func(string) error { return errors.New("synthetic cleanup failure") }
		t.Cleanup(func() { removeChatImageRecoveryTemporary = previousRemove })
		code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
		errorValue := failure["error"].(map[string]any)
		details := errorValue["details"].(map[string]any)
		if code == 0 || errorValue["type"] != "chat_image_recovery_temporary_cleanup_failed" || details["output_committed"] != true || calls != 1 ||
			details["remote_descriptor_status"] != "verified_at_request_time" || details["observed_at"] != fixture.account.SnapshotCreatedAt ||
			details["retrieved_at"] != "2026-08-29T08:01:00Z" {
			t.Fatalf("清理失败未显式报告：code=%d failure=%v calls=%d", code, failure, calls)
		}
		removeChatImageRecoveryTemporary = previousRemove
		leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(fixture.output), ".v-local-cli-output-*.tmp"))
		if err != nil || len(leftovers) != 1 {
			t.Fatalf("清理失败夹具遗留数量异常：%v err=%v", leftovers, err)
		}
		if err := os.Remove(leftovers[0]); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRecoverChatImageRejectsRemoteArtifactOriginalQualityOverclaim(t *testing.T) {
	fixture := createChatImageRecoveryFixture(t)
	calls := 0
	installSuccessfulChatImageRecovery(t, fixture, &calls)
	baseRecovery := recoverChatImageRemote
	recoverChatImageRemote = func(ctx context.Context, root, evidenceID, localTier, descriptorSHA256 string) (store.ChatImageRemoteArtifact, error) {
		artifact, err := baseRecovery(ctx, root, evidenceID, localTier, descriptorSHA256)
		artifact.SourceOriginalQualityStatus = "known"
		return artifact, err
	}
	challengeID, _ := recoveryChallenge(t, fixture)
	code, _, failure := runForTest("recover-chat-image", "--account", fixture.account.AccountName, "--output", fixture.output, "--consent", challengeID, fixture.evidenceID)
	errorValue := failure["error"].(map[string]any)
	details := errorValue["details"].(map[string]any)
	if code == 0 || errorValue["type"] != "chat_image_recovery_download_binding_mismatch" || calls != 1 ||
		details["source_original_quality_status"] != "unknown" {
		t.Fatalf("远端产物质量夸大未被拒绝：code=%d failure=%v calls=%d", code, failure, calls)
	}
	if _, err := os.Lstat(fixture.output); !os.IsNotExist(err) {
		t.Fatalf("质量夸大的远端产物被写出：%v", err)
	}
}
