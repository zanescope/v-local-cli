package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxWeChatIndexedTextBytes = 2 * 1024 * 1024

var (
	voiceIndexTablePattern = regexp.MustCompile(`(?i)^message_fts_v[0-9]+_[0-9]+$`)
	imageIndexTablePattern = regexp.MustCompile(`(?i)^imgfts[0-9]+v[0-9]+$`)
)

type WeChatIndexStatus struct {
	DatabaseFound      bool `json:"database_found"`
	SessionMapFound    bool `json:"session_map_found"`
	VoiceIndexTables   int  `json:"voice_index_tables"`
	ImageIndexTables   int  `json:"image_index_tables"`
	VoiceIndexedRows   int  `json:"voice_indexed_rows"`
	ImageIndexedRows   int  `json:"image_indexed_rows"`
	ExternalDependency bool `json:"external_dependency"`
	EngineInvoked      bool `json:"engine_invoked"`
	PrivateIPCInvoked  bool `json:"private_ipc_invoked"`
	NetworkPerformed   bool `json:"network_performed"`
}

type WeChatIndexedText struct {
	EvidenceID string `json:"evidence_id"`
	Chat       string `json:"chat"`
	LocalID    int64  `json:"local_id"`
	ServerID   int64  `json:"server_id,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	SortKey    int64  `json:"sort_key,omitempty"`
	Sender     string `json:"sender,omitempty"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Source     string `json:"source"`
	SourceDB   string `json:"source_db"`
	IndexTable string `json:"index_table"`
}

type indexMessageKey struct {
	SessionID int64
	LocalID   int64
}

func findMessageIndexDatabase(root string) (string, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return "", err
	}
	for _, path := range files {
		if strings.EqualFold(filepath.Base(path), "message_fts.db") {
			return path, nil
		}
	}
	return "", nil
}

