package store

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxAppMessageBytes  = 4_000_000
	maxForwardItems     = 500
	maxOfficialArticles = 100
)

var (
	messageMD5Attribute = regexp.MustCompile(`(?i)\bmd5=["']([0-9a-f]{32})["']`)
	messageMD5Element   = regexp.MustCompile(`(?is)<md5>\s*([0-9a-f]{32})\s*</md5>`)
	messageXMLMarkup    = regexp.MustCompile(`(?s)<[a-zA-Z/!]`)
	messageWhitespace   = regexp.MustCompile(`\s+`)
	voiceLengthPattern  = regexp.MustCompile(`(?i)voicelength=["'](\d+)["']`)
	emojiBracketPattern = regexp.MustCompile(`\[([^\[\]\r\n]{1,10})\]`)
	wechatEmoji         = map[string]string{
		"强": "👍", "弱": "👎", "玫瑰": "🌹", "呲牙": "😁", "捂脸": "🤦", "偷笑": "🤭",
		"旺柴": "🐶", "破涕为笑": "😂", "哇": "😮", "爱心": "❤️", "抱拳": "🙏", "庆祝": "🎉",
		"拥抱": "🤗", "OK": "👌", "拳头": "✊", "吃瓜": "🍉", "流泪": "😭", "色": "😍",
		"苦涩": "😣", "惊恐": "😱", "鼓掌": "👏", "太阳": "☀️", "机智": "🤓", "恐惧": "😨",
		"皱眉": "😟", "好的": "👌", "微笑": "😊", "666": "🙌", "月亮": "🌙", "可怜": "🥺",
		"发呆": "😳", "礼物": "🎁", "奸笑": "😏", "坏笑": "😏", "裂开": "🫠", "嘿哈": "😆",
		"抓狂": "😫", "红包": "🧧", "合十": "🙏", "让我看看": "👀", "胜利": "✌️", "烟花": "🎆",
		"跳跳": "😝", "害羞": "☺️", "憨笑": "😄", "耶": "✌️", "愉快": "☺️", "加油": "💪",
		"得意": "😎", "嘴唇": "💋", "咖啡": "☕", "尴尬": "😰", "亲亲": "😘", "汗": "😓",
		"撇嘴": "😖", "发抖": "🥶", "惊讶": "😲", "转圈": "💫", "调皮": "😜", "脸红": "😊",
		"衰": "😩", "握手": "🤝", "擦汗": "😥", "翻白眼": "🙄", "大哭": "😩", "白眼": "🙄",
		"笑脸": "😄", "天啊": "😱", "阴险": "😏", "福": "🧧", "困": "😪", "疑问": "❓",
		"囧": "😖", "难过": "🙁", "蛋糕": "🎂", "晕": "😵", "敲打": "👊", "爆竹": "🧨",
		"快哭了": "😢", "无语": "😑", "委屈": "🥺", "发怒": "😡", "心碎": "💔", "嘘": "🤫",
		"菜刀": "🔪", "闪电": "⚡", "睡": "😴", "咒骂": "🤬", "失望": "😞", "凋谢": "🥀",
		"吐": "🤮", "傲慢": "😤", "再见": "👋", "啤酒": "🍺", "炸弹": "💣", "怄火": "😡",
		"闭嘴": "🤐", "猪头": "🐷", "鄙视": "😒", "生病": "🤒", "便便": "💩", "骷髅": "💀",
		"流汗": "😓", "疯了": "🤪", "掩面": "🙈", "奋斗": "💪", "飞吻": "😘", "西瓜": "🍉",
		"碰拳": "👊", "无辜笑": "😇", "不看": "🙈", "叉号": "❌", "勾号": "✅", "点击": "👆",
		"100分": "💯", "火": "🔥", "泣不成声": "😭", "吐血": "🤮", "爱你": "🥰", "爱情": "💕",
		"笑哭R": "😂", "赞R": "👍", "偷笑R": "🤭", "红色心形R": "❤️", "给心心": "💗",
		"破涕為笑": "😂", "擁抱": "🤗", "難受": "😣", "尷尬": "😰", "愛心": "❤️", "慶祝": "🎉",
		"發呆": "😳", "親親": "😘", "Heart": "❤️", "Rose": "🌹", "Hug": "🤗", "Salute": "🙏",
		"ThumbsUp": "👍", "Chuckle": "🤭", "Grin": "😁", "NO": "🙅", "No": "🙅", "Yes": "🙆",
		"Speechless": "😑", "Concerned": "😟", "Whimper": "🥺", "Wilt": "🥀",
	}
)

type parsedMessageContent struct {
	BaseType        int64
	SubType         int64
	TypeLabel       string
	Kind            string
	Content         string
	Details         map[string]any
	ReplyTo         *MessageReply
	Mentions        []string
	VoiceDurationMS *int64
	MediaMD5        string
}

type messageXMLNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	Children []*messageXMLNode
	depth    int
}

// UnmarshalXML 与 xmlNode 同样需要自己计数嵌套层数，原因见 maxXMLDepth 的说明。
func (node *messageXMLNode) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	if node.depth >= maxXMLDepth {
		return errXMLTooDeep
	}
	node.Name = start.Name
	node.Attrs = append([]xml.Attr(nil), start.Attr...)
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			child := &messageXMLNode{depth: node.depth + 1}
			if err := decoder.DecodeElement(child, &value); err != nil {
				return err
			}
			node.Children = append(node.Children, child)
		case xml.CharData:
			node.Text += string(value)
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

func (node *messageXMLNode) child(name string) *messageXMLNode {
	if node == nil {
		return nil
	}
	for _, child := range node.Children {
		if strings.EqualFold(child.Name.Local, name) {
			return child
		}
	}
	return nil
}

func (node *messageXMLNode) descendant(name string) *messageXMLNode {
	if node == nil {
		return nil
	}
	if strings.EqualFold(node.Name.Local, name) {
		return node
	}
	for _, child := range node.Children {
		if found := child.descendant(name); found != nil {
			return found
		}
	}
	return nil
}

func (node *messageXMLNode) descendants(name string) []*messageXMLNode {
	if node == nil {
		return nil
	}
	result := []*messageXMLNode{}
	if strings.EqualFold(node.Name.Local, name) {
		result = append(result, node)
	}
	for _, child := range node.Children {
		result = append(result, child.descendants(name)...)
	}
	return result
}

func (node *messageXMLNode) path(names ...string) *messageXMLNode {
	current := node
	for _, name := range names {
		current = current.child(name)
		if current == nil {
			return nil
		}
	}
	return current
}

func (node *messageXMLNode) value() string {
	if node == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(node.Text))
}

func (node *messageXMLNode) attribute(name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attrs {
		if strings.EqualFold(attribute.Name.Local, name) {
			return strings.TrimSpace(html.UnescapeString(attribute.Value))
		}
	}
	return ""
}

