package store

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testMomentXML = `<TimelineObject>
<id>9</id><username>wxid_author</username><createTime>1700000000</createTime>
<contentDesc>朋友圈正文</contentDesc><location city="深圳" label="公园"/>
<ContentObject><type>1</type><title>示例链接</title><contentUrl>https://example.invalid/post</contentUrl>
<mediaList><media><SnsDataItem><id>%s</id><type>2</type>
<url md5="%s">https://cdn.invalid/resource/%s/0</url>
<thumb>https://cdn.invalid/thumb</thumb><size width="800" height="600"/>
</SnsDataItem></media></mediaList></ContentObject></TimelineObject>`

const testMomentInteractionXML = `<SnsDataItem><TimelineObject>
<id>19</id><username>wxid_author</username><createTime>1700000000</createTime>
<contentDesc>带互动的朋友圈</contentDesc><ContentObject><type>1</type></ContentObject>
</TimelineObject><LocalExtraInfo><like_flag>1</like_flag>
<like_user_list><user_comment><comment_id>101</comment_id><comment_64id>1001</comment_64id>
<type>1</type><username>wxid_liker</username><nickname>点赞者</nickname><create_time>1700000010</create_time>
<comment_flag>0</comment_flag><b_deleted>0</b_deleted><ref_comment_id>0</ref_comment_id><ref_comment_64id>0</ref_comment_64id>
<comment_content_type>0</comment_content_type><comment_imageinfo_count>0</comment_imageinfo_count><comment_emojiinfo_count>0</comment_emojiinfo_count>
</user_comment></like_user_list>
<comment_user_list><user_comment><comment_id>201</comment_id><comment_64id>2001</comment_64id>
<type>2</type><username>wxid_commenter</username><nickname>评论者</nickname><create_time>1700000020</create_time>
<content>第一条评论</content><comment_flag>0</comment_flag><b_deleted>0</b_deleted><ref_comment_id>0</ref_comment_id><ref_comment_64id>0</ref_comment_64id>
<comment_content_type>0</comment_content_type><comment_imageinfo_count>0</comment_imageinfo_count><comment_emojiinfo_count>0</comment_emojiinfo_count>
</user_comment></comment_user_list>
<comment_user_list><user_comment><comment_id>202</comment_id><comment_64id>2002</comment_64id>
<type>2</type><username>wxid_reply</username><nickname>回复者</nickname><create_time>1700000030</create_time>
<content>回复关键词</content><comment_flag>0</comment_flag><b_deleted>0</b_deleted><ref_comment_id>201</ref_comment_id><ref_comment_64id>2001</ref_comment_64id>
<comment_content_type>2</comment_content_type><comment_imageinfo_count>1</comment_imageinfo_count><comment_emojiinfo_count>0</comment_emojiinfo_count>
<imagelist><imageinfo><media_id>%s</media_id><md5>%s</md5><url>https://cdn.invalid/comment/%s/0</url><thumb_url>https://cdn.invalid/comment/%s/150</thumb_url></imageinfo></imagelist>
</user_comment></comment_user_list>
<comment_user_list><user_comment><comment_id>203</comment_id><comment_64id>2003</comment_64id>
<type>2</type><username>wxid_unresolved</username><nickname>引用缺失者</nickname><create_time>1700000040</create_time>
<content>引用目标未留存</content><comment_flag>0</comment_flag><b_deleted>0</b_deleted><ref_comment_id>999</ref_comment_id><ref_comment_64id>9999</ref_comment_64id>
<comment_content_type>0</comment_content_type><comment_imageinfo_count>0</comment_imageinfo_count><comment_emojiinfo_count>0</comment_emojiinfo_count>
</user_comment></comment_user_list>
</LocalExtraInfo></SnsDataItem>`

const testOfficialXML = `<msg><appmsg><type>5</type><mmreader>
<publisher><nickname>示例公众号</nickname><username>gh_example</username></publisher>
<category count="2"><item><title>第一篇</title><digest>第一篇摘要</digest>
<url>https://example.com/one</url><cover>https://img.example/one</cover><pub_time>1700000000</pub_time>
</item><item><title>第二篇</title><summary>第二篇摘要</summary>
<longurl>https://example.com/two</longurl><pub_time>1700000100</pub_time>
</item></category></mmreader></appmsg></msg>`

