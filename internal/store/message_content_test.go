package store

import (
	"strings"
	"testing"
)

const testLinkCardXML = `<msg><appmsg appid="wx123">
<title>示例文章</title><des>这是一段卡片摘要</des><type>5</type>
<url>https://example.com/read?id=1&amp;from=wx</url>
<sourceusername>gh_example</sourceusername><sourcedisplayname>示例来源</sourcedisplayname>
</appmsg></msg>`

const testForwardCardXML = `<msg><appmsg><title>群聊的聊天记录</title><type>19</type>
<recorditem><![CDATA[<recordinfo><datalist count="2">
<dataitem datatype="1"><sourcename>张三</sourcename><sourcetime>1700000000</sourcetime><datadesc>第一条消息</datadesc></dataitem>
<dataitem datatype="5"><weburlitem><title>链接标题</title><desc>链接摘要</desc><link>https://example.com/a?x=1&amp;y=2</link></weburlitem><source><fromusr>wxid_b</fromusr><fromusrname>李四</fromusrname></source></dataitem>
</datalist></recordinfo>]]></recorditem></appmsg></msg>`

func TestParseLinkCardAndSearchDetails(t *testing.T) {
	parsed := parseMessageContent(49, testLinkCardXML, "")
	if parsed.Kind != "link" || parsed.SubType != 5 {
		t.Fatalf("链接卡片类型错误：%+v", parsed)
	}
	if parsed.Content != "[链接] 示例文章｜来源：示例来源｜这是一段卡片摘要" {
		t.Fatalf("链接卡片摘要错误：%s", parsed.Content)
	}
	if detailString(parsed.Details, "url") != "https://example.com/read?id=1&from=wx" {
		t.Fatalf("链接地址没有完整保留：%+v", parsed.Details)
	}
	message := Message{Content: parsed.Content, Details: parsed.Details}
	if !strings.Contains(messageSearchText(message), "gh_example") {
		t.Fatal("搜索文本没有包含结构化卡片字段")
	}
}

func TestParseFileAndTransferCards(t *testing.T) {
	fileXML := `<msg><appmsg><title>季度报告.pdf</title><type>6</type><appattach><totallen>1048576</totallen><fileext>pdf</fileext><filemd5>0123456789abcdef0123456789abcdef</filemd5><cdnattachurl>https://example.com/file</cdnattachurl></appattach></appmsg></msg>`
	file := parseMessageContent(49, fileXML, "")
	if file.Kind != "file" || file.Content != "[文件] 季度报告.pdf｜1.0 MB" || detailString(file.Details, "file_md5") == "" {
		t.Fatalf("文件卡片解析错误：%+v", file)
	}
	transferXML := `<msg><appmsg><type>2000</type><wcpayinfo><paysubtype>3</paysubtype><pay_memo>午餐费用</pay_memo></wcpayinfo></appmsg></msg>`
	transfer := parseMessageContent(49, transferXML, "")
	if transfer.Kind != "transfer" || transfer.Content != "[转账·收款] 午餐费用" {
		t.Fatalf("转账卡片解析错误：%+v", transfer)
	}
}

func TestParseRedPacketChannelsAppletAndContactCard(t *testing.T) {
	redPacket := parseMessageContent(8594229559345, `<msg><appmsg><title>恭喜发财</title><type>2001</type><wcpayinfo><nativeurl>wxpay://hongbao?id=1</nativeurl><paymsgid>pay-1</paymsgid><amount>880</amount></wcpayinfo></appmsg></msg>`, "")
	packetDetails, ok := redPacket.Details["red_packet"].(map[string]any)
	if !ok || detailString(packetDetails, "amount") != "¥8.80" || redPacket.Content != "[红包] ¥8.80｜恭喜发财" {
		t.Fatalf("红包金额解析错误：%+v", redPacket)
	}
	channels := parseMessageContent(219043332145, `<msg><appmsg><title>视频分享</title><type>51</type><url>https://channels.weixin.qq.com/share/1</url><finderFeed><objectId>obj-1</objectId><username>finder-user</username><nickname>作者</nickname><desc>视频说明</desc><mediaList><media><mediaType>4</mediaType><url>https://finder.video/1</url><thumbUrl>https://finder.thumb/1</thumbUrl></media></mediaList></finderFeed></appmsg></msg>`, "")
	channelDetails, ok := channels.Details["channels"].(map[string]any)
	if !ok || detailString(channelDetails, "share_url") != "https://channels.weixin.qq.com/share/1" || !strings.Contains(channels.Content, "https://channels.weixin.qq.com/share/1") {
		t.Fatalf("视频号字段解析错误：%+v", channels)
	}
	applet := parseMessageContent(141733920817, `<msg><appmsg><title>示例小程序</title><type>33</type><weappinfo><appid>wx-app</appid><username>gh_app@app</username><pagepath>pages/home</pagepath><version>7</version></weappinfo></appmsg></msg>`, "")
	miniProgram, ok := applet.Details["mini_program"].(map[string]any)
	if !ok || detailString(miniProgram, "page_path") != "pages/home" || detailString(miniProgram, "app_id") != "wx-app" {
		t.Fatalf("小程序字段解析错误：%+v", applet)
	}
	card := parseMessageContent(42, `<msg username="wxid_card" nickname="名片用户" alias="alias-id" brandHomeUrl="https://brand.example"/>`, "")
	if card.Content != "[微信名片] 名片用户｜wxid_card" || detailString(card.Details, "alias") != "alias-id" || detailString(card.Details, "brand_home_url") != "https://brand.example" {
		t.Fatalf("微信名片字段解析错误：%+v", card)
	}
}

