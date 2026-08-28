package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zanescope/v-local-cli/internal/cryptoutil"
)

const maxChatImageBytes = 64 * 1024 * 1024
const maxChatImageHardlinkRows = 32
const maxChatImageResourceRows = 32
const maxChatImageRemoteParameterBytes = 4096

type ChatImage struct {
	EvidenceID                  string   `json:"evidence_id"`
	Chat                        string   `json:"chat"`
	LocalID                     int64    `json:"local_id"`
	ServerID                    int64    `json:"server_id,omitempty"`
	Timestamp                   int64    `json:"timestamp,omitempty"`
	SortKey                     int64    `json:"sort_key,omitempty"`
	Format                      string   `json:"format"`
	Bytes                       int64    `json:"bytes"`
	Width                       int      `json:"width"`
	Height                      int      `json:"height"`
	SHA256                      string   `json:"sha256"`
	VerifiedBy                  string   `json:"verified_by"`
	ResolutionStatus            string   `json:"resolution_status"`
	QualityTier                 string   `json:"quality_tier"`
	QualityBasis                string   `json:"quality_basis"`
	RemoteDescriptorStatus      string   `json:"remote_descriptor_status"`
	RemoteDescriptorParseStatus string   `json:"remote_descriptor_parse_status"`
	RemoteProtocolStatus        string   `json:"remote_protocol_status"`
	RemoteDescriptorTiers       []string `json:"remote_descriptor_tiers,omitempty"`
	HigherQualityLocalStatus    string   `json:"higher_quality_local_status"`
	HigherQualityDetectedFormat string   `json:"higher_quality_detected_format,omitempty"`
	HigherQualityRecoveryAction string   `json:"higher_quality_recovery_action"`
	Data                        []byte   `json:"-"`
}

// ChatImageResolutionError 只携带可公开的分类和计数，不包含微信源路径、
// CDN 描述符、密钥或候选内容。
type ChatImageResolutionError struct {
	Kind                        string
	DetectedFormat              string
	QualityTier                 string
	RemoteDescriptorStatus      string
	RemoteDescriptorParseStatus string
	RemoteProtocolStatus        string
	RemoteDescriptorTiers       []string
	CandidateCount              int
	ExistingCandidateCount      int
	cause                       error
}

func (err *ChatImageResolutionError) Error() string {
	return err.Kind
}

func (err *ChatImageResolutionError) Unwrap() error { return err.cause }

type chatImageRemoteDescriptor struct {
	status         string
	parseStatus    string
	protocolStatus string
	tiers          []string
	candidates     []chatImageRemoteCandidate
}

type chatImageRemoteCandidate struct {
	tier                    string
	encryptedQueryParameter string
	parameterEncoding       string
	aesKey                  [16]byte
	expectedBytes           int64
	expectedWidth           int
	expectedHeight          int
	expectedMD5             string
}

func chatImageRemoteCandidateHasBindingMetadata(candidate *chatImageRemoteCandidate) bool {
	if candidate == nil {
		return false
	}
	return candidate.expectedMD5 != "" ||
		(candidate.expectedBytes > 0 && candidate.expectedWidth > 0 && candidate.expectedHeight > 0)
}

func unknownChatImageRemoteDescriptor() chatImageRemoteDescriptor {
	return chatImageRemoteDescriptor{status: "unknown", parseStatus: "not_evaluated", protocolStatus: "not_evaluated"}
}

func missingChatImageRemoteDescriptor() chatImageRemoteDescriptor {
	return chatImageRemoteDescriptor{status: "missing", parseStatus: "not_applicable", protocolStatus: "not_applicable"}
}

func (descriptor *chatImageRemoteDescriptor) clear() {
	for index := range descriptor.candidates {
		clear(descriptor.candidates[index].aesKey[:])
	}
}

