package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

type DirectionStats struct {
	Sent     int    `json:"sent"`
	Received int    `json:"received"`
	Unknown  int    `json:"unknown"`
	Basis    string `json:"basis"`
}

type MemberStats struct {
	Rank           int    `json:"rank"`
	Sender         string `json:"sender"`
	Username       string `json:"username"`
	Nickname       string `json:"nickname,omitempty"`
	Remark         string `json:"remark,omitempty"`
	ContactDisplay string `json:"contact_display,omitempty"`
	GroupNickname  string `json:"group_nickname,omitempty"`
	Display        string `json:"display"`
	SenderIdentity string `json:"sender_identity"`
	IsFromMe       bool   `json:"is_from_me"`
	Messages       int    `json:"messages"`
	MediaMessages  int    `json:"media_messages"`
	ActiveDays     int    `json:"active_days"`
	FirstTimestamp int64  `json:"first_timestamp,omitempty"`
	LastTimestamp  int64  `json:"last_timestamp,omitempty"`
}

type ChatStats struct {
	Chat                  string            `json:"chat"`
	ChatKind              string            `json:"chat_kind"`
	SourceDatabases       int               `json:"source_databases"`
	SourceRows            int               `json:"source_rows"`
	TotalMessages         int               `json:"total_messages"`
	SystemMessages        int               `json:"system_messages"`
	ActiveDays            int               `json:"active_days"`
	FirstTimestamp        int64             `json:"first_timestamp,omitempty"`
	LastTimestamp         int64             `json:"last_timestamp,omitempty"`
	FirstLocalTime        string            `json:"first_local_time,omitempty"`
	LastLocalTime         string            `json:"last_local_time,omitempty"`
	PeakHour              *int              `json:"peak_hour"`
	ByKind                map[string]int    `json:"by_kind"`
	ByCategory            map[string]int    `json:"by_category"`
	ByHour                [24]int           `json:"by_hour"`
	ByDate                map[string]int    `json:"by_date"`
	MediaMessages         int               `json:"media_messages"`
	ByMediaKind           map[string]int    `json:"by_media_kind"`
	Participants          int               `json:"participants,omitempty"`
	UnknownSenderMessages int               `json:"unknown_sender_messages,omitempty"`
	Members               []MemberStats     `json:"members,omitempty"`
	Direction             *DirectionStats   `json:"direction,omitempty"`
	Coverage              map[string]string `json:"statistic_basis"`
}

type memberAccumulator struct {
	Messages       int
	MediaMessages  int
	FirstTimestamp int64
	LastTimestamp  int64
	Dates          map[string]bool
	IsFromMe       bool
}

func messageKind(localType int64) string {
	value := uint64(localType)
	base := uint32(value & 0xffffffff)
	subtype := uint32(value >> 32)
	switch base {
	case 1:
		return "text"
	case 3:
		return "image"
	case 34:
		return "voice"
	case 42:
		return "card"
	case 43:
		return "video"
	case 47:
		return "sticker"
	case 48:
		return "location"
	case 50:
		return "voip"
	case 10000:
		return "system"
	case 49:
		switch subtype {
		case 3:
			return "music"
		case 5, 49:
			return "link"
		case 6, 8, 24:
			return "file"
		case 19:
			return "forward"
		case 33, 36:
			return "applet"
		case 51:
			return "channels"
		case 53:
			return "solitaire"
		case 57:
			return "quote"
		case 62:
			return "pat"
		case 87:
			return "announce"
		case 115:
			return "gift"
		case 2000:
			return "transfer"
		case 2001:
			return "redpacket"
		default:
			return "appmsg"
		}
	default:
		return "unknown"
	}
}

func messageCategory(kind string) string {
	switch kind {
	case "text":
		return "text"
	case "image":
		return "image"
	case "voice":
		return "voice"
	case "video":
		return "video"
	case "file":
		return "file"
	case "sticker":
		return "sticker"
	default:
		return "other"
	}
}

func isMediaKind(kind string) bool {
	switch kind {
	case "image", "voice", "video", "file", "sticker":
		return true
	default:
		return false
	}
}

func updateTimestamp(first, last *int64, timestamp int64) {
	if timestamp <= 0 {
		return
	}
	if *first == 0 || timestamp < *first {
		*first = timestamp
	}
	if timestamp > *last {
		*last = timestamp
	}
}

