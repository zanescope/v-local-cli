package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

type Contact struct {
	Username   string `json:"username"`
	Alias      string `json:"alias,omitempty"`
	Remark     string `json:"remark,omitempty"`
	Nickname   string `json:"nickname,omitempty"`
	Display    string `json:"display"`
	Kind       string `json:"kind"`
	VerifyFlag int64  `json:"verify_flag,omitempty"`
}

type Message struct {
	Chat                  string         `json:"chat"`
	LocalID               int64          `json:"local_id,omitempty"`
	ServerID              int64          `json:"server_id,omitempty"`
	LocalType             int64          `json:"local_type,omitempty"`
	BaseType              int64          `json:"base_type,omitempty"`
	SubType               int64          `json:"sub_type,omitempty"`
	TypeLabel             string         `json:"type_label,omitempty"`
	Timestamp             int64          `json:"timestamp,omitempty"`
	SortKey               int64          `json:"sort_key,omitempty"`
	Sender                string         `json:"sender,omitempty"`
	SenderUsername        string         `json:"sender_username,omitempty"`
	SenderNickname        string         `json:"sender_nickname,omitempty"`
	SenderRemark          string         `json:"sender_remark,omitempty"`
	SenderContactDisplay  string         `json:"sender_contact_display,omitempty"`
	SenderGroupNickname   string         `json:"sender_group_nickname,omitempty"`
	SenderIdentity        string         `json:"sender_identity"`
	IsFromMe              bool           `json:"is_from_me"`
	Kind                  string         `json:"kind"`
	IsSystem              bool           `json:"is_system,omitempty"`
	Content               string         `json:"content"`
	Details               map[string]any `json:"details,omitempty"`
	ReplyTo               *MessageReply  `json:"reply_to,omitempty"`
	Mentions              []string       `json:"mentions,omitempty"`
	VoiceDurationMS       *int64         `json:"voice_duration_ms,omitempty"`
	VoiceTranscript       string         `json:"voice_transcript,omitempty"`
	VoiceTranscriptSource string         `json:"voice_transcript_source,omitempty"`
	MediaMD5              string         `json:"media_md5,omitempty"`
	SourceDB              string         `json:"source_db"`
	EvidenceID            string         `json:"evidence_id"`
}

type MessageReply struct {
	ToName   string `json:"to_name,omitempty"`
	Quoted   string `json:"quoted,omitempty"`
	RefSvrID string `json:"ref_svrid,omitempty"`
	RefMD5   string `json:"ref_md5,omitempty"`
}

func sqliteFiles(root string) ([]string, error) {
	var values []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
			values = append(values, path)
		}
		return nil
	})
	sort.Strings(values)
	return values, err
}

func openReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	urlPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	value := &url.URL{Scheme: "file", Path: urlPath}
	database, err := sql.Open("sqlite", value.String()+"?mode=ro&immutable=1&_pragma=query_only(1)")
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

func tableExists(database *sql.DB, table string) bool {
	var found int
	return database.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&found) == nil
}

// quoteIdentifier 把表名或列名包成 SQLite 方括号标识符。名字来自 sqlite_master 与
// PRAGMA table_info，属于数据库自身内容而非参数，只能拼接、无法用占位符；因此必须
// 统一在这里转义右方括号，避免个别站点漏转义导致标识符越界。
func quoteIdentifier(name string) string {
	const openBracket, closeBracket = "[", "]"
	return openBracket + strings.ReplaceAll(name, closeBracket, closeBracket+closeBracket) + closeBracket
}

func columns(database *sql.DB, table string) map[string]bool {
	rows, err := database.Query("PRAGMA table_info(" + quoteIdentifier(table) + ")")
	if err != nil {
		return map[string]bool{}
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey) == nil {
			result[name] = true
		}
	}
	return result
}

func contactKind(username string) string {
	return contactKindWithVerify(username, 0)
}