func parseMessageXML(content string) (*messageXMLNode, error) {
	var root messageXMLNode
	decoder := xml.NewDecoder(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	decoder.Strict = true
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	return &root, nil
}

func splitMessageType(localType int64) (int64, int64) {
	value := uint64(localType)
	return int64(uint32(value)), int64(uint32(value >> 32))
}

func kindForMessageType(baseType, subType int64) string {
	switch baseType {
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
		switch subType {
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

func messageTypeLabel(localType, baseType int64) string {
	knownLarge := map[int64]string{
		244813135921: "引用消息", 266287972401: "拍一拍", 81604378673: "聊天记录",
		154618822705: "小程序", 8594229559345: "红包", 8589934592049: "转账",
		34359738417: "文件", 103079215153: "文件", 25769803825: "文件",
	}
	if label := knownLarge[localType]; label != "" {
		return label
	}
	labels := map[int64]string{
		1: "文本", 3: "图片", 34: "语音", 42: "名片", 43: "视频",
		47: "动画表情", 48: "位置", 49: "链接/应用消息", 50: "通话", 10000: "系统",
	}
	if label := labels[baseType]; label != "" {
		return label
	}
	return fmt.Sprintf("type=%d", localType)
}

func parseSenderPrefix(content string) (string, string) {
	parts := strings.SplitN(content, ":\n", 2)
	if len(parts) != 2 {
		return "", content
	}
	head := strings.TrimSpace(parts[0])
	if strings.HasPrefix(head, "wxid_") || !strings.Contains(head, "@chatroom") {
		return head, parts[1]
	}
	return "", content
}

func parseMessageContent(localType int64, rawContent, source string) parsedMessageContent {
	baseType, packedSubType := splitMessageType(localType)
	details, xmlSubType, appMessage := parseAppMessage(rawContent)
	subType := packedSubType
	if xmlSubType > 0 {
		subType = xmlSubType
	}
	kind := kindForMessageType(baseType, subType)
	mediaMD5 := contentMediaMD5(rawContent)
	parsed := parsedMessageContent{
		BaseType: baseType, SubType: subType, TypeLabel: messageTypeLabel(localType, baseType),
		Kind: kind, Details: details, MediaMD5: mediaMD5,
	}
	parsed.ReplyTo = parseMessageReply(rawContent)
	parsed.Mentions = parseMessageMentions(source)

	switch kind {
	case "text":
		parsed.Content = normalizeEmojiText(strings.TrimSpace(rawContent))
	case "image":
		parsed.Content = mediaPlaceholder("图片", mediaMD5)
	case "video":
		parsed.Content = mediaPlaceholder("视频", mediaMD5)
	case "sticker":
		parsed.Content = mediaPlaceholder("表情", mediaMD5)
	case "card":
		parsed.Details = parseContactCard(rawContent)
		parsed.Content = composeSummary("微信名片", detailString(parsed.Details, "nickname"), detailString(parsed.Details, "username"))
	case "voice":
		if match := voiceLengthPattern.FindStringSubmatch(rawContent); len(match) == 2 {
			if duration, err := strconv.ParseInt(match[1], 10, 64); err == nil {
				parsed.VoiceDurationMS = &duration
				parsed.Content = fmt.Sprintf("[语音 %.1f 秒]", float64(duration)/1000)
			}
		}
		if parsed.Content == "" {
			parsed.Content = "[语音]"
		}
	case "location":
		parsed.Content = formatLocation(rawContent)
	case "voip":
		parsed.Content = formatVoIP(rawContent)
	case "system":
		if messageXMLMarkup.MatchString(rawContent) {
			parsed.Content = "[系统消息]"
		} else {
			parsed.Content = strings.TrimSpace(rawContent)
		}
	default:
		if appMessage || baseType == 49 {
			parsed.Content = renderAppMessage(kind, subType, details)
		} else if label := placeholderForKind(kind); label != "" {
			parsed.Content = label
		} else {
			parsed.Content = strings.TrimSpace(fmt.Sprintf("[%s] %s", parsed.TypeLabel, rawContent))
		}
	}
	return parsed
}

func normalizeEmojiText(value string) string {
	return emojiBracketPattern.ReplaceAllStringFunc(value, func(token string) string {
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token, "["), "]"))
		if normalized := wechatEmoji[name]; normalized != "" {
			return normalized
		}
		return token
	})
}

func parseAppMessage(content string) (map[string]any, int64, bool) {
	if !strings.Contains(content, "<appmsg") && !strings.Contains(content, "&lt;appmsg") {
		return nil, 0, false
	}
	if len([]byte(content)) > maxAppMessageBytes {
		return map[string]any{"parse_error": "too_large"}, 0, true
	}
	upper := strings.ToUpper(content)
	if strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY") {
		return map[string]any{"parse_error": "unsafe_doctype"}, 0, true
	}
	raw := content
	if strings.Contains(raw, "&lt;appmsg") {
		raw = html.UnescapeString(raw)
	}
	root, err := parseMessageXML(raw)
	if err != nil {
		details := parseAppMessageFallback(raw)
		// 超深嵌套仍降级到正则提取，但要显式标注原因，避免和普通格式错误混为一谈。
		if errors.Is(err, errXMLTooDeep) {
			details["parse_error"] = "too_deep"
		}
		return details, detailInteger(details, "appmsg_type"), true
	}
	app := root.descendant("appmsg")
	if app == nil {
		return map[string]any{"parse_error": "appmsg_missing"}, 0, true
	}
	appType := parseInteger(app.child("type").value())
	details := map[string]any{}
	setDetail(details, "appmsg_type", appType)
	setDetail(details, "title", app.child("title").value())
	setDetail(details, "description", app.child("des").value())
	setDetail(details, "url", app.child("url").value())
	setDetail(details, "low_url", app.child("lowurl").value())
	setDetail(details, "thumbnail_url", app.child("thumburl").value())
	setDetail(details, "app_id", app.attribute("appid"))
	setDetail(details, "pay_subtype", parseInteger(app.descendant("paysubtype").value()))
	setDetail(details, "pay_memo", app.descendant("pay_memo").value())
	parseWeAppInfo(app, details)
	parseFinderInfo(app, details)
	parseWCPayInfo(app, details)

	if appType == 6 || appType == 8 || appType == 24 {
		attachment := app.descendant("appattach")
		fileMD5 := firstNonEmpty(attachment.child("filemd5").value(), attachment.child("md5").value(), app.descendant("filemd5").value())
		if !validMD5(fileMD5) {
			fileMD5 = ""
		}
		setDetail(details, "file_name", app.child("title").value())
		setDetail(details, "file_size_bytes", parseInteger(attachment.child("totallen").value()))
		setDetail(details, "file_extension", strings.TrimPrefix(attachment.child("fileext").value(), "."))
		setDetail(details, "file_md5", strings.ToLower(fileMD5))
		setDetail(details, "file_download_url", attachment.child("cdnattachurl").value())
	}

	sourceName := firstNonEmpty(app.child("sourcedisplayname").value(), app.path("appinfo", "appname").value())
	sourceUsername := app.child("sourceusername").value()
	if sourceName != "" || sourceUsername != "" {
		source := map[string]any{}
		setDetail(source, "name", sourceName)
		setDetail(source, "username", sourceUsername)
		details["source"] = source
	}
	parseOfficialArticles(app, details)
	if recordNode := app.child("recorditem"); recordNode != nil {
		if record := strings.TrimSpace(recordNode.Text); record != "" {
			parseForwardRecord(record, details)
		}
	}
	return details, appType, true
}

