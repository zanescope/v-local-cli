package messageindex

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zanescope/v-local-cli/internal/state"
	"github.com/zanescope/v-local-cli/internal/store"
	_ "modernc.org/sqlite"
)

const (
	SchemaVersion = 1
	ParserVersion = 1
)

type Manifest struct {
	SchemaVersion          int                       `json:"schema_version"`
	ParserVersion          int                       `json:"parser_version"`
	AccountID              string                    `json:"account_id"`
	GenerationID           string                    `json:"generation_id"`
	SnapshotManifestSHA256 string                    `json:"snapshot_manifest_sha256"`
	CreatedAt              string                    `json:"created_at"`
	DocumentCount          int                       `json:"document_count"`
	FTSMode                string                    `json:"fts_mode"`
	Coverage               store.MessageScanCoverage `json:"message_coverage"`
}

type BuildReport struct {
	Status   string   `json:"status"`
	Manifest Manifest `json:"manifest"`
	Path     string   `json:"path,omitempty"`
}

type Status struct {
	Available bool      `json:"index_present"`
	Valid     bool      `json:"valid"`
	Reason    string    `json:"reason,omitempty"`
	Manifest  *Manifest `json:"manifest,omitempty"`
}

type SearchReport struct {
	Items    []store.Message `json:"items"`
	Coverage map[string]any  `json:"search_backend_status"`
}

type Position struct {
	Timestamp  int64  `json:"timestamp"`
	SortKey    int64  `json:"sort_key"`
	EvidenceID string `json:"evidence_id"`
}

type Change struct {
	Kind    string        `json:"change_kind"`
	Message store.Message `json:"message"`
}

type DiffReport struct {
	Items    []Change `json:"items"`
	Next     Position `json:"next_position"`
	HasMore  bool     `json:"has_more"`
	Complete bool     `json:"complete"`
}

type GarbageCollectionReport struct {
	RemovedGenerations int                 `json:"removed_generations"`
	RemovedStaging     int                 `json:"removed_staging"`
	ReclaimedBytes     int64               `json:"reclaimed_bytes"`
	Pinned             map[string][]string `json:"pinned,omitempty"`
	DryRun             bool                `json:"dry_run"`
}