func testPNG(payload string) []byte {
	digest := md5.Sum([]byte(payload))
	value := image.NewRGBA(image.Rect(0, 0, 1, 1))
	value.Set(0, 0, color.RGBA{R: digest[0], G: digest[1], B: digest[2], A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		panic(err)
	}
	return output.Bytes()
}

func contentMD5(value []byte) string {
	digest := md5.Sum(value)
	return hex.EncodeToString(digest[:])
}

func createContentContactDatabase(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "contact", "contact.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path,
		"CREATE TABLE contact(username TEXT, alias TEXT, remark TEXT, nick_name TEXT, delete_flag INTEGER)",
		"INSERT INTO contact VALUES('wxid_author','','朋友圈作者','作者',0)",
		"INSERT INTO contact VALUES('gh_example','example','公众号备注','示例公众号',0)",
	)
}

func TestMomentsDiscoverySearchAndMediaResolution(t *testing.T) {
	root := t.TempDir()
	createContentContactDatabase(t, root)
	plain := testPNG("moment")
	digest := contentMD5(plain)
	xml := fmt.Sprintf(testMomentXML, digest, digest, digest)
	path := filepath.Join(root, "sns", "sns.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path,
		"CREATE TABLE SnsTimeLine(tid INTEGER, user_name TEXT, content BLOB, pack_info_buf BLOB)",
		"INSERT INTO SnsTimeLine VALUES(9,'wxid_author','"+strings.ReplaceAll(xml, "'", "''")+"',X'00')",
	)

	contacts, err := MomentContacts(root, "作者", 20)
	if err != nil || !contacts.Available || len(contacts.Items) != 1 || contacts.Items[0].LocalMomentCount != 1 {
		t.Fatalf("朋友圈联系人异常：report=%+v err=%v", contacts, err)
	}
	history, err := Moments(root, "wxid_author", nil, nil, 20)
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("朋友圈历史异常：report=%+v err=%v", history, err)
	}
	item := history.Items[0]
	if item.Text != "朋友圈正文" || item.Location["city"] != "深圳" || item.Link == nil || len(item.Media) != 1 {
		t.Fatalf("朋友圈解析异常：%+v", item)
	}
	search, err := SearchMoments(root, "公园", "wxid_author", nil, nil, 20)
	if err != nil || len(search.Items) != 1 || len(search.Items[0].MatchedFields) != 1 || search.Items[0].MatchedFields[0] != "location.label" {
		t.Fatalf("朋友圈搜索异常：report=%+v err=%v", search, err)
	}

	account := t.TempDir()
	mediaPath := filepath.Join(account, "cache", digest+".png")
	if err := ensureParent(mediaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	resolution := ResolveMomentMedia(history.Items, MomentMediaOptions{AccountPath: account})
	if resolution.VerifiedLocalMedia != 1 || history.Items[0].Media[0].ResolutionStatus != "verified_local" ||
		history.Items[0].Media[0].Local == nil || history.Items[0].Media[0].Local.ContentMD5 != digest {
		t.Fatalf("朋友圈媒体强证据解析异常：resolution=%+v media=%+v", resolution, history.Items[0].Media[0])
	}
}

func TestMomentIdentityConflictBlocksMediaResolution(t *testing.T) {
	xml := fmt.Sprintf(testMomentXML, strings.Repeat("a", 32), strings.Repeat("a", 32), strings.Repeat("a", 32))
	item := parseMoment(int64(9), "wxid_other", xml, "", "sns/sns.db")
	if item.ParseStatus != "identity_conflict" || len(item.Media) != 1 || item.Media[0].ResolutionStatus != "identity_conflict" {
		t.Fatalf("朋友圈身份冲突未被阻断：%+v", item)
	}
	resolution := ResolveMomentMedia([]Moment{item}, MomentMediaOptions{AccountPath: t.TempDir()})
	if resolution.IdentityConflicts != 1 || resolution.VerifiedLocalMedia != 0 {
		t.Fatalf("身份冲突媒体不应被解析：%+v", resolution)
	}
}

func TestMomentIdentityConflictBlocksCommentMediaResolution(t *testing.T) {
	digest := strings.Repeat("b", 32)
	xml := fmt.Sprintf(testMomentInteractionXML, digest, digest, digest, digest)
	item := parseMoment(int64(19), "wxid_other", xml, "", "sns/sns.db")
	media := item.Interactions.Comments[1].Media[0]
	if item.ParseStatus != "identity_conflict" || item.Interactions.ParseStatus != "identity_conflict" || media.ResolutionStatus != "identity_conflict" {
		t.Fatalf("朋友圈身份冲突未阻断评论媒体：item=%+v media=%+v", item, media)
	}
	items := []Moment{item}
	resolution := ResolveMomentMedia(items, MomentMediaOptions{AccountPath: t.TempDir()})
	if resolution.IdentityConflicts != 1 || resolution.VerifiedLocalMedia != 0 {
		t.Fatalf("身份冲突评论媒体不应被解析：%+v", resolution)
	}
}