func indexTables(database *sql.DB, pattern *regexp.Regexp) ([]string, error) {
	rows, err := database.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || !pattern.MatchString(name) {
			continue
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	result := []string{}
	for _, name := range names {
		available := columns(database, name)
		if columnCI(available, "acontent") != "" && columnCI(available, "local_type") != "" &&
			columnCI(available, "message_local_id") != "" && columnCI(available, "session_id") != "" {
			result = append(result, name)
		}
	}
	return result, nil
}

func countIndexRows(database *sql.DB, tables []string, baseType int64) int {
	total := 0
	for _, table := range tables {
		query := "SELECT count(*) FROM " + quoteIdentifier(table)
		if baseType > 0 {
			query += " WHERE (local_type & 4294967295)=?"
		}
		var count int
		var err error
		if baseType > 0 {
			err = database.QueryRow(query, baseType).Scan(&count)
		} else {
			err = database.QueryRow(query).Scan(&count)
		}
		if err == nil && count > 0 {
			total += count
		}
	}
	return total
}

// WeChatTextIndexStatus 只读取微信现有全文索引的结构和计数，不启动微信组件。
func WeChatTextIndexStatus(root string) (WeChatIndexStatus, error) {
	status := WeChatIndexStatus{}
	path, err := findMessageIndexDatabase(root)
	if err != nil || path == "" {
		return status, err
	}
	status.DatabaseFound = true
	database, err := openReadOnly(path)
	if err != nil {
		return status, err
	}
	defer database.Close()
	status.SessionMapFound = findTableCI(database, "name2id") != ""
	voiceTables, err := indexTables(database, voiceIndexTablePattern)
	if err != nil {
		return status, err
	}
	imageTables, err := indexTables(database, imageIndexTablePattern)
	if err != nil {
		return status, err
	}
	status.VoiceIndexTables = len(voiceTables)
	status.ImageIndexTables = len(imageTables)
	status.VoiceIndexedRows = countIndexRows(database, voiceTables, 34)
	status.ImageIndexedRows = countIndexRows(database, imageTables, 0)
	return status, nil
}

func indexedText(value any) (string, error) {
	text := strings.TrimSpace(asString(value))
	if text == "" {
		return "", nil
	}
	if len(text) > maxWeChatIndexedTextBytes || strings.ContainsRune(text, 0) {
		return "", errors.New("微信索引文本大小或编码无效")
	}
	text = strings.ToValidUTF8(text, "�")
	if strings.Contains(strings.ToLower(text), "<voicemsg") {
		return "", errors.New("微信索引返回了语音元数据而不是转写文本")
	}
	return text, nil
}

func indexedSessionIDs(database *sql.DB, chats map[string]bool) (map[string]int64, error) {
	table := findTableCI(database, "name2id")
	if table == "" {
		return map[string]int64{}, nil
	}
	available := columns(database, table)
	usernameColumn := columnCI(available, "username")
	if usernameColumn == "" {
		return map[string]int64{}, nil
	}
	rows, err := database.Query("SELECT rowid," + quoteIdentifier(usernameColumn) + " FROM " + quoteIdentifier(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var id int64
		var raw any
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		username := asString(raw)
		if id > 0 && chats[username] {
			result[username] = id
		}
	}
	return result, rows.Err()
}

func indexedTexts(root string, messages []Message, kind string) (map[string]WeChatIndexedText, error) {
	result := map[string]WeChatIndexedText{}
	if len(messages) == 0 {
		return result, nil
	}
	path, err := findMessageIndexDatabase(root)
	if err != nil || path == "" {
		return result, err
	}
	database, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	chats := map[string]bool{}
	for _, message := range messages {
		if message.Kind == kind && message.LocalID > 0 {
			chats[message.Chat] = true
		}
	}
	sessionIDs, err := indexedSessionIDs(database, chats)
	if err != nil {
		return nil, err
	}
	pattern := voiceIndexTablePattern
	baseType := int64(34)
	if kind == "image" {
		pattern = imageIndexTablePattern
		baseType = 3
	} else if kind != "voice" {
		return nil, errors.New("微信索引文本类型无效")
	}
	tables, err := indexTables(database, pattern)
	if err != nil {
		return nil, err
	}
	candidates := map[indexMessageKey]Message{}
	sessions := map[int64]bool{}
	for _, message := range messages {
		sessionID := sessionIDs[message.Chat]
		if message.Kind == kind && message.LocalID > 0 && sessionID > 0 {
			candidates[indexMessageKey{SessionID: sessionID, LocalID: message.LocalID}] = message
			sessions[sessionID] = true
		}
	}
	seenText := map[string]string{}
	for _, table := range tables {
		for sessionID := range sessions {
			query := "SELECT message_local_id,acontent FROM " + quoteIdentifier(table) + " WHERE session_id=? AND (local_type & 4294967295)=?"
			rows, queryErr := database.Query(query, sessionID, baseType)
			if queryErr != nil {
				continue
			}
			for rows.Next() {
				var localID int64
				var raw any
				if rows.Scan(&localID, &raw) != nil {
					continue
				}
				message, found := candidates[indexMessageKey{SessionID: sessionID, LocalID: localID}]
				if !found {
					continue
				}
				text, textErr := indexedText(raw)
				if textErr != nil {
					_ = rows.Close()
					return nil, textErr
				}
				if text == "" {
					continue
				}
				if previous, exists := seenText[message.EvidenceID]; exists && previous != text {
					_ = rows.Close()
					return nil, fmt.Errorf("微信索引对证据 %s 返回了冲突文本", message.EvidenceID)
				}
				seenText[message.EvidenceID] = text
				result[message.EvidenceID] = WeChatIndexedText{
					EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID,
					ServerID: message.ServerID, Timestamp: message.Timestamp, SortKey: message.SortKey,
					Sender: message.Sender, Kind: kind, Text: text, Source: "wechat_existing_index",
					SourceDB: filepath.Base(path), IndexTable: table,
				}
			}
			_ = rows.Close()
		}
	}
	return result, nil
}

// WeChatVoiceTexts 读取微信已经生成的语音转写索引，不触发新的转写或联网。
func WeChatVoiceTexts(root string, messages []Message) (map[string]WeChatIndexedText, error) {
	return indexedTexts(root, messages, "voice")
}

// WeChatOCRTexts 读取微信已经生成的图片 OCR 索引，不启动 OCR 组件。
func WeChatOCRTexts(root string, messages []Message) (map[string]WeChatIndexedText, error) {
	return indexedTexts(root, messages, "image")
}

func mediaMessages(root, chat, kind string, start, end *int64, limit int) ([]Message, error) {
	collect := func(messages []Message, result *[]Message) {
		for _, message := range messages {
			if message.Kind == kind {
				*result = append(*result, message)
			}
		}
	}
	result := []Message{}
	if chat != "" {
		messages, err := HistoryWindow(root, chat, start, end, 0)
		if err != nil {
			return nil, err
		}
		collect(messages, &result)
	} else {
		contacts, err := Contacts(root, "", 0)
		if err != nil {
			return nil, err
		}
		for _, contact := range contacts {
			messages, historyErr := HistoryWindow(root, contact.Username, start, end, 0)
			if historyErr == nil {
				collect(messages, &result)
			}
		}
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

// ImageMessages 返回时间窗口内的图片消息；limit 为零表示不限制条数。
func ImageMessages(root, chat string, start, end *int64, limit int) ([]Message, error) {
	return mediaMessages(root, chat, "image", start, end, limit)
}

// FindImageMessage 根据证据标识重新从只读快照中定位图片消息。
func FindImageMessage(root, evidenceID string) (Message, error) {
	chat, _, err := ParseMessageEvidenceID(evidenceID)
	if err != nil {
		return Message{}, errors.New("图片证据标识格式无效")
	}
	messages, err := HistoryWindow(root, chat, nil, nil, 0)
	if err != nil {
		return Message{}, err
	}
	for _, message := range messages {
		if message.EvidenceID == evidenceID {
			if message.Kind != "image" {
				return Message{}, errors.New("指定证据不是图片消息")
			}
			return message, nil
		}
	}
	return Message{}, errors.New("当前快照中没有找到指定图片证据")
}
