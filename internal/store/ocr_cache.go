package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxOCRTextBytes = 2 * 1024 * 1024

// OCRText 是 v-local-cli 私有缓存中的图片文字记录；缓存不保存原始图片。
type OCRText struct {
	EvidenceID    string `json:"evidence_id"`
	Chat          string `json:"chat"`
	LocalID       int64  `json:"local_id"`
	ServerID      int64  `json:"server_id,omitempty"`
	Timestamp     int64  `json:"timestamp,omitempty"`
	SortKey       int64  `json:"sort_key,omitempty"`
	Text          string `json:"text"`
	ImageSHA256   string `json:"image_sha256"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engine_version,omitempty"`
	Source        string `json:"source"`
	CreatedAt     string `json:"created_at"`
}

func openOCRTextCache(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("OCR 文字缓存路径为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
		CREATE TABLE IF NOT EXISTS ocr_texts(
			evidence_id TEXT PRIMARY KEY,
			chat TEXT NOT NULL,
			local_id INTEGER NOT NULL,
			server_id INTEGER NOT NULL,
			timestamp INTEGER NOT NULL,
			sort_key INTEGER NOT NULL,
			text TEXT NOT NULL,
			image_sha256 TEXT NOT NULL,
			engine TEXT NOT NULL,
			engine_version TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`); err != nil {
		_ = database.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return database, nil
}

func scanOCRText(scanner interface{ Scan(...any) error }) (OCRText, error) {
	var value OCRText
	err := scanner.Scan(
		&value.EvidenceID, &value.Chat, &value.LocalID, &value.ServerID,
		&value.Timestamp, &value.SortKey, &value.Text, &value.ImageSHA256,
		&value.Engine, &value.EngineVersion, &value.Source, &value.CreatedAt,
	)
	return value, err
}

// SaveOCRText 原子替换同一图片证据的识别文字。
func SaveOCRText(path string, value OCRText) error {
	digest, digestErr := hex.DecodeString(value.ImageSHA256)
	value.Text = strings.TrimSpace(strings.ToValidUTF8(value.Text, "�"))
	if strings.TrimSpace(value.EvidenceID) == "" || strings.TrimSpace(value.Chat) == "" ||
		value.LocalID <= 0 || value.Text == "" || len(value.Text) > maxOCRTextBytes ||
		digestErr != nil || len(digest) != sha256.Size {
		return errors.New("OCR 文字记录无效")
	}
	if value.Engine == "" {
		value.Engine = "wechat-native-ocr"
	}
	if value.Source == "" {
		value.Source = "v-local-cli_private_cache"
	}
	if value.CreatedAt == "" {
		value.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	database, err := openOCRTextCache(path)
	if err != nil {
		return err
	}
	defer database.Close()
	_, err = database.Exec(`INSERT INTO ocr_texts(
		evidence_id,chat,local_id,server_id,timestamp,sort_key,text,image_sha256,engine,engine_version,source,created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(evidence_id) DO UPDATE SET
		chat=excluded.chat,local_id=excluded.local_id,server_id=excluded.server_id,
		timestamp=excluded.timestamp,sort_key=excluded.sort_key,text=excluded.text,
		image_sha256=excluded.image_sha256,engine=excluded.engine,engine_version=excluded.engine_version,
		source=excluded.source,created_at=excluded.created_at`,
		value.EvidenceID, value.Chat, value.LocalID, value.ServerID, value.Timestamp, value.SortKey,
		value.Text, value.ImageSHA256, value.Engine, value.EngineVersion, value.Source, value.CreatedAt,
	)
	return err
}

// LoadOCRText 读取一条私有 OCR 文字缓存。
func LoadOCRText(path, evidenceID string) (OCRText, bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return OCRText{}, false, nil
	} else if err != nil {
		return OCRText{}, false, err
	}
	database, err := openOCRTextCache(path)
	if err != nil {
		return OCRText{}, false, err
	}
	defer database.Close()
	row := database.QueryRow(`SELECT evidence_id,chat,local_id,server_id,timestamp,sort_key,text,
		image_sha256,engine,engine_version,source,created_at FROM ocr_texts WHERE evidence_id=?`, evidenceID)
	value, err := scanOCRText(row)
	if err == sql.ErrNoRows {
		return OCRText{}, false, nil
	}
	return value, err == nil, err
}

// SearchOCRTexts 直接搜索私有 OCR 缓存；调用方再用当前快照逐条校验证据。
func SearchOCRTexts(path, keyword, chat string, start, end *int64, limit int) ([]OCRText, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []OCRText{}, nil
	} else if err != nil {
		return nil, err
	}
	database, err := openOCRTextCache(path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.Query(`SELECT evidence_id,chat,local_id,server_id,timestamp,sort_key,text,
		image_sha256,engine,engine_version,source,created_at FROM ocr_texts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	needle := strings.ToLower(strings.TrimSpace(keyword))
	result := []OCRText{}
	for rows.Next() {
		value, scanErr := scanOCRText(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if chat != "" && value.Chat != chat ||
			start != nil && value.Timestamp < *start || end != nil && value.Timestamp > *end ||
			needle != "" && !strings.Contains(strings.ToLower(value.Text), needle) {
			continue
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

// DeleteOCRText 删除已经无法与当前图片内容一致关联的缓存记录。
func DeleteOCRText(path, evidenceID string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	database, err := openOCRTextCache(path)
	if err != nil {
		return err
	}
	defer database.Close()
	_, err = database.Exec(`DELETE FROM ocr_texts WHERE evidence_id=?`, evidenceID)
	return err
}

// OCRTextCount 返回私有 OCR 缓存的记录数，不存在缓存时返回零。
func OCRTextCount(path string) (int, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	database, err := openOCRTextCache(path)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	var count int
	err = database.QueryRow(`SELECT count(*) FROM ocr_texts`).Scan(&count)
	return count, err
}
