package app

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localplatform "github.com/zanescope/v-local-cli/internal/platform"
	"github.com/zanescope/v-local-cli/internal/provider"
	"github.com/zanescope/v-local-cli/internal/state"
	_ "modernc.org/sqlite"
)

func assertWXGFDecoderDiagnostics(t *testing.T, raw any) {
	t.Helper()
	diagnostics, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("WXGF 解码器诊断缺失或类型错误：%T %v", raw, raw)
	}
	if diagnostics["format"] != "wxgf" || diagnostics["status_scope"] != "public_cli_build" ||
		diagnostics["binary_presence_status"] != "not_evaluated" ||
		diagnostics["binary_presence_reason"] != "public_cli_does_not_inspect_path" ||
		diagnostics["path_auto_discovery"] != false || diagnostics["public_cli_integration_status"] != "not_wired" ||
		diagnostics["qualification_interface_status"] != "explicit_test_only" ||
		diagnostics["production_qualification_status"] != "not_qualified" ||
		diagnostics["qualification_success_enables_public_export"] != false {
		t.Fatalf("WXGF 解码器诊断扩大了公共构建能力：%v", diagnostics)
	}
}

func createChatImageExportFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	snapshot := t.TempDir()
	accountPath := t.TempDir()
	chat := "dong_zzc"
	stem := strings.Repeat("a", 32)
	mediaMD5 := strings.Repeat("b", 32)

	value := image.NewRGBA(image.Rect(0, 0, 2048, 1536))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()

	messagePath := filepath.Join(snapshot, "message", "message_0.db")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte(chat))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	if _, err := messageDB.Exec("CREATE TABLE [" + table + "](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,create_time INTEGER,message_content TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := messageDB.Exec("INSERT INTO ["+table+"] VALUES(12,9002,3,1700000001000,1700000001,?)", "<msg><img md5=\""+mediaMD5+"\" /></msg>"); err != nil {
		t.Fatal(err)
	}
	if err := messageDB.Close(); err != nil {
		t.Fatal(err)
	}

	resourcePath := filepath.Join(snapshot, "resource", "message_resource.db")
	if err := os.MkdirAll(filepath.Dir(resourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	resourceDB, err := sql.Open("sqlite", resourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resourceDB.Exec("CREATE TABLE MessageResourceInfo(message_local_id INTEGER,message_local_type INTEGER,message_svr_id INTEGER,packed_info BLOB)"); err != nil {
		t.Fatal(err)
	}
	inner := append([]byte{0x0a, 0x20}, []byte(stem)...)
	packed := append([]byte{0x12, byte(len(inner))}, inner...)
	if _, err := resourceDB.Exec("INSERT INTO MessageResourceInfo VALUES(?,?,?,?)", 12, 3, 9002, packed); err != nil {
		t.Fatal(err)
	}
	if err := resourceDB.Close(); err != nil {
		t.Fatal(err)
	}

	hardlinkPath := filepath.Join(snapshot, "hardlink", "hardlink.db")
	if err := os.MkdirAll(filepath.Dir(hardlinkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	hardlinkDB, err := sql.Open("sqlite", hardlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		"CREATE TABLE dir2id(username TEXT)",
		"INSERT INTO dir2id(rowid,username) VALUES(1,'segment-a'),(2,'segment-b')",
		"CREATE TABLE image_hardlink_info_v4(md5 TEXT,file_name TEXT,dir1 INTEGER,dir2 INTEGER)",
		"INSERT INTO image_hardlink_info_v4 VALUES('" + mediaMD5 + "', '" + stem + ".dat', 1, 2)",
	}
	for _, statement := range statements {
		if _, err := hardlinkDB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}

	mediaPath := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b", stem+".dat")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return snapshot, accountPath, payload
}

func TestExportChatImageUsesEvidenceBoundFullResolutionCandidate(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	accountID := state.AccountID("chat-image-acceptance")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "acceptance-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-chat-image", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}

	evidenceID := "wechat:dong_zzc:9002"
	outputPath := filepath.Join(t.TempDir(), "dong-zzc-full.png")
	code, output, failure := runForTest("export-chat-image", "--account", "acceptance-test", "--output", outputPath, evidenceID)
	if code != 0 {
		t.Fatalf("聊天图片导出失败：code=%d output=%v failure=%v", code, output, failure)
	}
	data := output["data"].(map[string]any)
	digest := sha256.Sum256(expected)
	if data["evidence_id"] != evidenceID || data["width"] != float64(2048) || data["height"] != float64(1536) ||
		data["sha256"] != hex.EncodeToString(digest[:]) || data["verified_by"] != "message_resource_stem+hardlink_map+full_decode" ||
		data["container_validation"] != "full_decode" || data["resolution_status"] != "verified_local" ||
		data["quality_tier"] != "medium" || data["quality_basis"] != "hardlink_cache_filename_variant" ||
		data["quality_claim_scope"] != "wechat_cache_variant_only" || data["source_original_dimensions_known"] != false ||
		data["dimensions_role"] != "decoded_output_observation_not_quality_gate" ||
		data["higher_quality_local_status"] != "missing" || data["higher_quality_recovery_action"] != "ask_user_to_open_original_then_refresh_and_retry" ||
		data["remote_descriptor_status"] != "missing" || data["remote_descriptor_parse_status"] != "not_applicable" || data["remote_protocol_status"] != "not_applicable" ||
		data["remote_acquisition_status"] != "not_available_no_descriptor" || data["network_access_performed"] != false {
		t.Fatalf("聊天图片强绑定元数据异常：%v", data)
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("聊天图片导出内容异常：bytes=%d err=%v", len(exported), err)
	}
	serialized, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte(accountPath)) || bytes.Contains(serialized, []byte(filepath.ToSlash(accountPath))) {
		t.Fatalf("聊天图片导出响应泄露源账号路径：%s", serialized)
	}

	code, _, failure = runForTest("export-chat-image", "--account", "acceptance-test", "--output", outputPath, evidenceID)
	if code == 0 || failure["error"].(map[string]any)["type"] != "output_exists" {
		t.Fatalf("聊天图片导出未保护已有输出：code=%d failure=%v", code, failure)
	}
}

func TestExportChatImageRejectsConflictingStrongCandidates(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	conflictPath := filepath.Join(accountPath, "msg", "attach", "another-session", stem+".dat")
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0o700); err != nil {
		t.Fatal(err)
	}
	var conflict bytes.Buffer
	if err := png.Encode(&conflict, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflictPath, conflict.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-conflict")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "conflict-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-conflict", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "must-not-exist.png")
	code, _, failure := runForTest("export-chat-image", "--account", "conflict-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_unavailable" {
		t.Fatalf("冲突的强候选未 fail closed：code=%d failure=%v", code, failure)
	}
	details := failure["error"].(map[string]any)["details"].(map[string]any)
	if details["reason"] != "content_conflict" || details["local_resolution_status"] != "content_conflict" || details["recovery_action"] != "manual_review" || details["network_access_performed"] != false {
		t.Fatalf("冲突候选缺少安全诊断：%v", details)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("冲突候选仍产生了输出：err=%v", err)
	}
}

func TestExportChatImageReportsThumbnailAndPotentiallyExpiredRemoteDescriptor(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	mediaMD5 := strings.Repeat("b", 32)

	messagePath := filepath.Join(snapshot, "message", "message_0.db")
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte("dong_zzc"))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	remoteHighParameter := strings.Repeat("c1", 89)
	remoteMediumParameter := strings.Repeat("d2", 96)
	remoteKey := strings.Repeat("e3", 16)
	descriptor := `<msg><img md5="` + mediaMD5 + `" cdnbigimgurl="` + remoteHighParameter + `" cdnmidimgurl="` + remoteMediumParameter +
		`" aeskey="` + remoteKey + `" hdlength="4096" cdnhdwidth="320" cdnhdheight="240" /></msg>`
	if _, err := messageDB.Exec("UPDATE ["+table+"] SET message_content=? WHERE local_id=12", descriptor); err != nil {
		_ = messageDB.Close()
		t.Fatal(err)
	}
	if err := messageDB.Close(); err != nil {
		t.Fatal(err)
	}
	hardlinkDB, err := sql.Open("sqlite", filepath.Join(snapshot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("UPDATE image_hardlink_info_v4 SET file_name=?", stem+"_t.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b", stem+".dat")
	thumbnail := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b", stem+"_t.dat")
	if err := os.Rename(original, thumbnail); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-thumbnail-descriptor")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "thumbnail-descriptor-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-thumbnail-descriptor", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "thumbnail.png")
	code, output, failure := runForTest("export-chat-image", "--account", "thumbnail-descriptor-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code != 0 {
		t.Fatalf("缩略图导出失败：code=%d failure=%v", code, failure)
	}
	data := output["data"].(map[string]any)
	if data["quality_tier"] != "thumbnail" || data["remote_descriptor_status"] != "present_expiry_unknown" ||
		data["remote_descriptor_parse_status"] != "parsed_unverified_protocol" ||
		data["higher_quality_local_status"] != "missing" || data["higher_quality_recovery_action"] != "ask_user_to_open_original_then_refresh_and_retry" ||
		data["remote_protocol_status"] != "unverified_desktop_protocol" || data["remote_acquisition_status"] != "unavailable_unverified_protocol" {
		t.Fatalf("缩略图或远端时效状态异常：%v", data)
	}
	tiers := data["remote_descriptor_tiers"].([]any)
	if len(tiers) != 2 || tiers[0] != "high" || tiers[1] != "medium" {
		t.Fatalf("远端质量提示异常：%v", tiers)
	}
	serialized, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{remoteHighParameter, remoteMediumParameter, remoteKey} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatalf("聊天图片状态泄露了 CDN 描述符或密钥：%s", serialized)
		}
	}
	for _, overclaim := range []string{"verified_at_request_time", "unknown_future"} {
		if bytes.Contains(serialized, []byte(overclaim)) {
			t.Fatalf("未联网的聊天图片错误声称验证了描述符时效：%s", serialized)
		}
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("缩略图导出内容异常：bytes=%d err=%v", len(exported), err)
	}
}

func TestExportChatImageAcceptsLowResolutionHighCacheVariant(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	mediaMD5 := strings.Repeat("b", 32)

	hardlinkDB, err := sql.Open("sqlite", filepath.Join(snapshot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("UPDATE image_hardlink_info_v4 SET file_name=?", stem+"_t.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("INSERT INTO image_hardlink_info_v4 VALUES(?,?,1,2)", mediaMD5, stem+"_h.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b")
	if err := os.Rename(filepath.Join(base, stem+".dat"), filepath.Join(base, stem+"_t.dat")); err != nil {
		t.Fatal(err)
	}
	var thumbnailEncoded bytes.Buffer
	if err := png.Encode(&thumbnailEncoded, image.NewRGBA(image.Rect(0, 0, 120, 90))); err != nil {
		t.Fatal(err)
	}
	thumbnailPayload := thumbnailEncoded.Bytes()
	if err := os.WriteFile(filepath.Join(base, stem+"_t.dat"), thumbnailPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	// 320x240 可以是源图本身的完整尺寸；high 只来自缓存文件档位，不能被固定像素门槛否定。
	highImage := image.NewRGBA(image.Rect(0, 0, 320, 240))
	var highEncoded bytes.Buffer
	if err := png.Encode(&highEncoded, highImage); err != nil {
		t.Fatal(err)
	}
	highPayload := highEncoded.Bytes()
	if bytes.Equal(highPayload, thumbnailPayload) {
		t.Fatal("high 缓存档位与缩略图夹具必须是不同内容")
	}
	if err := os.WriteFile(filepath.Join(base, stem+"_h.dat"), highPayload, 0o600); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-distinct-high")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "distinct-high-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-distinct-high", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "high.png")
	code, output, failure := runForTest("export-chat-image", "--account", "distinct-high-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code != 0 {
		t.Fatalf("不同内容的合法 high 缓存档位/缩略图被误判为冲突：code=%d failure=%v", code, failure)
	}
	data := output["data"].(map[string]any)
	if data["quality_tier"] != "high" || data["width"] != float64(320) || data["height"] != float64(240) ||
		data["quality_claim_scope"] != "wechat_cache_variant_only" || data["source_original_dimensions_known"] != false ||
		data["dimensions_role"] != "decoded_output_observation_not_quality_gate" ||
		data["higher_quality_local_status"] != "not_applicable" || data["higher_quality_recovery_action"] != "none" {
		t.Fatalf("没有优先选择已验真的 high 缓存档位候选：%v", data)
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, highPayload) {
		t.Fatalf("导出的不是 high 缓存档位候选：bytes=%d err=%v", len(exported), err)
	}
}

func TestChatImageRecoveryRefreshesAfterUserOpensOriginalThenRetriesOnce(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	sourceDatabases, accountPath, thumbnailPayload := createChatImageExportFixture(t)
	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", accountPath)
	stem := strings.Repeat("a", 32)
	mediaMD5 := strings.Repeat("b", 32)

	messagePath := filepath.Join(sourceDatabases, "message", "message_0.db")
	messageDB, err := sql.Open("sqlite", messagePath)
	if err != nil {
		t.Fatal(err)
	}
	tableDigest := md5.Sum([]byte("dong_zzc"))
	table := "Msg_" + hex.EncodeToString(tableDigest[:])
	remoteHighParameter := strings.Repeat("c4", 89)
	remoteKey := strings.Repeat("d5", 16)
	descriptor := `<msg><img md5="` + mediaMD5 + `" cdnbigimgurl="` + remoteHighParameter + `" aeskey="` + remoteKey +
		`" hdlength="4096" cdnhdwidth="320" cdnhdheight="240" /></msg>`
	if _, err := messageDB.Exec("UPDATE ["+table+"] SET message_content=? WHERE local_id=12", descriptor); err != nil {
		_ = messageDB.Close()
		t.Fatal(err)
	}
	if err := messageDB.Close(); err != nil {
		t.Fatal(err)
	}

	hardlinkPath := filepath.Join(sourceDatabases, "hardlink", "hardlink.db")
	hardlinkDB, err := sql.Open("sqlite", hardlinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("UPDATE image_hardlink_info_v4 SET file_name=?", stem+"_t.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}
	mediaBase := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b")
	if err := os.Rename(filepath.Join(mediaBase, stem+".dat"), filepath.Join(mediaBase, stem+"_t.dat")); err != nil {
		t.Fatal(err)
	}

	databaseRoot := filepath.Join(accountPath, "db_storage")
	if err := os.MkdirAll(databaseRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"message", "resource", "hardlink"} {
		if err := os.Rename(filepath.Join(sourceDatabases, directory), filepath.Join(databaseRoot, directory)); err != nil {
			t.Fatal(err)
		}
	}
	const aesKey = "0123456789abcdef"
	const xorKey = byte(90)
	validationPath := filepath.Join(accountPath, "cache", "media-key-validation.dat")
	if err := os.MkdirAll(filepath.Dir(validationPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticV2DAT(t, validationPath, syntheticPNG(t), aesKey, xorKey)
	bundle := provider.CandidateBundle{
		DatabaseKeys: map[string]string{"*": strings.Repeat("c", 64)},
		ImageKeys:    &provider.ImageKeys{AES: aesKey, XOR: int(xorKey)},
	}
	account := localplatform.Account{Name: filepath.Base(accountPath), Path: accountPath, DBDir: databaseRoot}
	if _, err := publishAccountSnapshot(account, bundle, snapshotPublishOptions{
		Storage: "keychain", RequireMedia: true, PreventCoverageRegression: true,
		CredentialSource: "saved_keychain", BuildIndex: true,
	}); err != nil {
		t.Fatalf("无法发布初始缩略图夹具：%v", err)
	}

	evidenceID := "wechat:dong_zzc:9002"
	firstOutputPath := filepath.Join(t.TempDir(), "before-user-confirmation.png")
	code, firstOutput, failure := runForTest("export-chat-image", "--account", account.Name, "--output", firstOutputPath, evidenceID)
	if code != 0 {
		t.Fatalf("初始缩略图诊断失败：code=%d failure=%v", code, failure)
	}
	firstData := firstOutput["data"].(map[string]any)
	if firstData["quality_tier"] != "thumbnail" || firstData["higher_quality_local_status"] != "missing" ||
		firstData["higher_quality_recovery_action"] != "ask_user_to_open_original_then_refresh_and_retry" ||
		firstData["remote_descriptor_status"] != "present_expiry_unknown" ||
		firstData["remote_descriptor_parse_status"] != "parsed_unverified_protocol" || firstData["network_access_performed"] != false {
		t.Fatalf("Agent 未得到安全的用户确认恢复动作：%v", firstData)
	}
	initialGeneration := firstOutput["meta"].(map[string]any)["generation_id"]

	// 模拟用户确认后只在微信中打开这一张原图：源 hardlink 记录和 high 缓存档位本地文件
	// 随之出现。Agent 的下一步应是 refresh，再自动重试同一个 evidence_id。
	hardlinkDB, err = sql.Open("sqlite", filepath.Join(databaseRoot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("INSERT INTO image_hardlink_info_v4 VALUES(?,?,1,2)", mediaMD5, stem+"_h.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}
	highImage := image.NewRGBA(image.Rect(0, 0, 2560, 1440))
	var highEncoded bytes.Buffer
	if err := png.Encode(&highEncoded, highImage); err != nil {
		t.Fatal(err)
	}
	highPayload := highEncoded.Bytes()
	if bytes.Equal(highPayload, thumbnailPayload) {
		t.Fatal("恢复夹具的 high 缓存档位图片必须不同于缩略图")
	}
	if err := os.WriteFile(filepath.Join(mediaBase, stem+"_h.dat"), highPayload, 0o600); err != nil {
		t.Fatal(err)
	}

	refreshResult, err := runRefreshWithSecrets([]string{"--account", account.Name, "--require-media"}, func(requestedAccountID string) (provider.CandidateBundle, error) {
		if requestedAccountID != state.AccountID(accountPath) {
			t.Fatalf("refresh 请求了错误账号：%s", requestedAccountID)
		}
		return bundle, nil
	})
	if err != nil {
		t.Fatalf("用户确认后的自动 refresh 失败：%v", err)
	}
	refreshData := refreshResult.(map[string]any)
	if refreshData["credential_source"] != "saved_keychain" || refreshData["process_access_performed"] != false ||
		refreshData["secrets_persisted"] != false {
		t.Fatalf("恢复 refresh 越过了安全边界：%v", refreshData)
	}

	retryOutputPath := filepath.Join(t.TempDir(), "after-single-refresh.png")
	code, retryOutput, failure := runForTest("export-chat-image", "--account", account.Name, "--output", retryOutputPath, evidenceID)
	if code != 0 {
		t.Fatalf("单次 refresh 后自动重试失败：code=%d failure=%v", code, failure)
	}
	retryData := retryOutput["data"].(map[string]any)
	if retryData["quality_tier"] != "high" || retryData["higher_quality_local_status"] != "not_applicable" ||
		retryData["higher_quality_recovery_action"] != "none" || retryData["network_access_performed"] != false {
		t.Fatalf("单次恢复没有选择本地 high 缓存档位图片：%v", retryData)
	}
	if retryOutput["meta"].(map[string]any)["generation_id"] == initialGeneration {
		t.Fatal("恢复重试没有绑定到 refresh 发布的新 generation")
	}
	exported, err := os.ReadFile(retryOutputPath)
	if err != nil || !bytes.Equal(exported, highPayload) {
		t.Fatalf("恢复重试导出的不是 high 缓存档位候选：bytes=%d err=%v", len(exported), err)
	}
	serialized, err := json.Marshal([]any{firstOutput, refreshResult, retryOutput})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{remoteHighParameter, remoteKey, aesKey} {
		if bytes.Contains(serialized, []byte(secret)) {
			t.Fatalf("恢复流程输出泄露描述符或密钥：%s", serialized)
		}
	}
}

func TestExportChatImageDoesNotRecommendRedownloadWhenHigherQualityIsWXGF(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	mediaMD5 := strings.Repeat("b", 32)

	hardlinkDB, err := sql.Open("sqlite", filepath.Join(snapshot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("UPDATE image_hardlink_info_v4 SET file_name=?", stem+"_t.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if _, err := hardlinkDB.Exec("INSERT INTO image_hardlink_info_v4 VALUES(?,?,1,2)", mediaMD5, stem+"_h.dat"); err != nil {
		_ = hardlinkDB.Close()
		t.Fatal(err)
	}
	if err := hardlinkDB.Close(); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b")
	if err := os.Rename(filepath.Join(base, stem+".dat"), filepath.Join(base, stem+"_t.dat")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, stem+"_h.dat"), append([]byte("wxgf"), make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-thumbnail-wxgf")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "thumbnail-wxgf-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-thumbnail-wxgf", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "thumbnail.png")
	code, output, failure := runForTest("export-chat-image", "--account", "thumbnail-wxgf-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code != 0 {
		t.Fatalf("WXGF high 缓存档位旁路下的缩略图导出失败：code=%d failure=%v", code, failure)
	}
	data := output["data"].(map[string]any)
	if data["quality_tier"] != "thumbnail" || data["higher_quality_local_status"] != "decoder_unavailable" ||
		data["higher_quality_detected_format"] != "wxgf" || data["higher_quality_recovery_action"] != "do_not_request_redownload_same_candidate" {
		t.Fatalf("没有区分 WXGF high 缓存档位与 higher-tier 缺失：%v", data)
	}
	assertWXGFDecoderDiagnostics(t, data["higher_quality_decoder_diagnostics"])
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("WXGF high 缓存档位旁路下的缩略图内容异常：bytes=%d err=%v", len(exported), err)
	}
}

func TestExportChatImageClassifiesWXGFDecoderUnavailable(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	fakeDecoderDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeDecoderDirectory, "ffmpeg.exe"), []byte("not-a-decoder"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDecoderDirectory)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	mediaPath := filepath.Join(accountPath, "msg", "attach", "segment-a", "segment-b", stem+".dat")
	if err := os.WriteFile(mediaPath, append([]byte("wxgf"), make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}
	accountID := state.AccountID("chat-image-wxgf")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "wxgf-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-wxgf", SnapshotManifestSHA256: "manifest-wxgf", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	code, _, failure := runForTest("export-chat-image", "--account", "wxgf-test", "--output", filepath.Join(t.TempDir(), "wxgf.out"), "wechat:dong_zzc:9002")
	if code == 0 {
		t.Fatal("WXGF 在没有已验收解码器时不应伪装为可预览图片")
	}
	details := failure["error"].(map[string]any)["details"].(map[string]any)
	hint := failure["error"].(map[string]any)["hint"].(string)
	if details["local_resolution_status"] != "decoder_unavailable" || details["detected_format"] != "wxgf" ||
		details["quality_tier"] != "medium" || details["recovery_action"] != "do_not_request_redownload_same_candidate" || details["network_access_performed"] != false {
		t.Fatalf("WXGF 诊断异常：%v", details)
	}
	if !strings.Contains(hint, "不会检查 PATH") || !strings.Contains(hint, "资格测试成功也不会自动启用导出") {
		t.Fatalf("WXGF 提示没有区分系统二进制与公共接线状态：%q", hint)
	}
	if details["quality_basis"] != "hardlink_cache_filename_variant" || details["quality_claim_scope"] != "wechat_cache_variant_only" ||
		details["source_original_dimensions_known"] != false {
		t.Fatalf("WXGF 错误路径扩大了图片质量声明：%v", details)
	}
	assertWXGFDecoderDiagnostics(t, details["decoder_diagnostics"])
	meta := failure["meta"].(map[string]any)
	if meta["generation_id"] != "generation-wxgf" || meta["snapshot_manifest_sha256"] != "manifest-wxgf" {
		t.Fatalf("WXGF 错误路径未绑定到解析它的快照：%v", meta)
	}
}

func TestExportChatImageUsesExactHardlinkNameInsteadOfStemVariant(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	stem := strings.Repeat("a", 32)
	variantPath := filepath.Join(accountPath, "msg", "attach", "another-session", stem+"_t.dat")
	if err := os.MkdirAll(filepath.Dir(variantPath), 0o700); err != nil {
		t.Fatal(err)
	}
	var variant bytes.Buffer
	if err := png.Encode(&variant, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(variantPath, variant.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-exact-name")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "exact-name-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-exact-name", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "exact-name.png")
	code, _, failure := runForTest("export-chat-image", "--account", "exact-name-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code != 0 {
		t.Fatalf("精确 hardlink 文件名未消除 stem 变体歧义：code=%d failure=%v", code, failure)
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("精确 hardlink 文件名导出内容异常：bytes=%d err=%v", len(exported), err)
	}
}

func TestExportChatImageMatchesMixedCaseHardlinkMD5(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, expected := createChatImageExportFixture(t)
	database, err := sql.Open("sqlite", filepath.Join(snapshot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	mixed := strings.Repeat("bB", 16)
	if _, err := database.Exec("UPDATE image_hardlink_info_v4 SET md5=?", mixed); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-mixed-md5")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "mixed-md5-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-mixed-md5", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "mixed-md5.png")
	code, _, failure := runForTest("export-chat-image", "--account", "mixed-md5-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code != 0 {
		t.Fatalf("混合大小写 MD5 未精确匹配：code=%d failure=%v", code, failure)
	}
	exported, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(exported, expected) {
		t.Fatalf("混合大小写 MD5 导出内容异常：bytes=%d err=%v", len(exported), err)
	}
}

func TestExportChatImageRejectsMessageDescriptorMismatch(t *testing.T) {
	home := testHome(t)
	t.Setenv("V_LOCAL_CLI_HOME", home)
	snapshot, accountPath, _ := createChatImageExportFixture(t)
	database, err := sql.Open("sqlite", filepath.Join(snapshot, "hardlink", "hardlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE image_hardlink_info_v4 SET md5=?", strings.Repeat("c", 32)); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	accountID := state.AccountID("chat-image-descriptor-mismatch")
	snapshot = privateTestSnapshot(t, home, accountID, snapshot)
	initialized := state.AccountState{
		AccountID: accountID, AccountName: "descriptor-mismatch-test", AccountPath: accountPath,
		SnapshotPath: snapshot, GenerationID: "generation-descriptor-mismatch", Storage: "snapshot-only",
	}
	if err := state.Save(&initialized); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "must-not-exist.png")
	code, _, failure := runForTest("export-chat-image", "--account", "descriptor-mismatch-test", "--output", outputPath, "wechat:dong_zzc:9002")
	if code == 0 || failure["error"].(map[string]any)["type"] != "chat_image_unavailable" {
		t.Fatalf("消息描述符不匹配仍选择了图片：code=%d failure=%v", code, failure)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("消息描述符不匹配仍产生了输出：err=%v", err)
	}
}
