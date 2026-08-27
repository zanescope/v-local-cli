package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type retainedInt64 struct {
	Value   int64
	Present bool
}

type redPacketRecord struct {
	MessageServerID int64
	SessionName     string
	SenderUsername  string
	NativeURL       string
	SendID          string
	SceneID         retainedInt64
	PacketStatus    retainedInt64
	PacketType      retainedInt64
	ReceiveStatus   retainedInt64
	ReceiveTime     retainedInt64
	ReceiveAmount   retainedInt64
	SourceDB        string
}

type redPacketIndex struct {
	TableFound bool
	ByMessage  map[string]redPacketRecord
	ByNative   map[string][]redPacketRecord
	BySendID   map[string][]redPacketRecord
}

func newRedPacketIndex() redPacketIndex {
	return redPacketIndex{
		ByMessage: map[string]redPacketRecord{},
		ByNative:  map[string][]redPacketRecord{},
		BySendID:  map[string][]redPacketRecord{},
	}
}

func redPacketMessageKey(chat string, serverID int64) string {
	return strings.TrimSpace(chat) + "\x00" + fmt.Sprintf("%d", serverID)
}

func columnNameCI(available map[string]bool, candidates ...string) string {
	for _, candidate := range candidates {
		for name := range available {
			if strings.EqualFold(name, candidate) {
				return name
			}
		}
	}
	return ""
}

func quotedColumn(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func selectedColumn(available map[string]bool, alias string, candidates ...string) (string, bool) {
	name := columnNameCI(available, candidates...)
	if name == "" {
		return "NULL AS " + quotedColumn(alias), false
	}
	return quotedColumn(name) + " AS " + quotedColumn(alias), true
}

func scanRetainedInteger(value any, columnPresent bool) retainedInt64 {
	if !columnPresent || value == nil {
		return retainedInt64{}
	}
	return retainedInt64{Value: asInt64(value), Present: true}
}

// loadRedPacketIndex 读取可选的 general.db/redEnvelopeTable。不同微信版本保留的列
// 子集并不一致，因此适配器会动态探测列。可选表打开失败不会影响普通聊天记录查询。
func loadRedPacketIndex(root string) redPacketIndex {
	index := newRedPacketIndex()
	files, err := sqliteFiles(root)
	if err != nil {
		return index
	}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "general.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "redEnvelopeTable")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		selected := make([]string, 0, 11)
		present := map[string]bool{}
		for _, field := range []struct {
			alias      string
			candidates []string
		}{
			{"message_server_id", []string{"message_server_id", "msg_server_id", "server_id"}},
			{"session_name", []string{"session_name", "chat_name", "talker"}},
			{"sender_user_name", []string{"sender_user_name", "sender_username", "sender"}},
			{"native_url", []string{"native_url", "nativeurl", "mNativeUrl"}},
			{"send_id", []string{"send_id", "sendid"}},
			{"scene_id", []string{"scene_id", "sceneid"}},
			{"hb_status", []string{"hb_status", "hbStatus"}},
			{"hb_type", []string{"hb_type", "hbType"}},
			{"receive_status", []string{"receive_status", "receiveStatus"}},
			{"receive_time", []string{"receive_time", "receiveTime"}},
			{"receive_amount", []string{"receive_amount", "receiveAmount"}},
		} {
			expression, found := selectedColumn(available, field.alias, field.candidates...)
			selected = append(selected, expression)
			present[field.alias] = found
		}
		rows, queryErr := database.Query("SELECT " + strings.Join(selected, ",") + " FROM " + quotedColumn(table))
		if queryErr != nil {
			_ = database.Close()
			continue
		}
		index.TableFound = true
		relative, _ := filepath.Rel(root, path)
		for rows.Next() {
			values := make([]any, len(selected))
			targets := make([]any, len(values))
			for position := range values {
				targets[position] = &values[position]
			}
			if rows.Scan(targets...) != nil {
				continue
			}
			record := redPacketRecord{
				MessageServerID: asInt64(values[0]),
				SessionName:     asString(values[1]),
				SenderUsername:  asString(values[2]),
				NativeURL:       asString(values[3]),
				SendID:          asString(values[4]),
				SceneID:         scanRetainedInteger(values[5], present["scene_id"]),
				PacketStatus:    scanRetainedInteger(values[6], present["hb_status"]),
				PacketType:      scanRetainedInteger(values[7], present["hb_type"]),
				ReceiveStatus:   scanRetainedInteger(values[8], present["receive_status"]),
				ReceiveTime:     scanRetainedInteger(values[9], present["receive_time"]),
				ReceiveAmount:   scanRetainedInteger(values[10], present["receive_amount"]),
				SourceDB:        filepath.ToSlash(relative),
			}
			if record.SessionName != "" && record.MessageServerID != 0 {
				index.ByMessage[redPacketMessageKey(record.SessionName, record.MessageServerID)] = record
			}
			if record.NativeURL != "" {
				index.ByNative[record.NativeURL] = append(index.ByNative[record.NativeURL], record)
			}
			if record.SendID != "" {
				index.BySendID[record.SendID] = append(index.BySendID[record.SendID], record)
			}
		}
		_ = rows.Close()
		_ = database.Close()
	}
	return index
}