func parseContactCard(content string) map[string]any {
	details := map[string]any{}
	root, err := parseMessageXML(content)
	if err != nil {
		details["parse_error"] = "malformed_xml"
		return details
	}
	card := root
	if !strings.EqualFold(card.Name.Local, "msg") {
		card = root.descendant("msg")
	}
	if card == nil {
		details["parse_error"] = "card_missing"
		return details
	}
	for source, target := range map[string]string{
		"username": "username", "nickname": "nickname", "alias": "alias", "province": "province",
		"city": "city", "regionCode": "region_code", "sex": "sex", "sign": "signature",
		"smallheadimgurl": "small_avatar_url", "bigheadimgurl": "large_avatar_url",
		"certflag": "certification_flag", "certinfo": "certification_info", "brandType": "brand_type",
		"brandFlags": "brand_flags", "brandIconUrl": "brand_icon_url", "brandHomeUrl": "brand_home_url",
		"brandSubscriptConfigUrl": "brand_subscription_config_url", "biznamecardinfo": "business_card_info",
	} {
		setDetail(details, target, card.attribute(source))
	}
	return details
}

func parseWeAppInfo(app *messageXMLNode, details map[string]any) {
	node := app.child("weappinfo")
	if node == nil {
		return
	}
	miniProgram := map[string]any{}
	setDetail(miniProgram, "app_id", node.child("appid").value())
	setDetail(miniProgram, "username", node.child("username").value())
	setDetail(miniProgram, "page_path", node.child("pagepath").value())
	setDetail(miniProgram, "version", parseInteger(node.child("version").value()))
	setDetail(miniProgram, "service_type", parseInteger(node.child("appservicetype").value()))
	setDetail(miniProgram, "sub_type", parseInteger(node.child("subType").value()))
	setDetail(miniProgram, "share_id", node.child("shareId").value())
	setDetail(miniProgram, "icon_url", node.child("weappiconurl").value())
	setDetail(miniProgram, "page_thumbnail_url", node.child("weapppagethumbrawurl").value())
	setDetail(miniProgram, "brand_official", parseInteger(node.child("brandofficialflag").value()))
	if len(miniProgram) > 0 {
		details["mini_program"] = miniProgram
	}
}

func parseFinderInfo(app *messageXMLNode, details map[string]any) {
	feed := app.child("finderFeed")
	nameCard := app.child("findernamecard")
	if feed == nil && nameCard == nil {
		return
	}
	channels := map[string]any{}
	setDetail(channels, "share_url", app.child("url").value())
	if feed != nil {
		setDetail(channels, "object_id", feed.child("objectId").value())
		setDetail(channels, "object_nonce_id", feed.child("objectNonceId").value())
		setDetail(channels, "username", feed.child("username").value())
		setDetail(channels, "nickname", feed.child("nickname").value())
		setDetail(channels, "description", feed.child("desc").value())
		setDetail(channels, "feed_type", parseInteger(feed.child("feedType").value()))
		setDetail(channels, "live_id", feed.child("liveId").value())
		setDetail(channels, "business_username", firstNonEmpty(feed.child("bizUsernameV2").value(), feed.child("bizUsername").value()))
		setDetail(channels, "business_nickname", feed.child("bizNickname").value())
		mediaItems := []map[string]any{}
		for _, media := range feed.descendants("media") {
			item := map[string]any{}
			setDetail(item, "media_type", parseInteger(media.child("mediaType").value()))
			setDetail(item, "url", media.child("url").value())
			setDetail(item, "thumbnail_url", media.child("thumbUrl").value())
			setDetail(item, "cover_url", firstNonEmpty(media.child("fullCoverUrl").value(), media.child("coverUrl").value()))
			setDetail(item, "title", media.child("title").value())
			setDetail(item, "width", parseInteger(media.child("width").value()))
			setDetail(item, "height", parseInteger(media.child("height").value()))
			setDetail(item, "duration_seconds", parseInteger(media.child("videoPlayDuration").value()))
			if len(item) > 0 {
				mediaItems = append(mediaItems, item)
			}
		}
		if len(mediaItems) > 0 {
			channels["media"] = mediaItems
		}
	}
	if nameCard != nil {
		nameCardDetails := map[string]any{}
		setDetail(nameCardDetails, "username", nameCard.child("username").value())
		setDetail(nameCardDetails, "nickname", nameCard.child("nickname").value())
		setDetail(nameCardDetails, "avatar_url", nameCard.child("avatar").value())
		setDetail(nameCardDetails, "authentication_job", nameCard.child("auth_job").value())
		setDetail(nameCardDetails, "authentication_icon_url", nameCard.child("auth_icon_url").value())
		channels["name_card"] = nameCardDetails
	}
	details["channels"] = channels
}