func chatImageFailure(kind string, descriptor chatImageRemoteDescriptor, cause error) *ChatImageResolutionError {
	return &ChatImageResolutionError{
		Kind: kind, RemoteDescriptorStatus: descriptor.status,
		RemoteDescriptorParseStatus: descriptor.parseStatus,
		RemoteProtocolStatus:        descriptor.protocolStatus,
		RemoteDescriptorTiers:       append([]string(nil), descriptor.tiers...), cause: cause,
	}
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
			return "", errors.New("图片消息资源数据库不可读")
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
		query += " ORDER BY rowid LIMIT ?"
		arguments = append(arguments, maxChatImageResourceRows+1)
		rows, queryErr := database.Query(query, arguments...)
		if queryErr != nil {
			_ = database.Close()
			return "", errors.New("图片消息资源查询失败")
		}
		matchedRows := 0
		for rows.Next() {
			matchedRows++
			if matchedRows > maxChatImageResourceRows {
				_ = rows.Close()
				_ = database.Close()
				return "", errors.New("图片消息资源映射超过上限")
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				_ = database.Close()
				return "", errors.New("图片消息资源行无效")
			}
			var packed []byte
			switch value := raw.(type) {
			case []byte:
				packed = append([]byte(nil), value...)
			case string:
				packed = []byte(value)
			default:
				_ = rows.Close()
				_ = database.Close()
				return "", errors.New("图片消息资源内容无效")
			}
			stem, stemErr := chatImageStem(packed)
			if stemErr != nil {
				_ = rows.Close()
				_ = database.Close()
				return "", errors.New("图片消息资源标识无效")
			}
			stems[stem] = true
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			_ = database.Close()
			return "", errors.New("图片消息资源读取失败")
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
	path        string
	qualityTier string
}

func chatImageQualityTier(fileName, stem string) string {
	name := strings.ToLower(filepath.Base(fileName))
	stem = strings.ToLower(stem)
	switch name {
	case stem + "_h.dat":
		return "high"
	case stem + ".dat":
		return "medium"
	case stem + "_t.dat":
		return "thumbnail"
	default:
		return "unknown"
	}
}

func chatImageQualityRank(value string) int {
	switch value {
	case "high":
		return 4
	case "medium":
		return 3
	case "thumbnail":
		return 2
	default:
		return 1
	}
}

func betterChatImageQuality(left, right string) string {
	if chatImageQualityRank(right) > chatImageQualityRank(left) {
		return right
	}
	return left
}

type chatImageCandidateState struct {
	validationFailed    map[string]bool
	decoderUnavailable  map[string]string
	unknownTierObserved bool
}

func newChatImageCandidateState() chatImageCandidateState {
	return chatImageCandidateState{
		validationFailed: map[string]bool{}, decoderUnavailable: map[string]string{},
	}
}

func (state chatImageCandidateState) higherQualityStatus(selectedTier string) (string, string, string) {
	selectedRank := chatImageQualityRank(selectedTier)
	if selectedTier == "unknown" {
		return "unknown", "", "manual_review"
	}
	if selectedTier == "high" {
		return "not_applicable", "", "none"
	}
	for _, tier := range []string{"high", "medium", "thumbnail"} {
		if chatImageQualityRank(tier) <= selectedRank {
			continue
		}
		if format := state.decoderUnavailable[tier]; format != "" {
			return "decoder_unavailable", format, "do_not_request_redownload_same_candidate"
		}
	}
	for _, tier := range []string{"high", "medium", "thumbnail"} {
		if chatImageQualityRank(tier) > selectedRank && state.validationFailed[tier] {
			return "validation_failed", "", "inspect_key_or_format_before_retry"
		}
	}
	if state.unknownTierObserved {
		return "unknown", "", "manual_review"
	}
	return "missing", "", "ask_user_to_open_original_then_refresh_and_retry"
}

func rawChatImageContent(root string, message Message) (string, error) {
	if strings.TrimSpace(message.SourceDB) == "" {
		return "", errors.New("图片消息缺少来源数据库")
	}
	path := filepath.Join(root, filepath.FromSlash(message.SourceDB))
	if !pathUnderRoot(path, root) {
		return "", errors.New("图片消息来源数据库越界")
	}
	database, err := openReadOnly(path)
	if err != nil {
		return "", errors.New("图片消息来源数据库不可读")
	}
	defer database.Close()
	table := messageTable(message.Chat)
	if !tableExists(database, table) {
		return "", errors.New("图片消息来源表不存在")
	}
	available := columns(database, table)
	selected := []string{}
	for _, name := range []string{"message_content", "compress_content", "WCDB_CT_message_content"} {
		if column := columnCI(available, name); column != "" {
			selected = append(selected, column)
		}
	}
	if len(selected) == 0 {
		return "", errors.New("图片消息没有可读正文列")
	}
	conditions := []string{}
	arguments := []any{}
	if column := columnCI(available, "local_id"); column != "" && message.LocalID != 0 {
		conditions = append(conditions, quoteIdentifier(column)+"=?")
		arguments = append(arguments, message.LocalID)
	}
	if column := columnCI(available, "server_id"); column != "" && message.ServerID != 0 {
		conditions = append(conditions, quoteIdentifier(column)+"=?")
		arguments = append(arguments, message.ServerID)
	}
	if column := columnCI(available, "local_type"); column != "" {
		conditions = append(conditions, "("+quoteIdentifier(column)+" & 4294967295)=3")
	}
	if len(arguments) == 0 {
		return "", errors.New("图片消息缺少可查询标识")
	}
	quoted := make([]string, len(selected))
	for index, name := range selected {
		quoted[index] = quoteIdentifier(name)
	}
	query := "SELECT " + strings.Join(quoted, ",") + " FROM " + quoteIdentifier(table) +
		" WHERE " + strings.Join(conditions, " AND ") + " ORDER BY rowid LIMIT 3"
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return "", errors.New("图片消息正文查询失败")
	}
	defer rows.Close()
	contents := map[string]bool{}
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
			fields[strings.ToLower(name)] = values[index]
		}
		encodingFlag := asInt64(fields[strings.ToLower(columnCI(available, "WCDB_CT_message_content"))])
		content := decodeValue(fields[strings.ToLower(columnCI(available, "message_content"))], encodingFlag)
		if strings.TrimSpace(content) == "" {
			content = decodeValue(fields[strings.ToLower(columnCI(available, "compress_content"))], encodingFlag)
		}
		_, content = parseSenderPrefix(content)
		if strings.TrimSpace(content) != "" {
			contents[content] = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", errors.New("图片消息正文读取失败")
	}
	if len(contents) != 1 {
		return "", errors.New("图片消息正文缺失或冲突")
	}
	for content := range contents {
		return content, nil
	}
	return "", errors.New("图片消息正文缺失")
}