func contactKindWithVerify(username string, verifyFlag int64) string {
	switch {
	case strings.HasSuffix(username, "@chatroom"):
		return "group"
	case verifyFlag != 0, strings.HasPrefix(username, "gh_"), strings.HasPrefix(username, "biz_"),
		strings.HasPrefix(username, "@"), username == "brandsessionholder":
		return "official"
	default:
		return "person"
	}
}

func Contacts(root, keyword string, limit int) ([]Contact, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, err
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	seen := map[string]bool{}
	var result []Contact
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "contact.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		if !tableExists(database, "contact") {
			_ = database.Close()
			continue
		}
		available := columns(database, "contact")
		wanted := []string{"username", "alias", "remark", "nick_name", "verify_flag"}
		selected := make([]string, 0, len(wanted))
		for _, name := range wanted {
			if available[name] {
				selected = append(selected, name)
			}
		}
		if !available["username"] {
			_ = database.Close()
			continue
		}
		rows, queryErr := database.Query("SELECT " + strings.Join(selected, ",") + " FROM contact")
		if queryErr != nil {
			_ = database.Close()
			continue
		}
		for rows.Next() {
			values := make([]any, len(selected))
			targets := make([]any, len(selected))
			for index := range values {
				targets[index] = &values[index]
			}
			if rows.Scan(targets...) != nil {
				continue
			}
			fields := map[string]string{}
			for index, name := range selected {
				fields[name] = asString(values[index])
			}
			username := fields["username"]
			if username == "" || seen[username] {
				continue
			}
			display := firstNonEmpty(fields["remark"], fields["nick_name"], username)
			haystack := strings.ToLower(strings.Join([]string{username, fields["alias"], fields["remark"], fields["nick_name"]}, "\n"))
			if keyword != "" && !strings.Contains(haystack, keyword) {
				continue
			}
			seen[username] = true
			verifyFlag := asInt64(fields["verify_flag"])
			result = append(result, Contact{
				Username: username, Alias: fields["alias"], Remark: fields["remark"],
				Nickname: fields["nick_name"], Display: display, Kind: contactKindWithVerify(username, verifyFlag), VerifyFlag: verifyFlag,
			})
		}
		_ = rows.Close()
		_ = database.Close()
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Display) < strings.ToLower(result[right].Display)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func messageTable(chat string) string {
	sum := md5.Sum([]byte(chat))
	return "Msg_" + hex.EncodeToString(sum[:])
}

func messageDatabase(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return (strings.HasPrefix(name, "message_") || strings.HasPrefix(name, "biz_message_")) &&
		!strings.Contains(name, "fts") && !strings.Contains(name, "resource")
}

// ChatExists 只检查当前不可变代际是否包含对应消息表，用于在联系人
// 数据缺失时保留稳定 username 的兼容查询路径。
func ChatExists(root, chat string) bool {
	if strings.TrimSpace(chat) == "" {
		return false
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return false
	}
	table := messageTable(chat)
	for _, path := range files {
		if !messageDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		found := tableExists(database, table)
		_ = database.Close()
		if found {
			return true
		}
	}
	return false
}

func name2ID(database *sql.DB) map[int64]string {
	result := map[int64]string{}
	if !tableExists(database, "Name2Id") {
		return result
	}
	rows, err := database.Query("SELECT rowid, user_name FROM Name2Id")
	if err != nil {
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var value any
		if rows.Scan(&id, &value) == nil {
			result[id] = decodeValue(value, 0)
		}
	}
	return result
}

func messageQuery(database *sql.DB, table, chat, source string, start, end *int64, limit int, identity messageIdentity) ([]Message, error) {
	var result []Message
	err := messageQueryEach(database, table, chat, source, start, end, limit, identity, func(message Message) error {
		result = append(result, message)
		return nil
	})
	return result, err
}