func parseWCPayInfo(app *messageXMLNode, details map[string]any) {
	node := app.child("wcpayinfo")
	if node == nil {
		return
	}
	redPacket := map[string]any{}
	setDetail(redPacket, "pay_message_id", node.child("paymsgid").value())
	setDetail(redPacket, "native_url", node.child("nativeurl").value())
	setDetail(redPacket, "scene_id", parseInteger(node.child("sceneid").value()))
	setDetail(redPacket, "inner_type", parseInteger(node.child("innertype").value()))
	setDetail(redPacket, "sender_title", node.child("sendertitle").value())
	setDetail(redPacket, "receiver_title", node.child("receivertitle").value())
	setDetail(redPacket, "sender_description", node.child("senderdes").value())
	setDetail(redPacket, "receiver_description", node.child("receiverdes").value())
	setDetail(redPacket, "exclusive_receiver_username", node.child("exclusive_recv_username").value())
	amount := ""
	amountSource := ""
	amountKind := ""
	for _, candidate := range []struct {
		value  string
		source string
		kind   string
	}{
		{node.child("redenvelopereceiveamount").value(), "message_xml.redenvelopereceiveamount", "received_amount"},
		{node.child("amount").value(), "message_xml.amount", "card_amount"},
		{node.child("totalamount").value(), "message_xml.totalamount", "total_amount"},
	} {
		if strings.TrimSpace(candidate.value) != "" {
			amount = candidate.value
			amountSource = candidate.source
			amountKind = candidate.kind
			break
		}
	}
	if amountMinor := parseInteger(amount); amountMinor > 0 {
		redPacket["amount_minor_units"] = amountMinor
		redPacket["amount_currency"] = "CNY"
		redPacket["amount"] = fmt.Sprintf("¥%.2f", float64(amountMinor)/100)
		redPacket["amount_status"] = "retained"
		redPacket["amount_source"] = amountSource
		redPacket["amount_kind"] = amountKind
	} else {
		redPacket["amount_status"] = "not_retained"
	}
	details["red_packet"] = redPacket
}

func parseAppMessageFallback(content string) map[string]any {
	body := content
	if match := regexp.MustCompile(`(?is)<appmsg(?:\s[^>]*)?>(.*?)</appmsg>`).FindStringSubmatch(content); len(match) == 2 {
		body = match[1]
	}
	details := map[string]any{}
	setDetail(details, "appmsg_type", parseInteger(xmlTagText(body, "type")))
	setDetail(details, "title", xmlTagText(body, "title"))
	setDetail(details, "description", xmlTagText(body, "des"))
	setDetail(details, "url", xmlTagText(body, "url"))
	if len(details) == 0 {
		details["parse_error"] = "malformed_xml"
	} else {
		details["parse_warning"] = "malformed_xml_fallback"
	}
	return details
}

func xmlTagText(content, tag string) string {
	pattern := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(tag) + `(?:\s[^>]*)?>(.*?)</` + regexp.QuoteMeta(tag) + `>`)
	match := pattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return ""
	}
	value := strings.TrimSpace(match[1])
	if strings.HasPrefix(value, "<![CDATA[") && strings.HasSuffix(value, "]]>") {
		value = value[9 : len(value)-3]
	}
	return strings.TrimSpace(html.UnescapeString(value))
}

func parseOfficialArticles(app *messageXMLNode, details map[string]any) {
	mmReader := app.descendant("mmreader")
	category := mmReader.descendant("category")
	if category == nil {
		return
	}
	publisherName := mmReader.path("publisher", "nickname").value()
	publisherUsername := mmReader.path("publisher", "username").value()
	items := category.childNodes("item")
	articles := []map[string]any{}
	for index, item := range items {
		if index >= maxOfficialArticles {
			break
		}
		article := map[string]any{"position": index + 1}
		title := firstChildValue(item, "title", "title_v2", "text_title", "itemtitle")
		description := firstChildValue(item, "digest", "des", "summary")
		url := firstChildValue(item, "url", "longurl", "shorturl")
		thumbnail := firstPathValue(item, []string{"cover"}, []string{"share_cover", "cdn_url"}, []string{"cover_16_9"}, []string{"cover_1_1"}, []string{"cover_3_4"}, []string{"cover_url"}, []string{"thumburl"})
		author := firstPathValue(item, []string{"author"}, []string{"sources", "source", "name"}, []string{"sourcename"})
		setDetail(article, "title", title)
		setDetail(article, "description", description)
		setDetail(article, "url", url)
		setDetail(article, "thumbnail_url", thumbnail)
		setDetail(article, "author", firstNonEmpty(author, publisherName))
		setDetail(article, "timestamp", parseInteger(firstChildValue(item, "pub_time", "pubtime", "publish_time")))
		setDetail(article, "item_show_type", parseInteger(item.child("itemshowtype").value()))
		setDetail(article, "delete_flag", parseInteger(item.child("del_flag").value()))
		if title != "" || description != "" || url != "" {
			articles = append(articles, article)
		}
	}
	declared := parseInteger(category.attribute("count"))
	if declared == 0 {
		declared = int64(len(items))
	}
	details["article_count"] = declared
	details["articles"] = articles
	if publisherName != "" || publisherUsername != "" {
		publisher := map[string]any{}
		setDetail(publisher, "name", publisherName)
		setDetail(publisher, "username", publisherUsername)
		details["publisher"] = publisher
	}
	if len(items) > maxOfficialArticles {
		details["parsed_article_count"] = len(articles)
		details["articles_truncated"] = true
	}
	if len(articles) > 0 {
		first := articles[0]
		for _, key := range []string{"title", "description", "url", "thumbnail_url"} {
			if details[key] == nil || details[key] == "" {
				setDetail(details, key, first[key])
			}
		}
	}
}

