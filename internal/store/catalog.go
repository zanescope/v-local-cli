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

// WalkMessages 对 generation 中的全部已识别消息表做一次确定性扫描。无法映射到
// 联系人的 Msg_* 表会进入 coverage，而不是被悄悄视为完整。
func WalkMessages(root string, emit func(Message) error) (MessageScanCoverage, error) {
	coverage := MessageScanCoverage{}
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return coverage, err
	}
	tableToChat := map[string]string{}
	for _, contact := range contacts {
		tableToChat[messageTable(contact.Username)] = contact.Username
	}
	// 已删除或尚未进入 contact 表的会话仍可能存在消息表。SessionTable 保留
	// 可逆的 username，可安全补足哈希表名映射，同时继续对真正未知表报告不完整。
	if sessions, sessionErr := Sessions(root, false, "", 0); sessionErr == nil {
		for _, session := range sessions.Items {
			tableToChat[messageTable(session.Username)] = session.Username
		}
	}
	files, err := sqliteFiles(root)
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
			if chat == "" {
				coverage.UnknownTables = append(coverage.UnknownTables, filepath.ToSlash(relative)+":"+table)
				continue
			}
			identity := loadMessageIdentity(root, chat)
			queryErr := messageQueryEach(database, table, chat, filepath.ToSlash(relative), nil, nil, 0, identity, func(message Message) error {
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
