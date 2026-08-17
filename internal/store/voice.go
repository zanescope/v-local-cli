package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	silk "github.com/wdvxdr1123/go-silk"
)

const (
	voiceSampleRate     = 16000
	maxVoiceSourceBytes = 8 * 1024 * 1024
	maxVoicePCMBytes    = 128 * 1024 * 1024
)

type VoiceTranscript struct {
	EvidenceID  string `json:"evidence_id"`
	Chat        string `json:"chat"`
	ServerID    int64  `json:"server_id,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	SortKey     int64  `json:"sort_key,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Transcript  string `json:"transcript"`
	AudioSHA256 string `json:"audio_sha256,omitempty"`
	Engine      string `json:"engine,omitempty"`
	Model       string `json:"model,omitempty"`
	Language    string `json:"language,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	Source      string `json:"source,omitempty"`
}

// ParseMessageEvidenceID 解析 CLI 自己生成的聊天消息证据标识。
func ParseMessageEvidenceID(evidenceID string) (string, int64, error) {
	const prefix = "wechat:"
	if !strings.HasPrefix(evidenceID, prefix) {
		return "", 0, errors.New("语音证据标识格式无效")
	}
	remainder := strings.TrimPrefix(evidenceID, prefix)
	separator := strings.LastIndex(remainder, ":")
	if separator <= 0 || separator == len(remainder)-1 {
		return "", 0, errors.New("语音证据标识格式无效")
	}
	identifier, err := strconv.ParseInt(remainder[separator+1:], 10, 64)
	if err != nil || identifier <= 0 {
		return "", 0, errors.New("语音证据标识格式无效")
	}
	return remainder[:separator], identifier, nil
}

// FindVoiceMessage 根据证据标识重新从只读快照中定位语音消息，避免接受任意数据库主键。
func FindVoiceMessage(root, evidenceID string) (Message, error) {
	chat, _, err := ParseMessageEvidenceID(evidenceID)
	if err != nil {
		return Message{}, err
	}
	messages, err := HistoryWindow(root, chat, nil, nil, 0)
	if err != nil {
		return Message{}, err
	}
	for _, message := range messages {
		if message.EvidenceID == evidenceID {
			if message.Kind != "voice" {
				return Message{}, errors.New("指定证据不是语音消息")
			}
			return message, nil
		}
	}
	return Message{}, errors.New("当前快照中没有找到指定语音证据")
}

func voiceDatabase(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(name, "media_") && strings.HasSuffix(name, ".db")
}

// VoiceData 按服务端消息标识从 media_*.db 中读取 SILK 负载。
func VoiceData(root string, serverID int64) ([]byte, error) {
	if serverID <= 0 {
		return nil, errors.New("语音消息缺少可关联的 server_id")
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		if !voiceDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "VoiceInfo")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		serverColumn := columnCI(available, "svr_id")
		dataColumn := columnCI(available, "voice_data")
		if serverColumn == "" || dataColumn == "" {
			_ = database.Close()
			continue
		}
		query := "SELECT " + quoteIdentifier(dataColumn) + " FROM " + quoteIdentifier(table) + " WHERE " + quoteIdentifier(serverColumn) + "=? LIMIT 1"
		var payload []byte
		queryErr := database.QueryRow(query, serverID).Scan(&payload)
		_ = database.Close()
		if queryErr == sql.ErrNoRows {
			continue
		}
		if queryErr != nil {
			return nil, queryErr
		}
		if len(payload) == 0 || len(payload) > maxVoiceSourceBytes {
			return nil, errors.New("语音负载大小无效")
		}
		return append([]byte(nil), payload...), nil
	}
	return nil, errors.New("当前快照中没有找到语音负载")
}

// VoiceMessages 返回时间窗口内的语音消息；limit 为零表示不限制条数。
func VoiceMessages(root, chat string, start, end *int64, limit int) ([]Message, error) {
	collect := func(messages []Message, result *[]Message) {
		for _, message := range messages {
			if message.Kind == "voice" {
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

// DecodeVoiceWAV 把微信 SILK 负载解码为 whisper.cpp 可直接读取的 16 kHz 单声道 WAV。
func DecodeVoiceWAV(payload []byte) ([]byte, string, error) {
	if len(payload) == 0 || len(payload) > maxVoiceSourceBytes {
		return nil, "", errors.New("语音负载大小无效")
	}
	digest := sha256.Sum256(payload)
	pcm, err := decodeSilkSafely(payload)
	if err != nil {
		return nil, "", fmt.Errorf("SILK 解码失败：%w", err)
	}
	if len(pcm) == 0 || len(pcm) > maxVoicePCMBytes || len(pcm)%2 != 0 {
		return nil, "", errors.New("SILK 解码后的 PCM 大小无效")
	}
	var output bytes.Buffer
	dataBytes := uint32(len(pcm))
	_ = binary.Write(&output, binary.LittleEndian, [4]byte{'R', 'I', 'F', 'F'})
	_ = binary.Write(&output, binary.LittleEndian, uint32(36)+dataBytes)
	_ = binary.Write(&output, binary.LittleEndian, [4]byte{'W', 'A', 'V', 'E'})
	_ = binary.Write(&output, binary.LittleEndian, [4]byte{'f', 'm', 't', ' '})
	_ = binary.Write(&output, binary.LittleEndian, uint32(16))
	_ = binary.Write(&output, binary.LittleEndian, uint16(1))
	_ = binary.Write(&output, binary.LittleEndian, uint16(1))
	_ = binary.Write(&output, binary.LittleEndian, uint32(voiceSampleRate))
	_ = binary.Write(&output, binary.LittleEndian, uint32(voiceSampleRate*2))
	_ = binary.Write(&output, binary.LittleEndian, uint16(2))
	_ = binary.Write(&output, binary.LittleEndian, uint16(16))
	_ = binary.Write(&output, binary.LittleEndian, [4]byte{'d', 'a', 't', 'a'})
	_ = binary.Write(&output, binary.LittleEndian, dataBytes)
	_, _ = output.Write(pcm)
	return output.Bytes(), hex.EncodeToString(digest[:]), nil
}

func decodeSilkSafely(payload []byte) (pcm []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			pcm = nil
			err = errors.New("SILK 解码器拒绝了异常负载")
		}
	}()
	return silk.DecodeSilkBuffToPcm(payload, voiceSampleRate)
}

func openVoiceTranscriptCache(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("语音转写缓存路径为空")
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
		CREATE TABLE IF NOT EXISTS voice_transcripts(
			evidence_id TEXT PRIMARY KEY,
			chat TEXT NOT NULL,
			server_id INTEGER NOT NULL,
			timestamp INTEGER NOT NULL,
			sort_key INTEGER NOT NULL,
			sender TEXT NOT NULL,
			transcript TEXT NOT NULL,
			audio_sha256 TEXT NOT NULL,
			engine TEXT NOT NULL,
			model TEXT NOT NULL,
			language TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'local_whisper_cpp',
			created_at TEXT NOT NULL
		)`); err != nil {
		_ = database.Close()
		return nil, err
	}
	if !columns(database, "voice_transcripts")["source"] {
		if _, err := database.Exec(`ALTER TABLE voice_transcripts ADD COLUMN source TEXT NOT NULL DEFAULT 'local_whisper_cpp'`); err != nil {
			_ = database.Close()
			return nil, err
		}
	}
	_ = os.Chmod(path, 0o600)
	return database, nil
}