func (node *messageXMLNode) childNodes(name string) []*messageXMLNode {
	if node == nil {
		return nil
	}
	result := []*messageXMLNode{}
	for _, child := range node.Children {
		if strings.EqualFold(child.Name.Local, name) {
			result = append(result, child)
		}
	}
	return result
}

func parseForwardRecord(record string, details map[string]any) {
	raw := strings.TrimPrefix(strings.TrimSpace(record), "\ufeff")
	root, err := parseMessageXML(raw)
	if err != nil {
		root, err = parseMessageXML(html.UnescapeString(raw))
	}
	if err != nil {
		details["item_count"] = 0
		details["items"] = []map[string]any{}
		details["forward_parse_error"] = true
		return
	}
	nodes := root.descendants("dataitem")
	items := []map[string]any{}
	for index, node := range nodes {
		if index >= maxForwardItems {
			break
		}
		items = append(items, parseForwardItem(node))
	}
	count := int64(len(nodes))
	if datalist := root.descendant("datalist"); datalist != nil {
		if declared := parseInteger(datalist.attribute("count")); declared > 0 {
			count = declared
		}
	}
	details["item_count"] = count
	details["items"] = items
	if len(nodes) > maxForwardItems {
		details["parsed_item_count"] = len(items)
		details["truncated"] = true
	}
}

func parseForwardItem(node *messageXMLNode) map[string]any {
	dataType := parseInteger(node.attribute("datatype"))
	kinds := map[int64]string{1: "text", 2: "image", 3: "voice", 4: "video", 5: "link", 6: "location", 7: "music", 8: "file", 14: "forward", 29: "music"}
	kind := kinds[dataType]
	if kind == "" {
		kind = "unknown"
	}
	source := node.child("source")
	senderUsername := firstPathValue(source, []string{"realchatname"}, []string{"fromusr"})
	sender := firstNonEmpty(node.child("sourcename").value(), source.child("fromusrname").value(), source.child("sourcename").value(), senderUsername)
	web := node.child("weburlitem")
	title := firstNonEmpty(node.child("datatitle").value(), web.child("title").value())
	description := firstNonEmpty(node.child("datadesc").value(), web.child("desc").value())
	url := firstNonEmpty(web.child("link").value(), web.child("url").value(), node.child("dataurl").value())
	content := description
	if kind != "text" {
		content = firstNonEmpty(title, description)
	}
	if content == "" {
		labels := map[string]string{"image": "图片", "voice": "语音", "video": "视频", "link": "链接", "location": "位置", "music": "音乐", "file": "文件", "forward": "聊天记录", "unknown": "消息"}
		content = "[" + labels[kind] + "]"
	}
	item := map[string]any{"data_type": dataType, "kind": kind, "content": normalizeEmojiText(content)}
	setDetail(item, "sender", sender)
	setDetail(item, "sender_username", senderUsername)
	rawTime := firstNonEmpty(node.child("sourcetime").value(), source.child("createtime").value(), source.child("sourcetime").value())
	if timestamp := parseInteger(rawTime); timestamp > 0 {
		if timestamp > 10_000_000_000 {
			timestamp /= 1000
		}
		item["timestamp"] = timestamp
		item["time"] = time.Unix(timestamp, 0).Format("2006-01-02 15:04:05")
	}
	setDetail(item, "title", title)
	setDetail(item, "description", description)
	setDetail(item, "url", url)
	return item
}