func TestMomentInteractionsSearchReplyAndMediaResolution(t *testing.T) {
	root := t.TempDir()
	createContentContactDatabase(t, root)
	commentImage := testPNG("comment-image")
	digest := contentMD5(commentImage)
	xml := fmt.Sprintf(testMomentInteractionXML, digest, digest, digest, digest)
	path := filepath.Join(root, "sns", "sns.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, path,
		"CREATE TABLE SnsTimeLine(tid INTEGER, user_name TEXT, content BLOB, pack_info_buf BLOB)",
		"INSERT INTO SnsTimeLine VALUES(19,'wxid_author','"+strings.ReplaceAll(xml, "'", "''")+"',X'00')",
	)

	history, err := Moments(root, "wxid_author", nil, nil, 20)
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("朋友圈互动历史异常：report=%+v err=%v", history, err)
	}
	interactions := history.Items[0].Interactions
	if interactions.ParseStatus != "parsed" || len(interactions.Likes) != 1 || len(interactions.Comments) != 3 {
		t.Fatalf("朋友圈互动解析异常：%+v", interactions)
	}
	reply := interactions.Comments[1]
	if reply.ReplyTo == nil || !reply.ReplyTo.Resolved || reply.ReplyTo.EvidenceID != interactions.Comments[0].EvidenceID || len(reply.Media) != 1 {
		t.Fatalf("朋友圈回复关系或评论图片异常：%+v", reply)
	}
	unresolved := interactions.Comments[2]
	if unresolved.ReplyTo == nil || unresolved.ReplyTo.Resolved || unresolved.ReplyTo.Comment64ID != "9999" {
		t.Fatalf("未留存回复引用未被保留：%+v", unresolved)
	}
	if history.Coverage["visible_likes"].(int) != 1 || history.Coverage["visible_comments"].(int) != 3 || history.Coverage["visible_replies"].(int) != 2 || history.Coverage["unresolved_visible_replies"].(int) != 1 || history.Coverage["comment_logical_media"].(int) != 1 {
		t.Fatalf("朋友圈互动覆盖统计异常：%+v", history.Coverage)
	}

	search, err := SearchMoments(root, "回复关键词", "wxid_author", nil, nil, 20)
	if err != nil || len(search.Items) != 1 || len(search.Items[0].MatchedFields) != 1 || !strings.Contains(search.Items[0].MatchedFields[0], "interactions.comments.") {
		t.Fatalf("朋友圈评论搜索异常：report=%+v err=%v", search, err)
	}

	account := t.TempDir()
	mediaPath := filepath.Join(account, "cache", digest+".png")
	if err := ensureParent(mediaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, commentImage, 0o600); err != nil {
		t.Fatal(err)
	}
	resolution := ResolveMomentMedia(history.Items, MomentMediaOptions{AccountPath: account})
	resolved := history.Items[0].Interactions.Comments[1].Media[0]
	if resolution.VerifiedLocalMedia != 1 || resolved.ResolutionStatus != "verified_local" || resolved.Local == nil || resolved.Local.ContentMD5 != digest {
		t.Fatalf("朋友圈评论图片强证据解析异常：resolution=%+v media=%+v", resolution, resolved)
	}
}