func validGenerationID(value string) bool {
	return value != "" && value == filepath.Base(value) && value != "." && value != ".." && !strings.ContainsAny(value, `/\`)
}

func GenerationDir(accountID, generationID string) (string, error) {
	if !validGenerationID(generationID) {
		return "", errors.New("generation 标识无效")
	}
	root, err := state.DerivedRoot(accountID)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, generationID)
	if err := state.ValidatePrivateTarget(directory, true); err != nil {
		return "", err
	}
	return directory, nil
}

func DatabasePath(accountID, generationID string) (string, error) {
	directory, err := GenerationDir(accountID, generationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "message-index.sqlite"), nil
}

func ManifestPath(accountID, generationID string) (string, error) {
	directory, err := GenerationDir(accountID, generationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "index-manifest.json"), nil
}

func randomSuffix() (string, error) {
	payload := make([]byte, 8)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func sqliteURL(path string, queryOnly bool) string {
	absolute, _ := filepath.Abs(path)
	urlPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	value := &url.URL{Scheme: "file", Path: urlPath}
	query := "?mode=ro&immutable=1"
	if queryOnly {
		query += "&_pragma=query_only(1)"
	}
	return value.String() + query
}

func openReadOnly(path string) (*sql.DB, error) {
	if err := state.ValidatePrivateTarget(path, false); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", sqliteURL(path, true))
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func textualDetails(value any, parent string, output *[]string) {
	switch current := value.(type) {
	case string:
		name := strings.ToLower(parent)
		compactName := strings.NewReplacer("_", "", "-", "", ".", "").Replace(name)
		if strings.Contains(compactName, "token") || strings.Contains(compactName, "secret") ||
			strings.Contains(compactName, "password") || strings.Contains(compactName, "passwd") ||
			strings.Contains(compactName, "credential") || strings.Contains(compactName, "cookie") ||
			strings.Contains(compactName, "authorization") || strings.HasSuffix(compactName, "key") {
			return
		}
		if trimmed := strings.TrimSpace(current); trimmed != "" {
			*output = append(*output, trimmed)
		}
	case []string:
		for _, item := range current {
			textualDetails(item, parent, output)
		}
	case []any:
		for _, item := range current {
			textualDetails(item, parent, output)
		}
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			textualDetails(current[key], key, output)
		}
	}
}

func searchText(message store.Message) string {
	parts := []string{
		message.Content, message.Sender, message.SenderUsername, message.SenderNickname,
		message.SenderRemark, message.SenderContactDisplay, message.SenderGroupNickname,
		message.TypeLabel, message.VoiceTranscript,
	}
	parts = append(parts, message.Mentions...)
	if message.ReplyTo != nil {
		parts = append(parts, message.ReplyTo.ToName, message.ReplyTo.Quoted)
	}
	textualDetails(message.Details, "details", &parts)
	seen := map[string]bool{}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToValidUTF8(part, "�"))
		if part != "" && !seen[part] {
			seen[part] = true
			result = append(result, part)
		}
	}
	return strings.Join(result, "\n")
}

func createFTS(transaction *sql.Tx) string {
	queries := []struct {
		mode string
		sql  string
	}{
		{"trigram", "CREATE VIRTUAL TABLE documents_fts USING fts5(evidence_id UNINDEXED, search_text, chat, sender, tokenize='trigram')"},
		{"unicode61", "CREATE VIRTUAL TABLE documents_fts USING fts5(evidence_id UNINDEXED, search_text, chat, sender, tokenize='unicode61')"},
	}
	for _, candidate := range queries {
		if _, err := transaction.Exec(candidate.sql); err == nil {
			return candidate.mode
		}
	}
	return "like"
}

func Build(account state.AccountState, force bool) (BuildReport, error) {
	if account.GenerationID == "" || account.SnapshotPath == "" {
		return BuildReport{}, errors.New("账号没有可索引的 generation")
	}
	// 已验证索引是该 generation 的不可变派生物。--force 只用于替换版本或
	// 绑定无效的旧索引，不能重写一个仍然有效的 generation。
	if status, _ := Inspect(account); status.Valid {
		manifest := *status.Manifest
		path, _ := DatabasePath(account.AccountID, account.GenerationID)
		return BuildReport{Status: "ready", Manifest: manifest, Path: path}, nil
	}
	root, err := state.DerivedRoot(account.AccountID)
	if err != nil {
		return BuildReport{}, err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return BuildReport{}, err
	}
	stage := filepath.Join(root, ".stage-"+account.GenerationID+"-"+suffix)
	if err := os.Mkdir(stage, 0o700); err != nil {
		return BuildReport{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	databasePath := filepath.Join(stage, "message-index.sqlite")
	file, err := os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return BuildReport{}, err
	}
	if err := file.Close(); err != nil {
		return BuildReport{}, err
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return BuildReport{}, err
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec("PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL"); err != nil {
		_ = database.Close()
		return BuildReport{}, err
	}
	transaction, err := database.Begin()
	if err != nil {
		_ = database.Close()
		return BuildReport{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = transaction.Rollback()
		}
		_ = database.Close()
	}()
	if _, err := transaction.Exec("CREATE TABLE metadata(key TEXT PRIMARY KEY, value TEXT NOT NULL);" +
		"CREATE TABLE documents(" +
		"evidence_id TEXT PRIMARY KEY,chat TEXT NOT NULL,timestamp INTEGER NOT NULL,sort_key INTEGER NOT NULL," +
		"kind TEXT NOT NULL,sender TEXT NOT NULL,source_db TEXT NOT NULL,content_sha256 TEXT NOT NULL," +
		"normalized_text TEXT NOT NULL,payload_json BLOB NOT NULL);" +
		"CREATE INDEX documents_position ON documents(timestamp,sort_key,evidence_id);" +
		"CREATE INDEX documents_chat_position ON documents(chat,timestamp,sort_key,evidence_id);"); err != nil {
		return BuildReport{}, err
	}
	ftsMode := createFTS(transaction)
	insertDocument, err := transaction.Prepare("INSERT OR REPLACE INTO documents(evidence_id,chat,timestamp,sort_key,kind,sender,source_db,content_sha256,normalized_text,payload_json) VALUES(?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return BuildReport{}, err
	}
	defer insertDocument.Close()
	var documentCount int
	coverage, err := store.WalkMessages(account.SnapshotPath, func(message store.Message) error {
		payload, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			return marshalErr
		}
		// 来源分片可能在 refresh 后重排；它属于证据定位信息，不应仅因物理
		// 存储位置变化就把同一条消息报告为 updated。
		semanticMessage := message
		semanticMessage.SourceDB = ""
		semanticPayload, marshalErr := json.Marshal(semanticMessage)
		if marshalErr != nil {
			return marshalErr
		}
		digest := sha256.Sum256(semanticPayload)
		text := searchText(message)
		_, execErr := insertDocument.Exec(
			message.EvidenceID, message.Chat, message.Timestamp, message.SortKey, message.Kind, message.Sender,
			message.SourceDB, hex.EncodeToString(digest[:]), strings.ToLower(text), payload,
		)
		if execErr != nil {
			return execErr
		}
		return nil
	})
	if err != nil {
		return BuildReport{}, err
	}
	if ftsMode != "like" {
		if _, err := transaction.Exec("INSERT INTO documents_fts(evidence_id,search_text,chat,sender) SELECT evidence_id,normalized_text,chat,sender FROM documents"); err != nil {
			return BuildReport{}, err
		}
	}
	metadata := map[string]string{
		"schema_version": fmt.Sprint(SchemaVersion), "parser_version": fmt.Sprint(ParserVersion),
		"account_id": account.AccountID, "generation_id": account.GenerationID,
		"snapshot_manifest_sha256": account.SnapshotManifestSHA256, "fts_mode": ftsMode,
	}
	for key, value := range metadata {
		if _, err := transaction.Exec("INSERT INTO metadata(key,value) VALUES(?,?)", key, value); err != nil {
			return BuildReport{}, err
		}
	}
	if err := transaction.QueryRow("SELECT COUNT(*) FROM documents").Scan(&documentCount); err != nil {
		return BuildReport{}, err
	}
	if err := transaction.Commit(); err != nil {
		return BuildReport{}, err
	}
	rollback = false
	if err := database.Close(); err != nil {
		return BuildReport{}, err
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion, ParserVersion: ParserVersion, AccountID: account.AccountID,
		GenerationID: account.GenerationID, SnapshotManifestSHA256: account.SnapshotManifestSHA256,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DocumentCount: documentCount,
		FTSMode: ftsMode, Coverage: coverage,
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BuildReport{}, err
	}
	if err := os.WriteFile(filepath.Join(stage, "index-manifest.json"), append(payload, '\n'), 0o600); err != nil {
		return BuildReport{}, err
	}
	final, err := GenerationDir(account.AccountID, account.GenerationID)
	if err != nil {
		return BuildReport{}, err
	}
	if _, statErr := os.Lstat(final); statErr == nil {
		if !force {
			return BuildReport{}, errors.New("目标 generation 索引已经存在")
		}
		backup := final + ".old-" + suffix
		if err := os.Rename(final, backup); err != nil {
			return BuildReport{}, err
		}
		if err := os.Rename(stage, final); err != nil {
			_ = os.Rename(backup, final)
			return BuildReport{}, err
		}
		_ = os.RemoveAll(backup)
	} else if os.IsNotExist(statErr) {
		if err := os.Rename(stage, final); err != nil {
			return BuildReport{}, err
		}
	} else {
		return BuildReport{}, statErr
	}
	published = true
	return BuildReport{Status: "built", Manifest: manifest, Path: filepath.Join(final, "message-index.sqlite")}, nil
}

func inspectGeneration(accountID, generationID, snapshotManifestSHA256 string) (Status, error) {
	if generationID == "" {
		return Status{Reason: "generation_missing"}, nil
	}
	manifestPath, err := ManifestPath(accountID, generationID)
	if err != nil {
		return Status{}, err
	}
	if err := state.ValidatePrivateTarget(manifestPath, false); err != nil {
		if os.IsNotExist(err) {
			return Status{Reason: "index_missing"}, nil
		}
		return Status{}, err
	}
	payload, err := os.ReadFile(manifestPath)
	if os.IsNotExist(err) {
		return Status{Reason: "index_missing"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	status := Status{Available: true}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		status.Reason = "manifest_invalid"
		return status, nil
	}
	status.Manifest = &manifest
	if manifest.SchemaVersion != SchemaVersion || manifest.ParserVersion != ParserVersion {
		status.Reason = "index_version_mismatch"
		return status, nil
	}
	if manifest.AccountID != accountID || manifest.GenerationID != generationID ||
		(snapshotManifestSHA256 != "" && manifest.SnapshotManifestSHA256 != snapshotManifestSHA256) {
		status.Reason = "generation_binding_mismatch"
		return status, nil
	}
	databasePath, _ := DatabasePath(accountID, generationID)
	database, openErr := openReadOnly(databasePath)
	if openErr != nil {
		status.Reason = "database_invalid"
		return status, nil
	}
	defer database.Close()
	var integrity string
	if database.QueryRow("PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		status.Reason = "integrity_check_failed"
		return status, nil
	}
	expectedMetadata := map[string]string{
		"schema_version": fmt.Sprint(SchemaVersion), "parser_version": fmt.Sprint(ParserVersion),
		"account_id": accountID, "generation_id": generationID,
		"snapshot_manifest_sha256": manifest.SnapshotManifestSHA256, "fts_mode": manifest.FTSMode,
	}
	for key, expected := range expectedMetadata {
		var actual string
		if database.QueryRow("SELECT value FROM metadata WHERE key=?", key).Scan(&actual) != nil || actual != expected {
			status.Reason = "database_binding_mismatch"
			return status, nil
		}
	}
	var documentCount int
	if database.QueryRow("SELECT COUNT(*) FROM documents").Scan(&documentCount) != nil || documentCount != manifest.DocumentCount {
		status.Reason = "document_count_mismatch"
		return status, nil
	}
	status.Valid = true
	return status, nil
}

func Inspect(account state.AccountState) (Status, error) {
	return inspectGeneration(account.AccountID, account.GenerationID, account.SnapshotManifestSHA256)
}

// InspectGeneration 验证历史 generation 的 manifest、内部数据库绑定、完整性和
// 文档计数。原始 snapshot 可能已由 GC 清理，因此历史代际以索引 manifest 中绑定的
// snapshot 摘要为准；当前 generation 应使用 Inspect 额外核对 state 摘要。
func InspectGeneration(accountID, generationID string) (Status, error) {
	return inspectGeneration(accountID, generationID, "")
}

func phraseQuery(keyword string) string {
	return `"` + strings.ReplaceAll(keyword, `"`, `""`) + `"`
}

func Search(account state.AccountState, keyword, chat string, start, end *int64, limit int) (SearchReport, error) {
	status, err := Inspect(account)
	if err != nil {
		return SearchReport{}, err
	}
	if !status.Valid || status.Manifest == nil {
		return SearchReport{Coverage: map[string]any{"backend": "generation_index", "index_present": status.Available, "index_valid": false, "message_coverage_status": "unknown", "reason": status.Reason}}, nil
	}
	path, _ := DatabasePath(account.AccountID, account.GenerationID)
	database, err := openReadOnly(path)
	if err != nil {
		return SearchReport{}, err
	}
	defer database.Close()
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return SearchReport{}, errors.New("搜索关键词不能为空")
	}
	arguments := []any{}
	conditions := []string{}
	from := "documents d"
	useFTS := status.Manifest.FTSMode == "trigram" && len([]rune(keyword)) >= 3
	if useFTS {
		from += " JOIN documents_fts f ON f.evidence_id=d.evidence_id"
		conditions = append(conditions, "documents_fts MATCH ?")
		arguments = append(arguments, phraseQuery(keyword))
	} else {
		conditions = append(conditions, "d.normalized_text LIKE ? ESCAPE '\\'")
		escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(keyword)
		arguments = append(arguments, "%"+escaped+"%")
	}
	if chat != "" {
		conditions = append(conditions, "d.chat=?")
		arguments = append(arguments, chat)
	}
	if start != nil {
		conditions = append(conditions, "d.timestamp>=?")
		arguments = append(arguments, *start)
	}
	if end != nil {
		conditions = append(conditions, "d.timestamp<=?")
		arguments = append(arguments, *end)
	}
	query := "SELECT d.payload_json FROM " + from + " WHERE " + strings.Join(conditions, " AND ") + " ORDER BY d.timestamp DESC,d.sort_key DESC,d.evidence_id DESC"
	if limit > 0 {
		query += " LIMIT ?"
		arguments = append(arguments, limit)
	}
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return SearchReport{}, err
	}
	defer rows.Close()
	searchBackend := "like"
	if useFTS {
		searchBackend = "fts5_trigram"
	}
	messageCoverageStatus := "partial"
	if status.Manifest.Coverage.Complete {
		messageCoverageStatus = "complete"
	}
	report := SearchReport{Coverage: map[string]any{
		"backend": "generation_index", "index_present": true, "index_valid": true, "message_coverage_status": messageCoverageStatus,
		"fts_mode": status.Manifest.FTSMode, "search_backend": searchBackend, "document_count": status.Manifest.DocumentCount,
	}}
	for rows.Next() {
		var payload []byte
		if rows.Scan(&payload) != nil {
			continue
		}
		var message store.Message
		if json.Unmarshal(payload, &message) == nil {
			report.Items = append(report.Items, message)
		}
	}
	return report, rows.Err()
}