func TestNormalizeEmojiText(t *testing.T) {
	if value := normalizeEmojiText("你好[微笑] [OK]"); value != "你好😊 👌" {
		t.Fatalf("表情归一化错误：%q", value)
	}
	if value := normalizeEmojiText("套餐[￥99任洗5件] [公司名称]"); value != "套餐[￥99任洗5件] [公司名称]" {
		t.Fatalf("未知方括号文本不应被改写：%q", value)
	}
}

func TestParseForwardRecordAndPreview(t *testing.T) {
	parsed := parseMessageContent(81604378673, testForwardCardXML, "")
	if parsed.Kind != "forward" || parsed.Content != "[聊天记录·2条] 张三：第一条消息｜李四：链接标题" {
		t.Fatalf("聊天记录摘要错误：%+v", parsed)
	}
	items, ok := parsed.Details["items"].([]map[string]any)
	if !ok || len(items) != 2 || detailString(items[1], "url") != "https://example.com/a?x=1&y=2" {
		t.Fatalf("聊天记录明细错误：%+v", parsed.Details)
	}
}

func TestParseOfficialBundleAndUnknownCard(t *testing.T) {
	xmlContent := `<msg><appmsg><type>5</type><mmreader><publisher><nickname>示例公众号</nickname><username>gh_example</username></publisher><category count="2"><item><title_v2>头条文章</title_v2><digest>头条摘要</digest><url>https://example.com/one?a=1&amp;b=2</url><share_cover><cdn_url>https://img/1</cdn_url></share_cover><sources><source><name>特约作者</name></source></sources></item><item><text_title>次条文章</text_title><summary>次条摘要</summary><longurl>https://example.com/two</longurl></item></category></mmreader></appmsg></msg>`
	parsed := parseMessageContent(21474836529, xmlContent, "")
	if parsed.Content != "[链接] 头条文章｜头条摘要" || detailInteger(parsed.Details, "article_count") != 2 {
		t.Fatalf("公众号多图文卡片解析错误：%+v", parsed)
	}
	articles, ok := parsed.Details["articles"].([]map[string]any)
	if !ok || len(articles) != 2 || detailString(articles[0], "author") != "特约作者" {
		t.Fatalf("公众号文章明细错误：%+v", parsed.Details)
	}
	unknown := parseMessageContent(49, `<msg><appmsg><title>未识别卡片</title><type>999</type></appmsg></msg>`, "")
	if unknown.Content != "[应用消息·999] 未识别卡片" {
		t.Fatalf("未知卡片没有保留子类型：%s", unknown.Content)
	}
}

func TestRejectUnsafeCardAndFormatMedia(t *testing.T) {
	unsafe := `<!DOCTYPE msg [<!ENTITY secret "hidden">]><msg><appmsg><type>5</type><title>&secret;</title></appmsg></msg>`
	parsed := parseMessageContent(49, unsafe, "")
	if detailString(parsed.Details, "parse_error") != "unsafe_doctype" {
		t.Fatalf("不安全卡片没有拒绝：%+v", parsed.Details)
	}
	image := parseMessageContent(3, `<msg><img md5="0123456789abcdef0123456789abcdef"/></msg>`, "")
	if image.Content != "[图片·012345]" || image.MediaMD5 == "" {
		t.Fatalf("图片指纹错误：%+v", image)
	}
	voice := parseMessageContent(34, `<msg><voicemsg voicelength="15300"/></msg>`, "")
	if voice.Content != "[语音 15.3 秒]" || voice.VoiceDurationMS == nil || *voice.VoiceDurationMS != 15300 {
		t.Fatalf("语音时长错误：%+v", voice)
	}
}

func TestParseLocationVoIPReplyAndMentions(t *testing.T) {
	location := parseMessageContent(48, `<msg><location x="22.1" y="114.2" label="" poiname="维港"/></msg>`, "")
	if location.Content != "[位置] 维港" {
		t.Fatalf("位置摘要错误：%s", location.Content)
	}
	call := parseMessageContent(50, `<msg><room_type>0</room_type></msg>`, "")
	if call.Content != "[视频通话]" {
		t.Fatalf("通话摘要错误：%s", call.Content)
	}
	replyXML := `<msg><appmsg><type>57</type><title>本次回复</title><refermsg><type>1</type><svrid>88</svrid><displayname>张三</displayname><content>被引用正文</content></refermsg></appmsg></msg>`
	reply := parseMessageContent(244813135921, replyXML, `<msgsource><atuserlist><![CDATA[notify@all,wxid_a]]></atuserlist></msgsource>`)
	if reply.ReplyTo == nil || reply.ReplyTo.Quoted != "被引用正文" || reply.ReplyTo.RefSvrID != "88" {
		t.Fatalf("引用消息解析错误：%+v", reply.ReplyTo)
	}
	if len(reply.Mentions) != 2 || reply.Mentions[0] != "所有人" || reply.Mentions[1] != "wxid_a" {
		t.Fatalf("@ 列表解析错误：%+v", reply.Mentions)
	}
}
