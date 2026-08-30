package store

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
)

type MessageScanCoverage struct {
	DatabasesScanned int      `json:"databases_scanned"`
	TablesDiscovered int      `json:"tables_discovered"`
	TablesIndexed    int      `json:"tables_indexed"`
	RowsEmitted      int      `json:"rows_emitted"`
	UnknownTables    []string `json:"unknown_tables,omitempty"`
	FailedTables     []string `json:"failed_tables,omitempty"`
	Complete         bool     `json:"complete"`
}

func messageTables(database *sql.DB) ([]string, error) {
	rows, err := database.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Msg_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && strings.HasPrefix(name, "Msg_") {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result, rows.Err()
}

func bindMessageChat(tableToChat map[string]string, ambiguousTables map[string]bool, chat string) {
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return
	}
	table := messageTable(chat)
	if ambiguousTables[table] {
		return
	}
	if previous, exists := tableToChat[table]; exists && previous != chat {
		delete(tableToChat, table)
		ambiguousTables[table] = true
		return
	}
	tableToChat[table] = chat
}

func resolveMessageTableChats(root string) (map[string]string, map[string]bool, []string, error) {
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return nil, nil, nil, err
	}
	tableToChat := map[string]string{}
	ambiguousTables := map[string]bool{}
	for _, contact := range contacts {
		bindMessageChat(tableToChat, ambiguousTables, contact.Username)
	}
	// 已删除或尚未进入 contact 表的会话仍可能存在消息表。SessionTable 保留
	// 可逆的 username，可安全补足哈希表名映射，同时继续对真正未知表报告不完整。
	if sessions, sessionErr := Sessions(root, false, "", 0); sessionErr == nil {
		for _, session := range sessions.Items {
			bindMessageChat(tableToChat, ambiguousTables, session.Username)
		}
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, nil, nil, err
	}
	// Name2Id 保留消息库已经知道的稳定 username。仅当其精确哈希对应同库实际
	// Msg_* 表时才用于补足已删除联系人或已移出 SessionTable 的历史会话。
	for _, path := range files {
		if !messageDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		tables, tableErr := messageTables(database)
		if tableErr == nil {
			present := make(map[string]bool, len(tables))
			for _, table := range tables {
				present[table] = true
			}
			for _, username := range name2ID(database) {
				if present[messageTable(username)] {
					bindMessageChat(tableToChat, ambiguousTables, username)
				}
			}
		}
		_ = database.Close()
	}
	return tableToChat, ambiguousTables, files, nil
}

// walkMessagesWindow 对 generation 中的全部已识别消息表做一次确定性扫描。
// 无法映射到联系人的 Msg_* 表会进入 coverage，而不是被悄悄视为完整。
func walkMessagesWindow(root string, start, end *int64, emit func(Message) error) (MessageScanCoverage, error) {
	coverage := MessageScanCoverage{}
	tableToChat, ambiguousTables, files, err := resolveMessageTableChats(root)
	if err != nil {
		return coverage, err
	}
	for _, path := range files {
		if !messageDatabase(path) {
			continue
		}
		relative, _ := filepath.Rel(root, path)
		database, openErr := openReadOnly(path)
		if openErr != nil {
			coverage.FailedTables = append(coverage.FailedTables, filepath.ToSlash(relative)+":open")
			continue
		}
		coverage.DatabasesScanned++
		tables, tableErr := messageTables(database)
		if tableErr != nil {
			_ = database.Close()
			return coverage, tableErr
		}
		coverage.TablesDiscovered += len(tables)
		for _, table := range tables {
			chat := tableToChat[table]
			if chat == "" || ambiguousTables[table] {
				coverage.UnknownTables = append(coverage.UnknownTables, filepath.ToSlash(relative)+":"+table)
				continue
			}
			identity := loadMessageIdentity(root, chat)
			queryErr := messageQueryEach(database, table, chat, filepath.ToSlash(relative), start, end, 0, identity, func(message Message) error {
				coverage.RowsEmitted++
				return emit(message)
			})
			if queryErr != nil {
				coverage.FailedTables = append(coverage.FailedTables, filepath.ToSlash(relative)+":"+table)
				continue
			}
			coverage.TablesIndexed++
		}
		_ = database.Close()
	}
	coverage.Complete = len(coverage.UnknownTables) == 0 && len(coverage.FailedTables) == 0 && coverage.TablesDiscovered == coverage.TablesIndexed
	return coverage, nil
}

// WalkMessages 扫描 generation 中的全部已识别消息，供内容寻址索引构建使用。
func WalkMessages(root string, emit func(Message) error) (MessageScanCoverage, error) {
	return walkMessagesWindow(root, nil, nil, emit)
}

// MessagesWindow 返回跨会话、按全局时间倒序排列的消息。limit=0 表示不设条数上限；
// coverage 始终反映完整表扫描情况，未知哈希表不会被静默忽略。
func MessagesWindow(root string, start, end *int64, limit int) ([]Message, MessageScanCoverage, error) {
	results := newBoundedSearchResults(limit)
	redPackets := loadRedPacketIndex(root)
	contacts := loadContactIdentity(root)
	coverage, err := walkMessagesWindow(root, start, end, func(message Message) error {
		enrichRedPacketMessage(&message, redPackets)
		if contact, found := contacts[message.Chat]; found {
			message.ChatDisplay = firstNonEmpty(contact.Display, contact.Remark, contact.Nickname, message.Chat)
			message.ChatKind = contact.Kind
		} else {
			message.ChatDisplay = message.Chat
			message.ChatKind = contactKind(message.Chat)
		}
		return results.emit(message)
	})
	if err != nil {
		return nil, coverage, err
	}
	return results.values(), coverage, nil
}