func messageQueryEach(database *sql.DB, table, chat, source string, start, end *int64, limit int, identity messageIdentity, emit func(Message) error) error {
	available := columns(database, table)
	if !available["message_content"] {
		return errors.New("消息表缺少 message_content")
	}
	if (start != nil || end != nil) && !available["create_time"] {
		return errors.New("消息表缺少 create_time，无法应用时间范围")
	}
	wanted := []string{"local_id", "server_id", "local_type", "sort_seq", "real_sender_id", "create_time", "status", "message_content", "compress_content", "WCDB_CT_message_content", "source"}
	selected := make([]string, 0, len(wanted))
	for _, name := range wanted {
		if available[name] {
			selected = append(selected, name)
		}
	}
	order := "rowid"
	if available["sort_seq"] {
		order = "sort_seq"
	} else if available["create_time"] {
		order = "create_time"
	}
	query := "SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier(table)
	conditions := []string{}
	arguments := []any{}
	if start != nil {
		conditions = append(conditions, "create_time >= ?")
		arguments = append(arguments, *start)
	}
	if end != nil {
		conditions = append(conditions, "create_time <= ?")
		arguments = append(arguments, *end)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY " + order + " DESC"
	if limit > 0 {
		query += " LIMIT " + strconv.Itoa(limit)
	}
	names := name2ID(database)
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(selected))
		targets := make([]any, len(selected))
		for index := range values {
			targets[index] = &values[index]
		}
		if rows.Scan(targets...) != nil {
			continue
		}
		fields := map[string]any{}
		for index, name := range selected {
			fields[name] = values[index]
		}
		localID := asInt64(fields["local_id"])
		serverID := asInt64(fields["server_id"])
		timestamp := asInt64(fields["create_time"])
		sortKey := asInt64(fields["sort_seq"])
		if sortKey == 0 {
			sortKey = timestamp * 1000
		}
		content := decodeValue(fields["message_content"], asInt64(fields["WCDB_CT_message_content"]))
		if strings.TrimSpace(content) == "" {
			content = decodeValue(fields["compress_content"], asInt64(fields["WCDB_CT_message_content"]))
		}
		localType := asInt64(fields["local_type"])
		prefixSender, content := parseSenderPrefix(content)
		parsed := parseMessageContent(localType, content, decodeValue(fields["source"], 0))
		evidenceSource := serverID
		if evidenceSource == 0 {
			evidenceSource = localID
		}
		sender := names[asInt64(fields["real_sender_id"])]
		if sender == "" {
			sender = prefixSender
		}
		message := Message{
			Chat: chat, LocalID: localID, ServerID: serverID,
			LocalType: localType, Timestamp: timestamp,
			BaseType: parsed.BaseType, SubType: parsed.SubType, TypeLabel: parsed.TypeLabel,
			SortKey: sortKey, Sender: sender, SenderUsername: sender, Kind: parsed.Kind, IsSystem: parsed.Kind == "system",
			Content: parsed.Content, Details: parsed.Details, ReplyTo: parsed.ReplyTo,
			Mentions: parsed.Mentions, VoiceDurationMS: parsed.VoiceDurationMS,
			MediaMD5: parsed.MediaMD5, SourceDB: source,
			EvidenceID: fmt.Sprintf("wechat:%s:%d", chat, evidenceSource),
		}
		identity.enrich(&message, asInt64(fields["status"]))
		if err := emit(message); err != nil {
			return err
		}
	}
	return rows.Err()
}

func History(root, chat string, limit int) ([]Message, error) {
	return HistoryWindow(root, chat, nil, nil, limit)
}

// HistoryWindow 读取指定会话在闭区间时间窗内的消息；空边界表示不限制该侧。
func HistoryWindow(root, chat string, start, end *int64, limit int) ([]Message, error) {
	messages, err := historyWindowWithIdentity(root, chat, start, end, limit, loadMessageIdentity(root, chat))
	if err != nil {
		return nil, err
	}
	enrichRedPacketMessages(messages, loadRedPacketIndex(root))
	return messages, nil
}

func Search(root, keyword, chat string, limit int) ([]Message, error) {
	return SearchWindow(root, keyword, chat, nil, nil, limit)
}