func parseChatImageRemotePositiveInteger(value string, maximum int64) (int64, bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > maximum {
		return 0, true, false
	}
	// Desktop message XML commonly serializes an unknown optional dimension as
	// zero. Treat that sentinel like an omitted observation: it cannot bind a
	// response, but it is not evidence that the descriptor itself is malformed.
	if parsed == 0 {
		return 0, false, true
	}
	return parsed, true, true
}

func parseChatImageRemoteParameter(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 64 || len(value) > maxChatImageRemoteParameterBytes || len(value)%2 != 0 {
		return "", "", false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
			return "", "", false
		}
	}
	return value, "opaque_hex", true
}

func parseChatImageRemoteAESKey(value string) ([16]byte, bool, bool) {
	var result [16]byte
	value = strings.TrimSpace(value)
	if value == "" {
		return result, false, true
	}
	if len(value) != 32 {
		return result, true, false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(result) {
		clear(decoded)
		return result, true, false
	}
	copy(result[:], decoded)
	clear(decoded)
	return result, true, true
}

func chatImageRemoteMD5(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	return normalizedChatImageMD5(value)
}

func parseChatImageRemoteDescriptor(content string) chatImageRemoteDescriptor {
	node, err := parseMessageXML(content)
	if err != nil {
		return unknownChatImageRemoteDescriptor()
	}
	image := node.descendant("img")
	if image == nil {
		return missingChatImageRemoteDescriptor()
	}
	type variantDefinition struct {
		tier, parameterAttribute, keyAttribute, sizeAttribute, widthAttribute, heightAttribute, md5Attribute string
		fallbackKeyAttribute                                                                                 string
	}
	definitions := []variantDefinition{
		{tier: "high", parameterAttribute: "cdnbigimgurl", keyAttribute: "aeskey", sizeAttribute: "hdlength", widthAttribute: "cdnhdwidth", heightAttribute: "cdnhdheight", md5Attribute: "originsourcemd5"},
		{tier: "medium", parameterAttribute: "cdnmidimgurl", keyAttribute: "aeskey", sizeAttribute: "length", widthAttribute: "cdnmidwidth", heightAttribute: "cdnmidheight", md5Attribute: "md5"},
		{tier: "thumbnail", parameterAttribute: "cdnthumburl", keyAttribute: "cdnthumbaeskey", fallbackKeyAttribute: "aeskey", sizeAttribute: "cdnthumblength", widthAttribute: "cdnthumbwidth", heightAttribute: "cdnthumbheight"},
	}
	descriptor := chatImageRemoteDescriptor{
		status: "present_expiry_unknown", parseStatus: "present_incomplete", protocolStatus: "unverified_desktop_protocol",
	}
	incomplete, invalid := false, false
	for _, definition := range definitions {
		rawParameter := image.attribute(definition.parameterAttribute)
		if rawParameter == "" {
			continue
		}
		descriptor.tiers = append(descriptor.tiers, definition.tier)
		parameter, parameterEncoding, parameterValid := parseChatImageRemoteParameter(rawParameter)
		keyValue := image.attribute(definition.keyAttribute)
		if keyValue == "" && definition.fallbackKeyAttribute != "" {
			keyValue = image.attribute(definition.fallbackKeyAttribute)
		}
		key, keyPresent, keyValid := parseChatImageRemoteAESKey(keyValue)
		expectedBytes, _, bytesValid := parseChatImageRemotePositiveInteger(image.attribute(definition.sizeAttribute), maxChatImageBytes)
		expectedWidth, widthPresent, widthValid := parseChatImageRemotePositiveInteger(image.attribute(definition.widthAttribute), 100000)
		expectedHeight, heightPresent, heightValid := parseChatImageRemotePositiveInteger(image.attribute(definition.heightAttribute), 100000)
		expectedMD5, md5Valid := chatImageRemoteMD5(image.attribute(definition.md5Attribute))
		if !parameterValid || !keyValid {
			invalid = true
			clear(key[:])
			continue
		}
		if !keyPresent {
			incomplete = true
			continue
		}
		if !bytesValid {
			invalid = true
			expectedBytes = 0
		}
		if !widthValid || !heightValid || widthPresent != heightPresent {
			invalid = true
			expectedWidth, expectedHeight = 0, 0
		}
		if !md5Valid {
			invalid = true
			expectedMD5 = ""
		}
		candidate := chatImageRemoteCandidate{
			tier: definition.tier, encryptedQueryParameter: parameter, parameterEncoding: parameterEncoding,
			aesKey: key, expectedBytes: expectedBytes, expectedWidth: int(expectedWidth), expectedHeight: int(expectedHeight), expectedMD5: expectedMD5,
		}
		if !chatImageRemoteCandidateHasBindingMetadata(&candidate) {
			incomplete = true
			clear(candidate.aesKey[:])
			continue
		}
		descriptor.candidates = append(descriptor.candidates, candidate)
	}
	if len(descriptor.tiers) == 0 {
		return missingChatImageRemoteDescriptor()
	}
	switch {
	case len(descriptor.candidates) > 0 && (incomplete || invalid):
		descriptor.parseStatus = "parsed_partial_unverified_protocol"
	case len(descriptor.candidates) > 0:
		descriptor.parseStatus = "parsed_unverified_protocol"
	case invalid:
		descriptor.parseStatus = "present_invalid"
	default:
		descriptor.parseStatus = "present_incomplete"
	}
	return descriptor
}

func inspectChatImageRemoteDescriptor(root string, message Message) chatImageRemoteDescriptor {
	content, err := rawChatImageContent(root, message)
	if err != nil {
		return unknownChatImageRemoteDescriptor()
	}
	return parseChatImageRemoteDescriptor(content)
}

func normalizedChatImageMD5(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 {
		return "", false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return value, true
}

func chatImageCandidates(root, accountPath, stem, mediaMD5 string) ([]chatImageCandidate, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, err
	}
	normalizedMD5 := ""
	if strings.TrimSpace(mediaMD5) != "" {
		var valid bool
		normalizedMD5, valid = normalizedChatImageMD5(mediaMD5)
		if !valid {
			return nil, errors.New("图片消息媒体标识无效")
		}
	}
	result := []chatImageCandidate{}
	seen := map[string]bool{}
	mappedFileNames := map[string]bool{}
	hardlinkMatched := false
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "hardlink.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			return nil, errors.New("图片 hardlink 数据库不可读")
		}
		table := findTableCI(database, "image_hardlink_info_v4")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		fileColumn := columnCI(available, "file_name")
		dir1Column, dir2Column := columnCI(available, "dir1"), columnCI(available, "dir2")
		md5Column := columnCI(available, "md5")
		if fileColumn == "" || dir1Column == "" || dir2Column == "" {
			_ = database.Close()
			continue
		}
		directories := hardlinkDirectoryMap(database)
		query := "SELECT " + quoteIdentifier(fileColumn) + "," + quoteIdentifier(dir1Column) + "," + quoteIdentifier(dir2Column) + " FROM " + quoteIdentifier(table)
		arguments := []any{}
		if normalizedMD5 != "" {
			if md5Column == "" {
				_ = database.Close()
				continue
			}
			query += " WHERE lower(CAST(" + quoteIdentifier(md5Column) + " AS TEXT)) = ? ORDER BY rowid LIMIT ?"
			arguments = append(arguments, normalizedMD5, maxChatImageHardlinkRows+1)
		} else {
			query += " WHERE lower(CAST(" + quoteIdentifier(fileColumn) + " AS TEXT)) LIKE ? ORDER BY rowid LIMIT ?"
			arguments = append(arguments, stem+"%", maxChatImageHardlinkRows+1)
		}
		rows, queryErr := database.Query(query, arguments...)
		if queryErr != nil {
			_ = database.Close()
			return nil, errors.New("图片 hardlink 映射查询失败")
		}
		matchedRows := 0
		for rows.Next() {
			matchedRows++
			if matchedRows > maxChatImageHardlinkRows {
				_ = rows.Close()
				_ = database.Close()
				return nil, errors.New("图片 hardlink 精确映射超过上限")
			}
			var fileName any
			var dir1, dir2 int64
			if err := rows.Scan(&fileName, &dir1, &dir2); err != nil {
				_ = rows.Close()
				_ = database.Close()
				return nil, errors.New("图片 hardlink 映射行无效")
			}
			if normalizedResourceStem(filepath.Base(asString(fileName))) != stem {
				continue
			}
			hardlinkMatched = true
			mappedName := filepath.Base(asString(fileName))
			mappedFileNames[strings.ToLower(mappedName)] = true
			segments := []string{}
			for _, segment := range []string{directories[dir1], directories[dir2], mappedName} {
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
					result = append(result, chatImageCandidate{
						path: candidatePath, qualityTier: chatImageQualityTier(mappedName, stem),
					})
				}
			}
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			_ = database.Close()
			return nil, errors.New("图片 hardlink 映射读取失败")
		}
		_ = database.Close()
	}
	// 微信 4.1 的附件目录可能在 hardlink 映射层级外再包一层会话目录。
	// 只按已由消息资源与 hardlink 同时证明的精确文件名补扫，并保持有界。
	filesScanned := 0
	scanTruncated := false
	if !hardlinkMatched {
		return result, nil
	}
	for _, base := range hardlinkRoots(accountPath, "image") {
		walkErr := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			filesScanned++
			if filesScanned > maxMomentMediaFiles {
				scanTruncated = true
				return fs.SkipAll
			}
			if !mappedFileNames[strings.ToLower(entry.Name())] || !pathUnderRoot(path, base) {
				return nil
			}
			identity := strings.ToLower(filepath.Clean(path))
			if !seen[identity] {
				seen[identity] = true
				result = append(result, chatImageCandidate{
					path: path, qualityTier: chatImageQualityTier(entry.Name(), stem),
				})
			}
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			return nil, errors.New("图片 hardlink 补扫失败")
		}
		if scanTruncated {
			break
		}
	}
	if scanTruncated {
		return nil, errors.New("图片 hardlink 补扫达到上限")
	}
	return result, nil
}

