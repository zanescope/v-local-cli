package store

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
)

// Session 是某个不可变快照中的会话状态。UnreadCount 只代表快照捕获时的
// SessionTable 值，不应解释为微信服务端的实时未读数。
type Session struct {
	Username        string `json:"username"`
	Display         string `json:"display"`
	Kind            string `json:"kind"`
	UnreadCount     int64  `json:"snapshot_unread_count"`
	LastTimestamp   int64  `json:"last_timestamp"`
	LastMessageKind string `json:"last_message_kind,omitempty"`
	LastSummary     string `json:"last_summary,omitempty"`
	LastSender      string `json:"last_sender,omitempty"`
	SourceDB        string `json:"source_db"`
}

type SessionReport struct {
	Items    []Session      `json:"items"`
	Coverage map[string]any `json:"session_source_coverage"`
}

func sessionDatabase(root string) string {
	files, err := sqliteFiles(root)
	if err != nil {
		return ""
	}
	for _, path := range files {
		if strings.EqualFold(filepath.Base(path), "session.db") {
			return path
		}
	}
	return ""
}

func selectedColumns(available map[string]bool, wanted ...string) []string {
	selected := make([]string, 0, len(wanted))
	for _, name := range wanted {
		if available[name] {
			selected = append(selected, name)
		}
	}
	return selected
}

func scanDynamicRows(rows *sql.Rows, selected []string, emit func(map[string]any)) error {
	for rows.Next() {
		values := make([]any, len(selected))
		targets := make([]any, len(selected))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			continue
		}
		fields := make(map[string]any, len(selected))
		for index, name := range selected {
			fields[name] = values[index]
		}
		emit(fields)
	}
	return rows.Err()
}

func Sessions(root string, unreadOnly bool, kind string, limit int) (SessionReport, error) {
	report := SessionReport{Coverage: map[string]any{
		"source_present": false, "status": "none", "source": "session.db/SessionTable",
	}}
	path := sessionDatabase(root)
	if path == "" {
		report.Coverage["reason"] = "session_database_missing"
		return report, nil
	}
	database, err := openReadOnly(path)
	if err != nil {
		return report, err
	}
	defer database.Close()
	if !tableExists(database, "SessionTable") {
		report.Coverage["reason"] = "session_table_missing"
		return report, nil
	}
	available := columns(database, "SessionTable")
	if !available["username"] {
		report.Coverage["reason"] = "username_column_missing"
		return report, nil
	}
	selected := selectedColumns(available, "username", "unread_count", "summary", "last_timestamp", "last_msg_type", "last_msg_sender", "last_sender_display_name")
	rows, err := database.Query("SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier("SessionTable"))
	if err != nil {
		return report, err
	}
	defer rows.Close()
	contacts := loadContactIdentity(root)
	relative, _ := filepath.Rel(root, path)
	err = scanDynamicRows(rows, selected, func(fields map[string]any) {
		username := strings.TrimSpace(asString(fields["username"]))
		if username == "" {
			return
		}
		unread := asInt64(fields["unread_count"])
		if unreadOnly && unread <= 0 {
			return
		}
		chatKind := contactKind(username)
		if contact, found := contacts[username]; found && contact.Kind != "" {
			chatKind = contact.Kind
		}
		if kind != "" && kind != chatKind {
			return
		}
		summary := strings.TrimSpace(decodeValue(fields["summary"], 0))
		localType := asInt64(fields["last_msg_type"])
		parsed := parseMessageContent(localType, summary, "")
		if parsed.Content != "" {
			summary = parsed.Content
		}
		senderUsername := strings.TrimSpace(asString(fields["last_msg_sender"]))
		senderDisplay := strings.TrimSpace(asString(fields["last_sender_display_name"]))
		if senderDisplay == "" {
			if contact, found := contacts[senderUsername]; found {
				senderDisplay = contact.Display
			} else {
				senderDisplay = senderUsername
			}
		}
		display := username
		if contact, found := contacts[username]; found {
			display = contact.Display
		}
		report.Items = append(report.Items, Session{
			Username: username, Display: display, Kind: chatKind, UnreadCount: unread,
			LastTimestamp: asInt64(fields["last_timestamp"]), LastMessageKind: parsed.Kind,
			LastSummary: summary, LastSender: senderDisplay, SourceDB: filepath.ToSlash(relative),
		})
	})
	if err != nil {
		return report, err
	}
	sort.Slice(report.Items, func(left, right int) bool {
		if report.Items[left].LastTimestamp == report.Items[right].LastTimestamp {
			return report.Items[left].Username < report.Items[right].Username
		}
		return report.Items[left].LastTimestamp > report.Items[right].LastTimestamp
	})
	if limit > 0 && len(report.Items) > limit {
		report.Items = report.Items[:limit]
		report.Coverage["result_limit_applied"] = true
	} else {
		report.Coverage["result_limit_applied"] = false
	}
	report.Coverage["source_present"] = true
	report.Coverage["status"] = "complete"
	report.Coverage["columns"] = selected
	return report, nil
}