func scanVoiceTranscript(scanner interface{ Scan(...any) error }) (VoiceTranscript, error) {
	var value VoiceTranscript
	err := scanner.Scan(
		&value.EvidenceID, &value.Chat, &value.ServerID, &value.Timestamp, &value.SortKey,
		&value.Sender, &value.Transcript, &value.AudioSHA256, &value.Engine, &value.Model,
		&value.Language, &value.Source, &value.CreatedAt,
	)
	return value, err
}

// LoadVoiceTranscript 读取一条私有暂存转写。
func LoadVoiceTranscript(path, evidenceID string) (VoiceTranscript, bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return VoiceTranscript{}, false, nil
	} else if err != nil {
		return VoiceTranscript{}, false, err
	}
	database, err := openVoiceTranscriptCache(path)
	if err != nil {
		return VoiceTranscript{}, false, err
	}
	defer database.Close()
	row := database.QueryRow(`SELECT evidence_id,chat,server_id,timestamp,sort_key,sender,transcript,
		audio_sha256,engine,model,language,source,created_at FROM voice_transcripts WHERE evidence_id=?`, evidenceID)
	value, err := scanVoiceTranscript(row)
	if err == sql.ErrNoRows {
		return VoiceTranscript{}, false, nil
	}
	return value, err == nil, err
}