// ResolveChatImage 通过消息资源标识和 hardlink 映射精确定位并验真聊天图片。
func ResolveChatImage(root, accountPath, evidenceID, aesKey string, xorKey int) (ChatImage, error) {
	message, err := FindImageMessage(root, evidenceID)
	if err != nil {
		return ChatImage{}, chatImageFailure("evidence_unavailable", unknownChatImageRemoteDescriptor(), err)
	}
	remoteDescriptor := inspectChatImageRemoteDescriptor(root, message)
	defer remoteDescriptor.clear()
	stem, err := imageResourceStem(root, message)
	if err != nil {
		return ChatImage{}, chatImageFailure("resource_descriptor_unavailable", remoteDescriptor, err)
	}
	candidates, err := chatImageCandidates(root, accountPath, stem, message.MediaMD5)
	if err != nil {
		failure := chatImageFailure("local_mapping_unavailable", remoteDescriptor, err)
		failure.CandidateCount = len(candidates)
		return ChatImage{}, failure
	}
	if len(candidates) == 0 {
		return ChatImage{}, chatImageFailure("local_mapping_unavailable", remoteDescriptor, errors.New("没有找到经过消息资源与 hardlink 双重关联的图片候选"))
	}
	verified := []ChatImage{}
	existingCandidates := 0
	unsupportedFormat := ""
	unsupportedQuality := "unknown"
	candidateState := newChatImageCandidateState()
	for _, candidate := range candidates {
		if candidate.qualityTier == "unknown" {
			candidateState.unknownTierObserved = true
		}
		info, statErr := os.Lstat(candidate.path)
		if statErr != nil {
			continue
		}
		existingCandidates++
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxChatImageBytes {
			candidateState.validationFailed[candidate.qualityTier] = true
			continue
		}
		raw, readErr := os.ReadFile(candidate.path)
		if readErr != nil {
			candidateState.validationFailed[candidate.qualityTier] = true
			continue
		}
		plain, format := raw, cryptoutil.ImageFormat(raw)
		if format == "unknown" {
			plain, format, readErr = cryptoutil.DecryptImageDAT(raw, aesKey, xorKey)
			if readErr != nil {
				candidateState.validationFailed[candidate.qualityTier] = true
				continue
			}
		}
		validation, validationErr := cryptoutil.ValidateImageStructure(plain)
		if validationErr != nil || validation.Format != format {
			if format == "wxgf" || format == "webp" {
				if current := candidateState.decoderUnavailable[candidate.qualityTier]; current == "" || format == "wxgf" {
					candidateState.decoderUnavailable[candidate.qualityTier] = format
				}
				if unsupportedFormat == "" || format == "wxgf" {
					unsupportedFormat = format
				}
				unsupportedQuality = betterChatImageQuality(unsupportedQuality, candidate.qualityTier)
			} else {
				candidateState.validationFailed[candidate.qualityTier] = true
			}
			continue
		}
		digest := sha256.Sum256(plain)
		verified = append(verified, ChatImage{
			EvidenceID: message.EvidenceID, Chat: message.Chat, LocalID: message.LocalID, ServerID: message.ServerID,
			Timestamp: message.Timestamp, SortKey: message.SortKey, Format: format, Bytes: int64(len(plain)),
			Width: validation.Width, Height: validation.Height, SHA256: hex.EncodeToString(digest[:]),
			VerifiedBy: "message_resource_stem+hardlink_map+full_decode", ResolutionStatus: "verified_local",
			QualityTier: candidate.qualityTier, QualityBasis: "hardlink_cache_filename_variant",
			RemoteDescriptorStatus: remoteDescriptor.status, RemoteDescriptorParseStatus: remoteDescriptor.parseStatus,
			RemoteProtocolStatus:  remoteDescriptor.protocolStatus,
			RemoteDescriptorTiers: append([]string(nil), remoteDescriptor.tiers...), Data: plain,
		})
	}
	if len(verified) == 0 {
		kind := "local_validation_failed"
		if existingCandidates == 0 {
			kind = "local_file_missing"
		} else if unsupportedFormat != "" {
			kind = "decoder_unavailable"
		}
		failure := chatImageFailure(kind, remoteDescriptor, errors.New("没有找到经过消息资源与 hardlink 双重关联的可验真图片"))
		failure.CandidateCount = len(candidates)
		failure.ExistingCandidateCount = existingCandidates
		failure.DetectedFormat = unsupportedFormat
		failure.QualityTier = unsupportedQuality
		return ChatImage{}, failure
	}
	// 同一条消息的 high/medium/thumbnail 通常经过不同缩放或编码，摘要不同并不
	// 构成证据冲突。先固定最高可验真的质量层级，再只比较该层级内的强候选；
	// 这样既能选择已落盘的 high 缓存档位，也继续对同层级的歧义 fail closed。
	selectedRank := 0
	for _, image := range verified {
		if rank := chatImageQualityRank(image.QualityTier); rank > selectedRank {
			selectedRank = rank
		}
	}
	unique := map[string]ChatImage{}
	for _, image := range verified {
		if chatImageQualityRank(image.QualityTier) != selectedRank {
			continue
		}
		unique[image.SHA256] = image
	}
	if len(unique) != 1 {
		failure := chatImageFailure("content_conflict", remoteDescriptor, fmt.Errorf("图片证据对应 %d 个不同内容候选", len(unique)))
		failure.CandidateCount = len(candidates)
		failure.ExistingCandidateCount = existingCandidates
		for _, image := range verified {
			failure.QualityTier = betterChatImageQuality(failure.QualityTier, image.QualityTier)
		}
		return ChatImage{}, failure
	}
	for _, image := range unique {
		image.HigherQualityLocalStatus, image.HigherQualityDetectedFormat, image.HigherQualityRecoveryAction = candidateState.higherQualityStatus(image.QualityTier)
		return image, nil
	}
	return ChatImage{}, errors.New("没有找到可验真图片")
}
