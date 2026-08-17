package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const maxChatImageBytes = 64 * 1024 * 1024

type ChatImage struct {
	EvidenceID string `json:"evidence_id"`
	Chat       string `json:"chat"`
	LocalID    int64  `json:"local_id"`
	ServerID   int64  `json:"server_id,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
	SortKey    int64  `json:"sort_key,omitempty"`
	Format     string `json:"format"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	VerifiedBy string `json:"verified_by"`
	Data       []byte `json:"-"`
}

func protobufVarint(payload []byte, offset *int) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if *offset >= len(payload) {
			return 0, errors.New("资源标识 protobuf 截断")
		}
		current := payload[*offset]
		*offset++
		value |= uint64(current&0x7f) << shift
		if current < 0x80 {
			return value, nil
		}
	}
	return 0, errors.New("资源标识 protobuf 无效")
}

func protobufBytesField(payload []byte, wanted uint64) ([]byte, error) {
	for offset := 0; offset < len(payload); {
		key, err := protobufVarint(payload, &offset)
		if err != nil {
			return nil, err
		}
		field, wire := key>>3, key&7
		switch wire {
		case 0:
			if _, err := protobufVarint(payload, &offset); err != nil {
				return nil, err
			}
		case 2:
			length, err := protobufVarint(payload, &offset)
			if err != nil || length > uint64(len(payload)-offset) {
				return nil, errors.New("资源标识 protobuf 长度无效")
			}
			value := payload[offset : offset+int(length)]
			offset += int(length)
			if field == wanted {
				return append([]byte(nil), value...), nil
			}
		case 1:
			offset += 8
		case 5:
			offset += 4
		default:
			return nil, errors.New("资源标识 protobuf 类型不受支持")
		}
		if offset > len(payload) {
			return nil, errors.New("资源标识 protobuf 越界")
		}
	}
	return nil, errors.New("资源标识字段缺失")
}

func chatImageStem(packed []byte) (string, error) {
	outer, err := protobufBytesField(packed, 2)
	if err != nil {
		return "", err
	}
	inner, err := protobufBytesField(outer, 1)
	if err != nil || len(inner) != 32 {
		return "", errors.New("图片资源标识格式无效")
	}
	value := strings.ToLower(string(inner))
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", errors.New("图片资源标识字符无效")
		}
	}
	return value, nil
}

func imageResourceStem(root string, message Message) (string, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return "", err
	}
	stems := map[string]bool{}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "message_resource.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "MessageResourceInfo")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		localIDColumn := columnCI(available, "message_local_id")
		localTypeColumn := columnCI(available, "message_local_type")
		serverIDColumn := columnCI(available, "message_svr_id")
		packedColumn := columnCI(available, "packed_info")
		if localIDColumn == "" || localTypeColumn == "" || packedColumn == "" {
			_ = database.Close()
			continue
		}
		query := "SELECT " + quoteIdentifier(packedColumn) + " FROM " + quoteIdentifier(table) + " WHERE " + quoteIdentifier(localIDColumn) + "=? AND (" + quoteIdentifier(localTypeColumn) + " & 4294967295)=3"
		arguments := []any{message.LocalID}
		if serverIDColumn != "" && message.ServerID != 0 {
			query += " AND " + quoteIdentifier(serverIDColumn) + "=?"
			arguments = append(arguments, message.ServerID)
		}
		rows, queryErr := database.Query(query, arguments...)
		if queryErr == nil {
			for rows.Next() {
				var raw any
				if rows.Scan(&raw) != nil {
					continue
				}
				var packed []byte
				switch value := raw.(type) {
				case []byte:
					packed = append([]byte(nil), value...)
				case string:
					packed = []byte(value)
				}
				stem, stemErr := chatImageStem(packed)
				if stemErr == nil {
					stems[stem] = true
				}
			}
			_ = rows.Close()
		}
		_ = database.Close()
	}
	if len(stems) == 0 {
		return "", errors.New("当前快照没有图片资源标识")
	}
	if len(stems) != 1 {
		return "", errors.New("图片资源标识存在冲突")
	}
	for stem := range stems {
		return stem, nil
	}
	return "", errors.New("当前快照没有图片资源标识")
}

type chatImageCandidate struct {
	path string
}