// LoadVoiceTranscripts 一次读取已有的私有语音转写，避免消息列表逐条打开缓存数据库。
func LoadVoiceTranscripts(path string, evidenceIDs map[string]bool) (map[string]VoiceTranscript, error) {
	result := map[string]VoiceTranscript{}
	if len(evidenceIDs) == 0 {
		return result, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return result, nil
	} else if err != nil {
		return nil, err
	}
	database, err := openVoiceTranscriptCache(path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.Query(`SELECT evidence_id,chat,server_id,timestamp,sort_key,sender,transcript,
		audio_sha256,engine,model,language,source,created_at FROM voice_transcripts ORDER BY sort_key DESC,evidence_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		value, scanErr := scanVoiceTranscript(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if evidenceIDs[value.EvidenceID] {
			result[value.EvidenceID] = value
		}
	}
	return result, rows.Err()
}

// SaveVoiceTranscript 原子替换同一证据标识的转写；音频摘要随记录保存，便于避免错用旧结果。
func SaveVoiceTranscript(path string, value VoiceTranscript) error {
	digest, digestErr := hex.DecodeString(value.AudioSHA256)
	if strings.TrimSpace(value.EvidenceID) == "" || strings.TrimSpace(value.Transcript) == "" || digestErr != nil || len(digest) != sha256.Size {
		return errors.New("语音转写记录无效")
	}
	if value.CreatedAt == "" {
		value.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(value.Source) == "" {
		value.Source = "local_whisper_cpp"
	}
	database, err := openVoiceTranscriptCache(path)
	if err != nil {
		return err
	}
	defer database.Close()
	_, err = database.Exec(`INSERT INTO voice_transcripts(
		evidence_id,chat,server_id,timestamp,sort_key,sender,transcript,audio_sha256,engine,model,language,source,created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(evidence_id) DO UPDATE SET
		chat=excluded.chat,server_id=excluded.server_id,timestamp=excluded.timestamp,sort_key=excluded.sort_key,
		sender=excluded.sender,transcript=excluded.transcript,audio_sha256=excluded.audio_sha256,
		engine=excluded.engine,model=excluded.model,language=excluded.language,source=excluded.source,created_at=excluded.created_at`,
		value.EvidenceID, value.Chat, value.ServerID, value.Timestamp, value.SortKey, value.Sender,
		value.Transcript, value.AudioSHA256, value.Engine, value.Model, value.Language, value.Source, value.CreatedAt,
	)
	return err
}

// SearchVoiceTranscripts 在已暂存转写中执行大小写不敏感的子串搜索。
func SearchVoiceTranscripts(path, keyword, chat string, start, end *int64, limit int) ([]VoiceTranscript, error) {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return nil, errors.New("语音转写搜索关键词不能为空")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []VoiceTranscript{}, nil
	} else if err != nil {
		return nil, err
	}
	database, err := openVoiceTranscriptCache(path)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	query := `SELECT evidence_id,chat,server_id,timestamp,sort_key,sender,transcript,
		audio_sha256,engine,model,language,source,created_at FROM voice_transcripts`
	conditions := []string{}
	arguments := []any{}
	if chat != "" {
		conditions = append(conditions, "chat=?")
		arguments = append(arguments, chat)
	}
	if start != nil {
		conditions = append(conditions, "timestamp>=?")
		arguments = append(arguments, *start)
	}
	if end != nil {
		conditions = append(conditions, "timestamp<=?")
		arguments = append(arguments, *end)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY sort_key DESC,evidence_id DESC"
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []VoiceTranscript{}
	for rows.Next() {
		value, scanErr := scanVoiceTranscript(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if strings.Contains(strings.ToLower(value.Transcript), keyword) {
			result = append(result, value)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result, rows.Err()
}

// VoiceTranscriptCount 返回私有语音转写缓存的记录数，不存在缓存时返回零。
func VoiceTranscriptCount(path string) (int, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	database, err := openVoiceTranscriptCache(path)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	var count int
	err = database.QueryRow(`SELECT count(*) FROM voice_transcripts`).Scan(&count)
	return count, err
}