// SearchWindow 在指定闭区间时间窗内搜索已解码的消息正文。
func SearchWindow(root, keyword, chat string, start, end *int64, limit int) ([]Message, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return nil, errors.New("搜索关键词不能为空")
	}
	historyLimit := 5000
	perChatLimit := 1000
	if limit == 0 {
		historyLimit = 0
		perChatLimit = 0
	}
	if chat != "" {
		messages, err := HistoryWindow(root, chat, start, end, historyLimit)
		if err != nil {
			return nil, err
		}
		return filterMessages(messages, keyword, limit), nil
	}
	contactsByUsername := loadContactIdentity(root)
	redPackets := loadRedPacketIndex(root)
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return nil, err
	}
	var result []Message
	for _, contact := range contacts {
		identity := messageIdentity{contacts: contactsByUsername, groupNicknames: map[string]string{}}
		if strings.HasSuffix(contact.Username, "@chatroom") {
			identity.groupNicknames = loadGroupNicknames(root, contact.Username)
		}
		messages, historyErr := historyWindowWithIdentity(root, contact.Username, start, end, perChatLimit, identity)
		if historyErr != nil {
			continue
		}
		enrichRedPacketMessages(messages, redPackets)
		result = append(result, filterMessages(messages, keyword, 0)...)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].SortKey > result[right].SortKey })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func historyWindowWithIdentity(root, chat string, start, end *int64, limit int, identity messageIdentity) ([]Message, error) {
	if strings.TrimSpace(chat) == "" {
		return nil, errors.New("会话 username 不能为空")
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, err
	}
	table := messageTable(chat)
	var result []Message
	var lastQueryError error
	queried := false
	for _, path := range files {
		if !messageDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		if tableExists(database, table) {
			relative, _ := filepath.Rel(root, path)
			messages, queryErr := messageQuery(database, table, chat, filepath.ToSlash(relative), start, end, limit, identity)
			if queryErr == nil {
				queried = true
				result = append(result, messages...)
			} else {
				lastQueryError = queryErr
			}
		}
		_ = database.Close()
	}
	if !queried && lastQueryError != nil {
		return nil, lastQueryError
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].SortKey == result[right].SortKey {
			return result[left].EvidenceID > result[right].EvidenceID
		}
		return result[left].SortKey > result[right].SortKey
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func filterMessages(messages []Message, keyword string, limit int) []Message {
	result := make([]Message, 0)
	for _, message := range messages {
		if strings.Contains(messageSearchText(message), keyword) {
			result = append(result, message)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result
}

func historyWindowEach(root, chat string, start, end *int64, emit func(Message) error) error {
	return historyWindowEachWithRedPackets(root, chat, start, end, loadRedPacketIndex(root), emit)
}

func historyWindowEachWithRedPackets(root, chat string, start, end *int64, redPackets redPacketIndex, emit func(Message) error) error {
	if strings.TrimSpace(chat) == "" {
		return errors.New("会话 username 不能为空")
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return err
	}
	table := messageTable(chat)
	identity := loadMessageIdentity(root, chat)
	enrichedEmit := func(message Message) error {
		enrichRedPacketMessage(&message, redPackets)
		return emit(message)
	}
	queried := false
	var lastQueryError error
	for _, path := range files {
		if !messageDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		if tableExists(database, table) {
			relative, _ := filepath.Rel(root, path)
			queryErr := messageQueryEach(database, table, chat, filepath.ToSlash(relative), start, end, 0, identity, enrichedEmit)
			if queryErr == nil {
				queried = true
			} else {
				lastQueryError = queryErr
			}
		}
		_ = database.Close()
	}
	if !queried && lastQueryError != nil {
		return lastQueryError
	}
	return nil
}

func searchWindowEach(root, keyword, chat string, start, end *int64, emit func(Message) error) error {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return errors.New("搜索关键词不能为空")
	}
	filteredEmit := func(message Message) error {
		if strings.Contains(messageSearchText(message), keyword) {
			return emit(message)
		}
		return nil
	}
	redPackets := loadRedPacketIndex(root)
	if chat != "" {
		return historyWindowEachWithRedPackets(root, chat, start, end, redPackets, filteredEmit)
	}
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return err
	}
	for _, contact := range contacts {
		if err := historyWindowEachWithRedPackets(root, contact.Username, start, end, redPackets, filteredEmit); err != nil {
			continue
		}
	}
	return nil
}

// StreamExportWindow 使用私有磁盘暂存完成全局排序，避免 --all 导出把全部消息装入内存。
func StreamExportWindow(writer io.Writer, temporaryDirectory, root, mode, query, chat string, start, end *int64, format string) (int, error) {
	if writer == nil || (format != "json" && format != "jsonl") {
		return 0, errors.New("导出写入器或格式无效")
	}
	if err := os.MkdirAll(temporaryDirectory, 0o700); err != nil {
		return 0, err
	}
	temporary, err := os.CreateTemp(temporaryDirectory, ".v-local-cli-export-*.db")
	if err != nil {
		return 0, err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Chmod(0o600)
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return 0, err
	}
	database, err := sql.Open("sqlite", temporaryPath)
	if err != nil {
		_ = os.Remove(temporaryPath)
		return 0, err
	}
	defer func() {
		_ = database.Close()
		_ = os.Remove(temporaryPath)
	}()
	if _, err := database.Exec("PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; CREATE TABLE messages(sort_key INTEGER NOT NULL, evidence_id TEXT NOT NULL, payload BLOB NOT NULL)"); err != nil {
		return 0, err
	}
	transaction, err := database.Begin()
	if err != nil {
		return 0, err
	}
	statement, err := transaction.Prepare("INSERT INTO messages(sort_key,evidence_id,payload) VALUES(?,?,?)")
	if err != nil {
		_ = transaction.Rollback()
		return 0, err
	}
	emit := func(message Message) error {
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		_, err = statement.Exec(message.SortKey, message.EvidenceID, payload)
		return err
	}
	if mode == "history" {
		err = historyWindowEach(root, query, start, end, emit)
	} else if mode == "search" {
		err = searchWindowEach(root, query, chat, start, end, emit)
	} else {
		err = errors.New("导出类型无效")
	}
	closeStatementErr := statement.Close()
	if err != nil || closeStatementErr != nil {
		_ = transaction.Rollback()
		if err != nil {
			return 0, err
		}
		return 0, closeStatementErr
	}
	if err := transaction.Commit(); err != nil {
		return 0, err
	}
	rows, err := database.Query("SELECT payload FROM messages ORDER BY sort_key DESC, evidence_id DESC")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	if format == "json" {
		if _, err := io.WriteString(writer, "{\"items\":["); err != nil {
			return 0, err
		}
	}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return 0, err
		}
		if format == "json" {
			if count > 0 {
				if _, err := io.WriteString(writer, ","); err != nil {
					return 0, err
				}
			}
			if _, err := writer.Write(payload); err != nil {
				return 0, err
			}
		} else {
			if _, err := writer.Write(append(payload, '\n')); err != nil {
				return 0, err
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if format == "json" {
		if _, err := fmt.Fprintf(writer, "],\"count\":%d}\n", count); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func decodeValue(value any, flag int64) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, ok := value.([]byte)
	if !ok {
		return fmt.Sprint(value)
	}
	if flag == 4 || hasBytesPrefix(data, zstdMagic) {
		decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(16*1024*1024))
		if err == nil {
			plain, decodeErr := decoder.DecodeAll(data, nil)
			decoder.Close()
			if decodeErr == nil && len(plain) <= 4*1024*1024 {
				data = plain
			} else if decodeErr != nil {
				return "[压缩消息解码失败]"
			}
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

func hasBytesPrefix(value, prefix []byte) bool {
	return len(value) >= len(prefix) && string(value[:len(prefix)]) == string(prefix)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if bytes, ok := value.([]byte); ok {
		return strings.ToValidUTF8(string(bytes), "�")
	}
	return fmt.Sprint(value)
}

func asInt64(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case float64:
		return int64(number)
	case []byte:
		parsed, _ := strconv.ParseInt(string(number), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(number, 10, 64)
		return parsed
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