func exactRedPacketCandidate(records []redPacketRecord, chat string) (redPacketRecord, bool) {
	var matched []redPacketRecord
	for _, record := range records {
		if record.SessionName == "" || record.SessionName == chat {
			matched = append(matched, record)
		}
	}
	if len(matched) == 1 {
		return matched[0], true
	}
	return redPacketRecord{}, false
}

func (index redPacketIndex) match(message Message, details map[string]any) (redPacketRecord, bool) {
	if message.ServerID != 0 {
		if record, found := index.ByMessage[redPacketMessageKey(message.Chat, message.ServerID)]; found {
			return record, true
		}
	}
	if nativeURL := detailString(details, "native_url"); nativeURL != "" {
		if record, found := exactRedPacketCandidate(index.ByNative[nativeURL], message.Chat); found {
			return record, true
		}
	}
	if payMessageID := detailString(details, "pay_message_id"); payMessageID != "" {
		if record, found := exactRedPacketCandidate(index.BySendID[payMessageID], message.Chat); found {
			return record, true
		}
	}
	return redPacketRecord{}, false
}

func normalizeRetainedTimestamp(value int64) int64 {
	if value > 10_000_000_000 {
		return value / 1000
	}
	return value
}

func localDateTime(timestamp int64) (string, string) {
	if timestamp <= 0 {
		return "", ""
	}
	value := time.Unix(timestamp, 0).Local()
	return value.Format("2006-01-02 15:04:05"), value.Format("2006-01-02")
}

func receiveStatus(code int64) (string, string) {
	switch code {
	case 0:
		return "not_received", "未领取"
	case 2:
		return "received", "已领取"
	default:
		return "unknown", fmt.Sprintf("领取状态未知（码 %d）", code)
	}
}

func packetStatus(code int64) (string, string) {
	switch code {
	case 2:
		return "available", "可领取"
	case 4:
		return "fully_claimed", "已领完"
	case 5:
		return "expired", "已过期"
	default:
		return "unknown", fmt.Sprintf("红包状态未知（码 %d）", code)
	}
}

func redPacketSummaryStatus(redPacket map[string]any) string {
	label := detailString(redPacket, "receive_status_label")
	if label == "未领取" {
		switch detailString(redPacket, "packet_status") {
		case "fully_claimed":
			return "未领取·已领完"
		case "expired":
			return "未领取·已过期"
		}
	}
	return label
}