// Stats 只读取统计所需字段，不加载或返回消息正文。
func Stats(root, chat string, start, end *int64, top int) (ChatStats, error) {
	if strings.TrimSpace(chat) == "" {
		return ChatStats{}, errors.New("会话 username 不能为空")
	}
	result := ChatStats{
		Chat: chat, ChatKind: "private",
		ByKind: map[string]int{},
		ByCategory: map[string]int{
			"text": 0, "image": 0, "voice": 0, "video": 0,
			"file": 0, "sticker": 0, "other": 0,
		},
		ByDate: map[string]int{},
		ByMediaKind: map[string]int{
			"image": 0, "voice": 0, "video": 0, "file": 0, "sticker": 0,
		},
		Coverage: map[string]string{
			"source":       "local_plaintext_snapshot",
			"type_basis":   "local_type_base_and_packed_subtype",
			"sender_basis": "name2id_real_sender_id_with_private_fallback",
		},
	}
	if strings.HasSuffix(chat, "@chatroom") {
		result.ChatKind = "group"
	} else if strings.HasPrefix(chat, "gh_") {
		result.ChatKind = "official"
	}
	if result.ChatKind != "group" {
		result.Direction = &DirectionStats{Basis: "named_sender_with_legacy_rowid_fallback"}
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return ChatStats{}, err
	}
	table := messageTable(chat)
	dates := map[string]bool{}
	members := map[string]*memberAccumulator{}
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
		if !tableExists(database, table) {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		if (start != nil || end != nil) && !available["create_time"] {
			lastQueryError = errors.New("消息表缺少 create_time，无法应用时间范围")
			_ = database.Close()
			continue
		}
		selected := []string{"0 AS local_type", "0 AS create_time", "0 AS real_sender_id", "0 AS status"}
		if available["local_type"] {
			selected[0] = "local_type"
		}
		if available["create_time"] {
			selected[1] = "create_time"
		}
		if available["real_sender_id"] {
			selected[2] = "real_sender_id"
		}
		if available["status"] {
			selected[3] = "status"
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
		names := name2ID(database)
		rows, queryErr := database.Query(query, arguments...)
		if queryErr != nil {
			lastQueryError = queryErr
			_ = database.Close()
			continue
		}
		queried = true
		result.SourceDatabases++
		for rows.Next() {
			var localType, timestamp, senderID, status sql.NullInt64
			if rows.Scan(&localType, &timestamp, &senderID, &status) != nil {
				continue
			}
			result.SourceRows++
			kind := messageKind(localType.Int64)
			if kind == "system" {
				result.SystemMessages++
				continue
			}
			result.TotalMessages++
			result.ByKind[kind]++
			result.ByCategory[messageCategory(kind)]++
			media := isMediaKind(kind)
			if media {
				result.MediaMessages++
				result.ByMediaKind[kind]++
			}
			localDate := ""
			if timestamp.Int64 > 0 {
				local := time.Unix(timestamp.Int64, 0).In(time.Local)
				result.ByHour[local.Hour()]++
				localDate = local.Format("2006-01-02")
				result.ByDate[localDate]++
				dates[localDate] = true
				updateTimestamp(&result.FirstTimestamp, &result.LastTimestamp, timestamp.Int64)
			}
			sender := names[senderID.Int64]
			if result.ChatKind == "group" {
				if sender == "" {
					result.UnknownSenderMessages++
					continue
				}
				member := members[sender]
				if member == nil {
					member = &memberAccumulator{Dates: map[string]bool{}}
					members[sender] = member
				}
				member.Messages++
				member.IsFromMe = member.IsFromMe || status.Int64 == 2
				if media {
					member.MediaMessages++
				}
				if localDate != "" {
					member.Dates[localDate] = true
				}
				updateTimestamp(&member.FirstTimestamp, &member.LastTimestamp, timestamp.Int64)
			} else if sender == chat {
				result.Direction.Received++
			} else if sender != "" {
				result.Direction.Sent++
			} else if senderID.Int64 == 2 {
				result.Direction.Sent++
			} else {
				result.Direction.Received++
			}
		}
		if rowErr := rows.Err(); rowErr != nil {
			_ = rows.Close()
			_ = database.Close()
			return ChatStats{}, rowErr
		}
		_ = rows.Close()
		_ = database.Close()
	}
	if !queried && lastQueryError != nil {
		return ChatStats{}, lastQueryError
	}
	result.ActiveDays = len(dates)
	if result.FirstTimestamp > 0 {
		result.FirstLocalTime = time.Unix(result.FirstTimestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
		result.LastLocalTime = time.Unix(result.LastTimestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
	}
	peakCount := 0
	for hour, count := range result.ByHour {
		if count > peakCount {
			value := hour
			result.PeakHour = &value
			peakCount = count
		}
	}
	if result.ChatKind == "group" {
		identity := loadMessageIdentity(root, chat)
		result.Participants = len(members)
		for sender, value := range members {
			contact := identity.contacts[sender]
			groupNickname := identity.groupNicknames[sender]
			display := firstNonEmpty(groupNickname, contact.Display, contact.Nickname, sender)
			senderIdentity := "contact"
			if value.IsFromMe {
				senderIdentity = "self"
			}
			result.Members = append(result.Members, MemberStats{
				Sender: sender, Username: sender, Nickname: contact.Nickname, Remark: contact.Remark,
				ContactDisplay: contact.Display, GroupNickname: groupNickname, Display: display,
				SenderIdentity: senderIdentity, IsFromMe: value.IsFromMe,
				Messages: value.Messages, MediaMessages: value.MediaMessages,
				ActiveDays: len(value.Dates), FirstTimestamp: value.FirstTimestamp,
				LastTimestamp: value.LastTimestamp,
			})
		}
		sort.Slice(result.Members, func(left, right int) bool {
			if result.Members[left].Messages == result.Members[right].Messages {
				return result.Members[left].Sender < result.Members[right].Sender
			}
			return result.Members[left].Messages > result.Members[right].Messages
		})
		if top > 0 && len(result.Members) > top {
			result.Members = result.Members[:top]
		}
		for index := range result.Members {
			result.Members[index].Rank = index + 1
		}
	}
	return result, nil
}