func chatImageCandidates(root, accountPath, stem string) ([]chatImageCandidate, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, err
	}
	result := []chatImageCandidate{}
	seen := map[string]bool{}
	hardlinkMatched := false
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "hardlink.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "image_hardlink_info_v4")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		fileColumn := columnCI(available, "file_name")
		dir1Column, dir2Column := columnCI(available, "dir1"), columnCI(available, "dir2")
		if fileColumn == "" || dir1Column == "" || dir2Column == "" {
			_ = database.Close()
			continue
		}
		directories := hardlinkDirectoryMap(database)
		rows, queryErr := database.Query("SELECT "+quoteIdentifier(fileColumn)+","+quoteIdentifier(dir1Column)+","+quoteIdentifier(dir2Column)+" FROM "+quoteIdentifier(table)+" WHERE lower(CAST("+quoteIdentifier(fileColumn)+" AS TEXT)) LIKE ? ORDER BY rowid", stem+"%")
		if queryErr == nil {
			for rows.Next() {
				var fileName any
				var dir1, dir2 int64
				if rows.Scan(&fileName, &dir1, &dir2) != nil || normalizedResourceStem(filepath.Base(asString(fileName))) != stem {
					continue
				}
				hardlinkMatched = true
				segments := []string{}
				for _, segment := range []string{directories[dir1], directories[dir2], filepath.Base(asString(fileName))} {
					if strings.TrimSpace(segment) != "" {
						segments = append(segments, segment)
					}
				}
				for _, base := range hardlinkRoots(accountPath, "image") {
					candidatePath := filepath.Join(append([]string{base}, segments...)...)
					if !pathUnderRoot(candidatePath, base) {
						continue
					}
					identity := strings.ToLower(filepath.Clean(candidatePath))
					if !seen[identity] {
						seen[identity] = true
						result = append(result, chatImageCandidate{path: candidatePath})
					}
				}
			}
			_ = rows.Close()
		}
		_ = database.Close()
	}
	// 微信 4.1 的附件目录可能在 hardlink 映射层级外再包一层会话目录。
	// 只按已由消息资源与 hardlink 同时证明的精确文件名补扫，并保持有界。
	filesScanned := 0
	if !hardlinkMatched {
		return result, nil
	}
	for _, base := range hardlinkRoots(accountPath, "image") {
		_ = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			filesScanned++
			if filesScanned > maxMomentMediaFiles {
				return fs.SkipAll
			}
			if normalizedResourceStem(entry.Name()) != stem || !pathUnderRoot(path, base) {
				return nil
			}
			identity := strings.ToLower(filepath.Clean(path))
			if !seen[identity] {
				seen[identity] = true
				result = append(result, chatImageCandidate{path: path})
			}
			return nil
		})
	}
	return result, nil
}

// ResolveChatImage 通过消息资源标识和 hardlink 映射精确定位并验真聊天图片。
func ResolveChatImage(root, accountPath, evidenceID, aesKey string, xorKey int) (ChatImage, error) {
	message, err := FindImageMessage(root, evidenceID)
	if err != nil {
		return ChatImage{}, err
	}
	stem, err := imageResourceStem(root, message)
	if err != nil {
		return ChatImage{}, err
	}
	candidates, err := chatImageCandidates(root, accountPath, stem)
	if err != nil {
		return ChatImage{}, err
	}
	verified := []ChatImage{}
	for _, candidate := range candidates {
		info, statErr := os.Lstat(candidate.path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxChatImageBytes {
			continue
		}
		raw, readErr := os.ReadFile(candidate.path)
		if readErr != nil {
			continue
		}
		plain, format := raw, cryptoutil.ImageFormat(raw)
		if format == "unknown" {
			plain, format, readErr = cryptoutil.DecryptImageDAT(raw, aesKey, xorKey)
			if readErr != nil {
				continue
			}
		}
		validation, validationErr := cryptoutil.ValidateImageStructure(plain)
		if validationErr != nil || validation.Format != format {
			continue
		}
		digest := sha256.Sum256(plain)
		verified = append(verified, ChatImage{
			EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID, ServerID: message.ServerID,
			Timestamp: message.Timestamp, SortKey: message.SortKey, Format: format, Bytes: int64(len(plain)),
			SHA256: hex.EncodeToString(digest[:]), VerifiedBy: "message_resource_stem+hardlink_map+full_decode", Data: plain,
		})
	}
	if len(verified) == 0 {
		return ChatImage{}, errors.New("没有找到经过消息资源与 hardlink 双重关联的可验真图片")
	}
	unique := map[string]ChatImage{}
	for _, image := range verified {
		unique[image.SHA256] = image
	}
	if len(unique) != 1 {
		return ChatImage{}, fmt.Errorf("图片证据对应 %d 个不同内容候选", len(unique))
	}
	for _, image := range unique {
		return image, nil
	}
	return ChatImage{}, errors.New("没有找到可验真图片")
}
