package store

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Favorite struct {
	EvidenceID string `json:"evidence_id"`
	LocalID    int64  `json:"local_id"`
	Type       int64  `json:"type"`
	Kind       string `json:"kind"`
	Timestamp  int64  `json:"timestamp"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text,omitempty"`
	URL        string `json:"url,omitempty"`
	From       string `json:"from,omitempty"`
	Chat       string `json:"chat,omitempty"`
	SourceDB   string `json:"source_db"`
}

type FavoriteReport struct {
	Items    []Favorite     `json:"items"`
	Coverage map[string]any `json:"coverage"`
}

func favoriteKind(value int64) string {
	switch value {
	case 1:
		return "text"
	case 2:
		return "image"
	case 3:
		return "voice"
	case 4, 20:
		return "video"
	case 5:
		return "article"
	case 6:
		return "location"
	case 7:
		return "mini_program"
	case 14:
		return "chat_record"
	case 19:
		return "contact_card"
	default:
		return "other"
	}
}

func truncateFavoriteText(value string, limit int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func safeFavoriteURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" {
		return ""
	}
	return parsed.String()
}

func parseFavoriteContent(content string) (title, text, target string) {
	root, status := parseXML(content, true)
	if status != "" {
		return "", truncateFavoriteText(content, 4096), ""
	}
	title = firstNonEmpty(root.descendantText("title"), root.descendantText("filename", "file_name"))
	text = firstNonEmpty(root.descendantText("desc", "digest", "summary"), root.descendantText("content"))
	target = safeFavoriteURL(firstNonEmpty(root.descendantText("url", "link", "sourceurl", "source_url")))
	if text == "" {
		if favoriteText := root.text(); favoriteText != "" {
			text = favoriteText
		}
	}
	return truncateFavoriteText(title, 512), truncateFavoriteText(text, 4096), target
}

func Favorites(root, keyword, kind string, limit int) (FavoriteReport, error) {
	report := FavoriteReport{Coverage: map[string]any{"available": false, "complete": false, "source": "favorite.db/fav_db_item"}}
	files, err := sqliteFiles(root)
	if err != nil {
		return report, err
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	databasesFound := 0
	databasesScanned := 0
	failed := []string{}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "favorite.db") {
			continue
		}
		databasesFound++
		relative, _ := filepath.Rel(root, path)
		relativeName := filepath.ToSlash(relative)
		database, openErr := openReadOnly(path)
		if openErr != nil {
			failed = append(failed, relativeName+":open")
			continue
		}
		databasesScanned++
		if !tableExists(database, "fav_db_item") {
			failed = append(failed, relativeName+":table_missing")
			_ = database.Close()
			continue
		}
		available := columns(database, "fav_db_item")
		if !available["local_id"] || !available["type"] {
			failed = append(failed, relativeName+":required_columns_missing")
			_ = database.Close()
			continue
		}
		selected := selectedColumns(available, "local_id", "type", "update_time", "content", "fromusr", "realchatname")
		rows, queryErr := database.Query("SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier("fav_db_item"))
		if queryErr != nil {
			failed = append(failed, relativeName+":query")
			_ = database.Close()
			continue
		}
		scanErr := scanDynamicRows(rows, selected, func(fields map[string]any) {
			localID, typeNumber := asInt64(fields["local_id"]), asInt64(fields["type"])
			itemKind := favoriteKind(typeNumber)
			if kind != "" && itemKind != kind {
				return
			}
			title, text, target := parseFavoriteContent(decodeValue(fields["content"], 0))
			from, chat := strings.TrimSpace(asString(fields["fromusr"])), strings.TrimSpace(asString(fields["realchatname"]))
			haystack := strings.ToLower(strings.Join([]string{title, text, from, chat, target}, "\n"))
			if keyword != "" && !strings.Contains(haystack, keyword) {
				return
			}
			timestamp := asInt64(fields["update_time"])
			if timestamp > 9_999_999_999 {
				timestamp /= 1000
			}
			report.Items = append(report.Items, Favorite{
				EvidenceID: "wechat:favorite:" + asString(localID), LocalID: localID, Type: typeNumber,
				Kind: itemKind, Timestamp: timestamp, Title: title, Text: text, URL: target,
				From: from, Chat: chat, SourceDB: filepath.ToSlash(relative),
			})
		})
		if scanErr != nil {
			failed = append(failed, relativeName+":scan")
		}
		_ = rows.Close()
		_ = database.Close()
		report.Coverage["available"] = true
	}
	report.Coverage["databases_found"] = databasesFound
	report.Coverage["databases_scanned"] = databasesScanned
	if len(failed) > 0 {
		report.Coverage["failed_sources"] = failed
	}
	report.Coverage["complete"] = report.Coverage["available"] == true && len(failed) == 0
	sort.Slice(report.Items, func(left, right int) bool {
		if report.Items[left].Timestamp == report.Items[right].Timestamp {
			return report.Items[left].EvidenceID > report.Items[right].EvidenceID
		}
		return report.Items[left].Timestamp > report.Items[right].Timestamp
	})
	if limit > 0 && len(report.Items) > limit {
		report.Items = report.Items[:limit]
		report.Coverage["result_limit_applied"] = true
	} else {
		report.Coverage["result_limit_applied"] = false
	}
	if report.Coverage["available"] != true {
		report.Coverage["reason"] = "favorite_database_or_table_missing"
	}
	return report, nil
}