func Diff(currentPath, basePath string, after Position, limit int) (DiffReport, error) {
	if limit < 1 {
		return DiffReport{}, errors.New("增量批次大小必须大于零")
	}
	if err := state.ValidatePrivateTarget(currentPath, false); err != nil {
		return DiffReport{}, err
	}
	if basePath != "" {
		if err := state.ValidatePrivateTarget(basePath, false); err != nil {
			return DiffReport{}, err
		}
	}
	database, err := sql.Open("sqlite", sqliteURL(currentPath, false))
	if err != nil {
		return DiffReport{}, err
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	from := "documents d"
	changeExpression := "'inserted'"
	conditions := []string{}
	arguments := []any{}
	if basePath != "" {
		if _, err := database.Exec("ATTACH DATABASE ? AS previous", sqliteURL(basePath, false)); err != nil {
			return DiffReport{}, err
		}
		from += " LEFT JOIN previous.documents p ON p.evidence_id=d.evidence_id"
		conditions = append(conditions, "(p.evidence_id IS NULL OR p.content_sha256<>d.content_sha256)")
		changeExpression = "CASE WHEN p.evidence_id IS NULL THEN 'inserted' ELSE 'updated' END"
	}
	if after.EvidenceID != "" {
		conditions = append(conditions, "(d.timestamp>? OR (d.timestamp=? AND d.sort_key>?) OR (d.timestamp=? AND d.sort_key=? AND d.evidence_id>?))")
		arguments = append(arguments, after.Timestamp, after.Timestamp, after.SortKey, after.Timestamp, after.SortKey, after.EvidenceID)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	query := "SELECT " + changeExpression + ",d.timestamp,d.sort_key,d.evidence_id,d.payload_json FROM " + from + where + " ORDER BY d.timestamp ASC,d.sort_key ASC,d.evidence_id ASC LIMIT ?"
	arguments = append(arguments, limit+1)
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return DiffReport{}, err
	}
	defer rows.Close()
	report := DiffReport{Complete: true}
	for rows.Next() {
		var kind, evidenceID string
		var timestamp, sortKey int64
		var payload []byte
		if rows.Scan(&kind, &timestamp, &sortKey, &evidenceID, &payload) != nil {
			continue
		}
		if len(report.Items) == limit {
			report.HasMore = true
			break
		}
		var message store.Message
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		report.Items = append(report.Items, Change{Kind: kind, Message: message})
		report.Next = Position{Timestamp: timestamp, SortKey: sortKey, EvidenceID: evidenceID}
	}
	return report, rows.Err()
}

func directoryBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() {
			if info, infoErr := entry.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func GarbageCollect(accountID, currentGeneration string, retainPrevious int, pinned map[string][]string, dryRun bool) (GarbageCollectionReport, error) {
	report := GarbageCollectionReport{Pinned: pinned, DryRun: dryRun}
	root, err := state.DerivedRoot(accountID)
	if err != nil {
		return report, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return report, err
	}
	var generations []string
	var staging []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if strings.HasPrefix(entry.Name(), ".stage-") || strings.Contains(entry.Name(), ".old-") {
			staging = append(staging, path)
			continue
		}
		if entry.Name() == currentGeneration || len(pinned[entry.Name()]) > 0 || !validGenerationID(entry.Name()) {
			continue
		}
		generations = append(generations, path)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(generations)))
	if retainPrevious < 0 {
		retainPrevious = 0
	}
	if len(generations) > retainPrevious {
		generations = generations[retainPrevious:]
	} else {
		generations = nil
	}
	for _, path := range staging {
		if err := state.ValidatePrivateTarget(path, true); err != nil {
			return report, err
		}
		report.RemovedStaging++
		report.ReclaimedBytes += directoryBytes(path)
		if !dryRun {
			if err := os.RemoveAll(path); err != nil {
				return report, err
			}
		}
	}
	for _, path := range generations {
		if err := state.ValidatePrivateTarget(path, true); err != nil {
			return report, err
		}
		report.RemovedGenerations++
		report.ReclaimedBytes += directoryBytes(path)
		if !dryRun {
			if err := os.RemoveAll(path); err != nil {
				return report, err
			}
		}
	}
	return report, nil
}