func TestMomentMediaUsesHardlinkMappingWithoutGuessingByTime(t *testing.T) {
	plain := testPNG("hardlink")
	digest := contentMD5(plain)
	xml := fmt.Sprintf(testMomentXML, digest, digest, digest)
	item := parseMoment(int64(9), "wxid_author", xml, "", "sns/sns.db")
	snapshot := t.TempDir()
	hardlinkPath := filepath.Join(snapshot, "hardlink", "hardlink.db")
	if err := ensureParent(hardlinkPath); err != nil {
		t.Fatal(err)
	}
	createTestDatabase(t, hardlinkPath,
		"CREATE TABLE dir2id(username TEXT)",
		"INSERT INTO dir2id(rowid,username) VALUES(1,'segment-a'),(2,'segment-b')",
		"CREATE TABLE image_hardlink_info_v4(md5 TEXT,file_name TEXT,dir1 INTEGER,dir2 INTEGER)",
		"INSERT INTO image_hardlink_info_v4 VALUES('"+digest+"','opaque.dat',1,2)",
	)
	account := t.TempDir()
	mediaPath := filepath.Join(account, "msg", "attach", "segment-a", "segment-b", "opaque.dat")
	if err := ensureParent(mediaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	items := []Moment{item}
	resolution := ResolveMomentMedia(items, MomentMediaOptions{AccountPath: account, SnapshotPath: snapshot})
	if resolution.VerifiedLocalMedia != 1 || items[0].Media[0].Local == nil || items[0].Media[0].Local.VerifiedBy != "plaintext_md5" {
		t.Fatalf("hardlink 媒体映射异常：resolution=%+v media=%+v", resolution, items[0].Media[0])
	}
}

func TestMomentMediaRejectsAmbiguousExactResourceCandidates(t *testing.T) {
	resourceID := "resource_identifier_123456"
	items := []Moment{{
		ParseStatus: "parsed",
		Media: []MomentMedia{{
			Index: 1, MediaID: resourceID, Kind: "image",
			ResolutionStatus: "logical_only", VerifiedBy: "sns_xml_parentage",
		}},
	}}
	account := t.TempDir()
	for index, payload := range []string{"first", "second"} {
		path := filepath.Join(account, "cache", fmt.Sprintf("candidate-%d", index), resourceID+".png")
		if err := ensureParent(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, testPNG(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolution := ResolveMomentMedia(items, MomentMediaOptions{AccountPath: account})
	if resolution.AmbiguousStrongCandidates != 1 || items[0].Media[0].ResolutionStatus != "ambiguous_strong_candidates" || items[0].Media[0].Local != nil {
		t.Fatalf("同等级冲突候选未被拒绝：resolution=%+v media=%+v", resolution, items[0].Media[0])
	}
}

func createOfficialFixture(t *testing.T, root string, articleXML string) {
	t.Helper()
	createContentContactDatabase(t, root)
	path := filepath.Join(root, "message", "biz_message_0.db")
	if err := ensureParent(path); err != nil {
		t.Fatal(err)
	}
	table := messageTable("gh_example")
	createTestDatabase(t, path,
		"CREATE TABLE Name2Id(user_name TEXT)",
		"INSERT INTO Name2Id(rowid,user_name) VALUES(1,'gh_example')",
		"CREATE TABLE ["+table+"](local_id INTEGER,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,message_content TEXT)",
		"INSERT INTO ["+table+"] VALUES(1,101,21474836529,1700000200000,1,1700000200,'"+strings.ReplaceAll(articleXML, "'", "''")+"')",
		"INSERT INTO ["+table+"] VALUES(2,102,1,1700000300000,1,1700000300,'服务通知')",
	)
}

func TestOfficialDiscoveryHistoryAndSearch(t *testing.T) {
	root := t.TempDir()
	createOfficialFixture(t, root, testOfficialXML)
	accounts, err := OfficialAccounts(root, "公众号", 20)
	if err != nil || len(accounts.Items) != 1 || accounts.Items[0].LocalMessageCount != 2 || !accounts.Items[0].FollowedCandidate {
		t.Fatalf("公众号发现异常：report=%+v err=%v", accounts, err)
	}
	history, err := OfficialHistory(root, "gh_example", nil, nil, 20)
	if err != nil || len(history.Items) != 2 || history.Items[0].Title != "第二篇" || history.Items[0].ContentLevel != "card_metadata" {
		t.Fatalf("公众号历史异常：report=%+v err=%v", history, err)
	}
	if history.Coverage["article_body_available"] != false || history.Coverage["complete_publication_history"] != false {
		t.Fatalf("公众号覆盖边界异常：%+v", history.Coverage)
	}
	search, err := SearchOfficial(root, "第一篇摘要", "gh_example", nil, nil, 20)
	if err != nil || len(search.Items) != 1 || search.Items[0].MatchedFields[0] != "description" {
		t.Fatalf("公众号搜索异常：report=%+v err=%v", search, err)
	}
}

func TestOfficialHistoryRejectsUnsafeURL(t *testing.T) {
	root := t.TempDir()
	unsafe := strings.Replace(testOfficialXML, "https://example.com/one", "javascript:alert(1)", 1)
	createOfficialFixture(t, root, unsafe)
	history, err := OfficialHistory(root, "gh_example", nil, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range history.Items {
		if item.Title == "第一篇" && item.URL != "" {
			t.Fatalf("不安全 URL 未拒绝：%+v", item)
		}
	}
	if history.Coverage["unsafe_urls_rejected"].(int) != 1 {
		t.Fatalf("不安全 URL 计数异常：%+v", history.Coverage)
	}
}