func enrichRedPacketMessage(message *Message, index redPacketIndex) {
	if message == nil || message.Kind != "redpacket" {
		return
	}
	if message.Details == nil {
		message.Details = map[string]any{}
	}
	redPacket, ok := message.Details["red_packet"].(map[string]any)
	if !ok || redPacket == nil {
		redPacket = map[string]any{}
		message.Details["red_packet"] = redPacket
	}
	if message.Timestamp > 0 {
		messageTime, messageDate := localDateTime(message.Timestamp)
		redPacket["message_timestamp"] = message.Timestamp
		redPacket["message_time"] = messageTime
		redPacket["message_date"] = messageDate
		redPacket["date_source"] = "message.create_time"
	}

	record, matched := index.match(*message, redPacket)
	if !index.TableFound {
		redPacket["receive_status"] = "not_retained"
		redPacket["receive_status_label"] = "领取状态未在本地记录中保留"
		redPacket["receive_status_source"] = "not_retained"
		redPacket["receive_time_status"] = "not_retained"
	} else if !matched {
		redPacket["receive_status"] = "unmatched"
		redPacket["receive_status_label"] = "未关联到本地领取状态记录"
		redPacket["receive_status_source"] = "unmatched"
		redPacket["receive_time_status"] = "unmatched"
	} else {
		redPacket["status_source_db"] = record.SourceDB
		redPacket["status_source_table"] = "redEnvelopeTable"
		setDetail(redPacket, "send_id", record.SendID)
		setDetail(redPacket, "sender_username", record.SenderUsername)
		if record.SceneID.Present {
			redPacket["scene_id"] = record.SceneID.Value
		}
		if record.PacketType.Present {
			redPacket["packet_type_code"] = record.PacketType.Value
		}
		if record.ReceiveStatus.Present {
			status, label := receiveStatus(record.ReceiveStatus.Value)
			redPacket["receive_status_code"] = record.ReceiveStatus.Value
			redPacket["receive_status"] = status
			redPacket["receive_status_label"] = label
			redPacket["receive_status_source"] = "redEnvelopeTable.receive_status"
		} else {
			redPacket["receive_status"] = "not_retained"
			redPacket["receive_status_label"] = "领取状态未在本地记录中保留"
			redPacket["receive_status_source"] = "not_retained"
		}
		if record.PacketStatus.Present {
			status, label := packetStatus(record.PacketStatus.Value)
			redPacket["packet_status_code"] = record.PacketStatus.Value
			redPacket["packet_status"] = status
			redPacket["packet_status_label"] = label
		}
		if record.ReceiveTime.Present && record.ReceiveTime.Value > 0 {
			timestamp := normalizeRetainedTimestamp(record.ReceiveTime.Value)
			receiveTime, receiveDate := localDateTime(timestamp)
			redPacket["receive_timestamp"] = timestamp
			redPacket["receive_time"] = receiveTime
			redPacket["receive_date"] = receiveDate
			redPacket["receive_time_status"] = "retained"
			redPacket["receive_time_source"] = "redEnvelopeTable.receive_time"
		} else {
			redPacket["receive_time_status"] = "not_retained"
		}
		if detailString(redPacket, "amount") == "" && record.ReceiveAmount.Present && record.ReceiveAmount.Value > 0 {
			redPacket["amount_minor_units"] = record.ReceiveAmount.Value
			redPacket["amount_currency"] = "CNY"
			redPacket["amount"] = fmt.Sprintf("¥%.2f", float64(record.ReceiveAmount.Value)/100)
			redPacket["amount_status"] = "retained"
			redPacket["amount_source"] = "redEnvelopeTable.receive_amount"
			redPacket["amount_kind"] = "received_amount"
		}
	}

	parts := []string{redPacketSummaryStatus(redPacket)}
	if amount := detailString(redPacket, "amount"); amount != "" {
		parts = append(parts, amount)
	} else if detailString(redPacket, "amount_status") == "not_retained" {
		parts = append(parts, "金额未在本地记录中保留")
	}
	if messageTime := detailString(redPacket, "message_time"); messageTime != "" {
		parts = append(parts, "消息时间："+messageTime)
	}
	parts = append(parts, firstNonEmpty(detailString(message.Details, "title"), detailString(message.Details, "description")))
	message.Content = composeSummary("红包", parts...)
}

func enrichRedPacketMessages(messages []Message, index redPacketIndex) {
	for position := range messages {
		enrichRedPacketMessage(&messages[position], index)
	}
}
