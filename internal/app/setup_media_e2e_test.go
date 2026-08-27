package app

import (
	"bytes"
	"crypto/aes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"
)

func writeProviderFixture(t *testing.T, aesKey string, xorKey int, databasePath string) string {
	t.Helper()
	t.Setenv("V_LOCAL_TEST_AES", aesKey)
	t.Setenv("V_LOCAL_TEST_XOR", strconv.Itoa(xorKey))
	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := testCatalogFileIdentity(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	page := make([]byte, 4096)
	read, readErr := file.Read(page)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || read < 16 {
		t.Fatalf("cannot build catalog proof: read=%d readErr=%v closeErr=%v", read, readErr, closeErr)
	}
	digest := sha256.Sum256(page[:read])
	t.Setenv("V_LOCAL_TEST_FILE_ID", identity)
	t.Setenv("V_LOCAL_TEST_FILE_SIZE", strconv.FormatInt(info.Size(), 10))
	t.Setenv("V_LOCAL_TEST_FILE_MTIME", strconv.FormatInt(info.ModTime().UnixNano(), 10))
	t.Setenv("V_LOCAL_TEST_FIRST_PAGE", fmt.Sprintf("%x", digest[:]))
	directory := t.TempDir()
	if runtime.GOOS == "windows" {
		providerPath := filepath.Join(directory, "mock-provider.cmd")
		powerShellPath := filepath.Join(directory, "mock-provider.ps1")
		const powerShell = `$ErrorActionPreference = 'Stop'
$request = [Console]::In.ReadToEnd() | ConvertFrom-Json
$response = [ordered]@{
  protocol = 'v-local-key-provider/v1'
  request_id = $request.request_id
  catalog_id = 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee'
  catalog_entries = @([ordered]@{database_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';relative_path='contact/contact.db';canonical_file_id=$env:V_LOCAL_TEST_FILE_ID;size=[int64]$env:V_LOCAL_TEST_FILE_SIZE;mtime_ns=[int64]$env:V_LOCAL_TEST_FILE_MTIME;first_page_sha256=$env:V_LOCAL_TEST_FIRST_PAGE;classification='plaintext';required_for_key_coverage=$false})
  database_keys = [ordered]@{}
  image_keys = [ordered]@{aes = $env:V_LOCAL_TEST_AES; xor = [int]$env:V_LOCAL_TEST_XOR}
  diagnostics = [ordered]@{
    result_code='complete';workflow_status='terminal';requested_scopes=@('database','media')
    database_target_status='present';database_coverage_status='complete';media_coverage_status='complete'
    security_posture_status='not_applicable';shadow_route_status='not_applicable';route_priority=@();routes_attempted=@()
    next_action='none';target_binding_status='unknown';session_account_status='unknown';candidate_mode='none';candidate_sources=@();blocking_reasons=@()
    platform='windows';binary_fingerprint_status='unavailable';binary_signing_status='unavailable'
    process_architecture='unknown';process_architecture_status='unavailable';process_translation_status='not_applicable'
    compatibility_registry_status='not_evaluated';config_cipher_route_status='not_evaluated';windows_route_evidence=@()
    process_count=0;selected_process_count=0;target_bound_process_count=0;other_account_process_count=0;unknown_account_process_count=0
    opened_process_count=0;access_denied_count=0;per_process_collector_count=0
    config_cipher_structure_count=0;config_cipher_invalid_structure_count=0;config_cipher_candidate_count=0;config_cipher_verified_candidate_count=0
    static_scan_fallback=$false;fallback_candidate_count=0;fallback_stage_counts=[ordered]@{}
  }
}
[Console]::Out.WriteLine(($response | ConvertTo-Json -Compress -Depth 4))
`
		if err := os.WriteFile(powerShellPath, []byte(powerShell), 0o600); err != nil {
			t.Fatal(err)
		}
		const wrapper = "@echo off\r\npowershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"%~dp0mock-provider.ps1\"\r\n"
		if err := os.WriteFile(providerPath, []byte(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		return providerPath
	}
	providerPath := filepath.Join(directory, "mock-provider")
	// macOS 在跨越 Provider 进程边界前会主动清除调用方的环境变量。把 fixture
	// 中合成且不含秘密的证据嵌入脚本，让该测试覆盖生产环境的隔离行为，而不依赖
	// 继承 V_LOCAL_TEST_* 环境变量。
	script := "#!/bin/sh\n" +
		"V_LOCAL_TEST_FILE_ID='" + identity + "'\n" +
		"V_LOCAL_TEST_FILE_SIZE='" + strconv.FormatInt(info.Size(), 10) + "'\n" +
		"V_LOCAL_TEST_FILE_MTIME='" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "'\n" +
		"V_LOCAL_TEST_FIRST_PAGE='" + fmt.Sprintf("%x", digest[:]) + "'\n" +
		"V_LOCAL_TEST_AES='" + aesKey + "'\n" +
		"V_LOCAL_TEST_XOR='" + strconv.Itoa(xorKey) + "'\n" +
		"request_id=$(sed -n 's/.*\"request_id\":\"\\([0-9a-f]*\\)\".*/\\1/p')\n" +
		"printf '{\"protocol\":\"v-local-key-provider/v1\",\"request_id\":\"%s\",\"catalog_id\":\"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\",\"catalog_entries\":[{\"database_id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"relative_path\":\"contact/contact.db\",\"canonical_file_id\":\"%s\",\"size\":%s,\"mtime_ns\":%s,\"first_page_sha256\":\"%s\",\"classification\":\"plaintext\",\"required_for_key_coverage\":false}],\"database_keys\":{},\"image_keys\":{\"aes\":\"%s\",\"xor\":%s},\"diagnostics\":{\"result_code\":\"complete\",\"workflow_status\":\"terminal\",\"requested_scopes\":[\"database\",\"media\"],\"database_target_status\":\"present\",\"database_coverage_status\":\"complete\",\"media_coverage_status\":\"complete\",\"security_posture_status\":\"not_applicable\",\"shadow_route_status\":\"not_applicable\",\"route_priority\":[],\"routes_attempted\":[],\"next_action\":\"none\",\"target_binding_status\":\"unknown\",\"session_account_status\":\"unknown\",\"candidate_mode\":\"none\",\"candidate_sources\":[],\"blocking_reasons\":[],\"platform\":\"windows\",\"binary_fingerprint_status\":\"unavailable\",\"binary_signing_status\":\"unavailable\",\"process_architecture\":\"unknown\",\"process_architecture_status\":\"unavailable\",\"process_translation_status\":\"not_applicable\",\"compatibility_registry_status\":\"not_evaluated\",\"config_cipher_route_status\":\"not_evaluated\",\"windows_route_evidence\":[],\"process_count\":0,\"selected_process_count\":0,\"target_bound_process_count\":0,\"other_account_process_count\":0,\"unknown_account_process_count\":0,\"opened_process_count\":0,\"access_denied_count\":0,\"per_process_collector_count\":0,\"config_cipher_structure_count\":0,\"config_cipher_invalid_structure_count\":0,\"config_cipher_candidate_count\":0,\"config_cipher_verified_candidate_count\":0,\"static_scan_fallback\":false,\"fallback_candidate_count\":0,\"fallback_stage_counts\":{}}}\\n' \"$request_id\" \"$V_LOCAL_TEST_FILE_ID\" \"$V_LOCAL_TEST_FILE_SIZE\" \"$V_LOCAL_TEST_FILE_MTIME\" \"$V_LOCAL_TEST_FIRST_PAGE\" \"$V_LOCAL_TEST_AES\" \"$V_LOCAL_TEST_XOR\"\n"
	if err := os.WriteFile(providerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return providerPath
}

func writeSyntheticV2DAT(t *testing.T, path string, plain []byte, aesKey string, xorKey byte) {
	t.Helper()
	const aesSize = 2
	block, err := aes.NewCipher([]byte(aesKey))
	if err != nil {
		t.Fatal(err)
	}
	aesPlain := append([]byte(nil), plain[:aesSize]...)
	padding := aes.BlockSize - len(aesPlain)%aes.BlockSize
	aesPlain = append(aesPlain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	aesCipher := make([]byte, len(aesPlain))
	for offset := 0; offset < len(aesPlain); offset += aes.BlockSize {
		block.Encrypt(aesCipher[offset:offset+aes.BlockSize], aesPlain[offset:offset+aes.BlockSize])
	}
	xorPlain := plain[aesSize:]
	xorCipher := make([]byte, len(xorPlain))
	for index, value := range xorPlain {
		xorCipher[index] = value ^ xorKey
	}
	header := make([]byte, 15)
	copy(header, []byte{0x07, 0x08, 0x56, 0x32, 0x08, 0x07})
	binary.LittleEndian.PutUint32(header[6:10], aesSize)
	binary.LittleEndian.PutUint32(header[10:14], uint32(len(xorCipher)))
	data := append(header, aesCipher...)
	data = append(data, xorCipher...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func syntheticPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 2, 2))
	canvas.Set(0, 0, color.RGBA{255, 0, 0, 255})
	canvas.Set(1, 0, color.RGBA{0, 255, 0, 255})
	canvas.Set(0, 1, color.RGBA{0, 0, 255, 255})
	canvas.Set(1, 1, color.RGBA{255, 255, 0, 255})
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestSetupProviderKeychainRefreshAndExportMedia(t *testing.T) {
	account := t.TempDir()
	databaseDirectory := filepath.Join(account, "db_storage", "contact")
	if err := os.MkdirAll(databaseDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(databaseDirectory, "contact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT); INSERT INTO contact VALUES('alice','','阿丽','Alice')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	plain := syntheticPNG(t)
	aesKey := "0123456789abcdef"
	xorKey := byte(90)
	datPath := filepath.Join(account, "cache", "image.dat")
	if err := os.MkdirAll(filepath.Dir(datPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSyntheticV2DAT(t, datPath, plain, aesKey, xorKey)
	providerPath := writeProviderFixture(t, aesKey, int(xorKey), filepath.Join(databaseDirectory, "contact.db"))

	t.Setenv("V_LOCAL_CLI_ACCOUNT_DIR", account)
	t.Setenv("V_LOCAL_CLI_HOME", testHome(t))
	code, output, errorOutput := runForTest("setup", "--allow-key-access", "--provider", providerPath, "--storage", "keychain")
	if code != 0 {
		t.Fatalf("provider setup failed: code=%d output=%v error=%v", code, output, errorOutput)
	}
	setupData := output["data"].(map[string]any)
	media := setupData["media"].(map[string]any)
	if setupData["status"] != "ready" || media["status"] != "verified" || setupData["secrets_persisted"] != true || setupData["database_keys_persisted"] != false || setupData["image_keys_persisted"] != true {
		t.Fatalf("provider setup did not persist verified media secrets: %v", setupData)
	}

	code, output, errorOutput = runForTest("refresh", "--require-media")
	if code != 0 {
		t.Fatalf("refresh failed: code=%d output=%v error=%v", code, output, errorOutput)
	}
	refreshData := output["data"].(map[string]any)
	if refreshData["credential_source"] != "saved_keychain" || refreshData["process_access_performed"] != false || refreshData["secrets_persisted"] != false {
		t.Fatalf("refresh crossed provider boundary: %v", refreshData)
	}

	outputPath := filepath.Join(t.TempDir(), "decoded.png")
	code, output, errorOutput = runForTest("export-media", "--output", outputPath, datPath)
	if code != 0 {
		t.Fatalf("media export failed: code=%d output=%v error=%v", code, output, errorOutput)
	}
	decoded, err := os.ReadFile(outputPath)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("exported image differs from source: bytes=%d err=%v", len(decoded), err)
	}
}