func renderAppMessage(kind string, appType int64, details map[string]any) string {
	text := detailString(details, "title")
	if text == "" {
		text = detailString(details, "description")
	}
	switch kind {
	case "link":
		parts := []string{firstNonEmpty(oneLine(detailString(details, "title")), clipped(detailString(details, "description"), 120), oneLine(detailString(details, "url")))}
		if source, ok := details["source"].(map[string]any); ok {
			if name := oneLine(detailString(source, "name")); name != "" {
				parts = append(parts, "来源："+name)
			}
		}
		if description := clipped(detailString(details, "description"), 120); description != "" && description != oneLine(detailString(details, "title")) {
			parts = append(parts, description)
		}
		return composeSummary("链接", parts...)
	case "forward":
		count := detailInteger(details, "item_count")
		label := "聊天记录"
		if count > 0 {
			label = fmt.Sprintf("聊天记录·%d条", count)
		}
		previews := []string{}
		if items, ok := details["items"].([]map[string]any); ok {
			for index, item := range items {
				if index >= 3 {
					break
				}
				content := clipped(detailString(item, "content"), 48)
				if sender := oneLine(detailString(item, "sender")); sender != "" && content != "" {
					content = sender + "：" + content
				}
				if content != "" {
					previews = append(previews, content)
				}
			}
		}
		if len(previews) == 0 {
			previews = append(previews, text, detailString(details, "description"))
		}
		return composeSummary(label, previews...)
	case "file":
		parts := []string{firstNonEmpty(detailString(details, "file_name"), text)}
		if size := detailInteger(details, "file_size_bytes"); size >= 0 && details["file_size_bytes"] != nil {
			parts = append(parts, formatByteSize(size))
		}
		return composeSummary("文件", parts...)
	case "transfer":
		side := map[int64]string{1: "发起", 3: "收款"}[detailInteger(details, "pay_subtype")]
		label := "转账"
		if side != "" {
			label += "·" + side
		}
		return composeSummary(label, firstNonEmpty(detailString(details, "pay_memo"), text))
	case "redpacket":
		amount := ""
		if redPacket, ok := details["red_packet"].(map[string]any); ok {
			amount = detailString(redPacket, "amount")
			if amount == "" && detailString(redPacket, "amount_status") == "not_retained" {
				amount = "金额未在本地记录中保留"
			}
		}
		return composeSummary("红包", amount, text)
	case "quote":
		if text != "" {
			return text
		}
		return "[引用回复]"
	}
	if kind == "applet" {
		parts := []string{text}
		if miniProgram, ok := details["mini_program"].(map[string]any); ok {
			parts = append(parts, detailString(miniProgram, "page_path"), detailString(miniProgram, "app_id"))
		}
		return composeSummary("小程序", parts...)
	}
	if kind == "channels" {
		parts := []string{text}
		if channels, ok := details["channels"].(map[string]any); ok {
			parts = append(parts, detailString(channels, "nickname"), detailString(channels, "description"), detailString(channels, "share_url"))
		}
		return composeSummary("视频号", parts...)
	}
	labels := map[string]string{"music": "音乐", "applet": "小程序", "channels": "视频号", "solitaire": "接龙", "pat": "拍一拍", "announce": "群公告", "gift": "礼物"}
	label := labels[kind]
	if label == "" {
		label = "应用消息"
		if appType > 0 {
			label += fmt.Sprintf("·%d", appType)
		}
	}
	return composeSummary(label, text)
}

func parseMessageReply(content string) *MessageReply {
	if !strings.Contains(content, "<refermsg") {
		return nil
	}
	root, err := parseMessageXML(content)
	if err != nil {
		return nil
	}
	reference := root.descendant("refermsg")
	if reference == nil {
		return nil
	}
	reply := &MessageReply{
		ToUsername: reference.child("fromusr").value(),
		ToName:     reference.child("displayname").value(),
		Quoted:     reference.child("content").value(),
		RefSvrID:   reference.child("svrid").value(),
	}
	referenceType := parseInteger(reference.child("type").value())
	rawQuoted := reply.Quoted
	if strings.Contains(rawQuoted, "<appmsg") || strings.Contains(rawQuoted, "&lt;appmsg") {
		reply.Quoted = parseMessageContent(49, rawQuoted, "").Content
	} else if referenceType > 0 && referenceType != 1 {
		base, _ := splitMessageType(referenceType)
		reply.Quoted = messageTypeLabel(referenceType, base)
	}
	if strings.HasPrefix(strings.TrimSpace(reply.Quoted), "<") {
		reply.Quoted = "[非文本内容]"
	}
	if referenceType == 3 || referenceType == 43 || referenceType == 34 || referenceType == 47 {
		if md5 := contentMediaMD5(rawQuoted); md5 != "" {
			reply.RefMD5 = md5
			if strings.HasSuffix(reply.Quoted, "]") {
				reply.Quoted = strings.TrimSuffix(reply.Quoted, "]") + "·" + md5[:6] + "]"
			} else {
				reply.Quoted += "·" + md5[:6]
			}
		}
	}
	if reply.ToUsername == "" && reply.ToName == "" && reply.Quoted == "" && reply.RefSvrID == "" {
		return nil
	}
	reply.Quoted = normalizeEmojiText(reply.Quoted)
	return reply
}

func parseMessageMentions(source string) []string {
	if !strings.Contains(source, "<atuserlist") {
		return nil
	}
	value := xmlTagText(source, "atuserlist")
	if value == "" {
		return nil
	}
	result := []string{}
	for _, token := range regexp.MustCompile(`[,、\s]+`).Split(value, -1) {
		token = strings.TrimSpace(token)
		if token == "notify@all" {
			token = "所有人"
		}
		if token != "" {
			result = append(result, token)
		}
	}
	return result
}

func messageSearchText(message Message) string {
	parts := []string{strings.ToLower(message.Content), strings.ToLower(message.VoiceTranscript), strings.ToLower(message.Sender), strings.ToLower(message.SenderUsername), strings.ToLower(message.SenderNickname), strings.ToLower(message.SenderRemark), strings.ToLower(message.SenderGroupNickname)}
	if message.ReplyTo != nil {
		parts = append(parts, strings.ToLower(message.ReplyTo.ToUsername), strings.ToLower(message.ReplyTo.ToName), strings.ToLower(message.ReplyTo.Quoted))
	}
	parts = append(parts, strings.ToLower(strings.Join(message.Mentions, " ")))
	if len(message.Details) > 0 {
		if payload, err := json.Marshal(message.Details); err == nil {
			parts = append(parts, strings.ToLower(string(payload)))
		}
	}
	return strings.Join(parts, "\n")
}

func contentMediaMD5(content string) string {
	raw := content
	if strings.Contains(raw, "&lt;") {
		raw = html.UnescapeString(raw)
	}
	for _, pattern := range []*regexp.Regexp{messageMD5Attribute, messageMD5Element} {
		if match := pattern.FindStringSubmatch(raw); len(match) == 2 {
			return strings.ToLower(match[1])
		}
	}
	return ""
}

func formatLocation(content string) string {
	attribute := func(name string) string {
		pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `="([^"]*)"`)
		if match := pattern.FindStringSubmatch(content); len(match) == 2 {
			return strings.TrimSpace(html.UnescapeString(match[1]))
		}
		return ""
	}
	if name := firstNonEmpty(attribute("poiname"), attribute("label")); name != "" {
		return "[位置] " + name
	}
	if x, y := attribute("x"), attribute("y"); x != "" && y != "" {
		return "[位置] " + y + "," + x
	}
	return "[位置]"
}

func formatVoIP(content string) string {
	switch xmlTagText(content, "room_type") {
	case "0":
		return "[视频通话]"
	case "1":
		return "[语音通话]"
	default:
		return "[通话]"
	}
}

func placeholderForKind(kind string) string {
	labels := map[string]string{
		"image": "[图片]", "voice": "[语音消息]", "video": "[视频]", "location": "[位置]",
		"system": "[系统消息]", "transfer": "[转账]", "redpacket": "[红包]", "quote": "[引用消息]",
		"sticker": "[表情]", "card": "[名片]", "voip": "[通话]", "pat": "[拍一拍]",
		"forward": "[聊天记录]", "gift": "[礼物]", "channels": "[视频号]", "applet": "[小程序]",
		"music": "[音乐]", "solitaire": "[接龙]",
	}
	return labels[kind]
}

func mediaPlaceholder(label, md5 string) string {
	if md5 == "" {
		return "[" + label + "]"
	}
	return "[" + label + "·" + md5[:6] + "]"
}

func setDetail(target map[string]any, key string, value any) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		if strings.TrimSpace(typed) == "" {
			return
		}
	case int64:
		if typed == 0 {
			return
		}
	}
	target[key] = value
}

func parseInteger(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func validMD5(value string) bool {
	return len(value) == 32 && regexp.MustCompile(`(?i)^[0-9a-f]{32}$`).MatchString(value)
}

func firstChildValue(node *messageXMLNode, names ...string) string {
	for _, name := range names {
		if value := node.child(name).value(); value != "" {
			return value
		}
	}
	return ""
}

func firstPathValue(node *messageXMLNode, paths ...[]string) string {
	for _, path := range paths {
		if value := node.path(path...).value(); value != "" {
			return value
		}
	}
	return ""
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	value, _ := details[key].(string)
	return value
}

func detailInteger(details map[string]any, key string) int64 {
	if details == nil {
		return 0
	}
	switch value := details[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	}
	return 0
}

func oneLine(value string) string {
	return strings.TrimSpace(messageWhitespace.ReplaceAllString(normalizeEmojiText(value), " "))
}

func clipped(value string, limit int) string {
	value = oneLine(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func composeSummary(label string, parts ...string) string {
	clean := []string{}
	for _, part := range parts {
		if value := oneLine(part); value != "" {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return "[" + label + "]"
	}
	return "[" + label + "] " + strings.Join(clean, "｜")
}

func formatByteSize(size int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	amount := float64(size)
	unit := units[0]
	for _, candidate := range units {
		unit = candidate
		if amount < 1024 || candidate == units[len(units)-1] {
			break
		}
		amount /= 1024
	}
	if unit == "B" {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f %s", amount, unit)
}
