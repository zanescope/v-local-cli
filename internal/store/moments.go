package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type MomentContact struct {
	Username         string `json:"username"`
	DisplayName      string `json:"display_name"`
	LocalMomentCount int    `json:"local_moment_count"`
}

type MomentAuthor struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type MomentInteractionReply struct {
	CommentID   string `json:"comment_id,omitempty"`
	Comment64ID string `json:"comment_64id,omitempty"`
	EvidenceID  string `json:"evidence_id,omitempty"`
	Resolved    bool   `json:"resolved"`
}

type MomentInteraction struct {
	EvidenceID      string                  `json:"evidence_id"`
	EvidenceType    string                  `json:"evidence_type"`
	Kind            string                  `json:"kind"`
	InteractionID   string                  `json:"interaction_id"`
	CommentID       string                  `json:"comment_id,omitempty"`
	Comment64ID     string                  `json:"comment_64id,omitempty"`
	Actor           MomentAuthor            `json:"actor"`
	Timestamp       int64                   `json:"timestamp,omitempty"`
	Time            string                  `json:"time,omitempty"`
	Content         string                  `json:"content,omitempty"`
	ContentTypeCode *int64                  `json:"content_type_code,omitempty"`
	CommentFlag     *int64                  `json:"comment_flag,omitempty"`
	SourceCode      *int64                  `json:"source_code,omitempty"`
	Deleted         bool                    `json:"deleted"`
	LocallyAdded    bool                    `json:"locally_added"`
	ReplyTo         *MomentInteractionReply `json:"reply_to,omitempty"`
	ExpectedMedia   int                     `json:"expected_media"`
	ExpectedEmojis  int                     `json:"expected_emojis"`
	Media           []MomentMedia           `json:"media"`
	Provenance      map[string]string       `json:"provenance"`
}

type MomentInteractions struct {
	ParseStatus    string              `json:"parse_status"`
	Source         string              `json:"source"`
	Scope          string              `json:"scope"`
	ViewerLikeFlag *int64              `json:"viewer_like_flag,omitempty"`
	Likes          []MomentInteraction `json:"likes"`
	Comments       []MomentInteraction `json:"comments"`
}

type MomentLink struct {
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	URL            string `json:"url,omitempty"`
	SourceUsername string `json:"source_username,omitempty"`
	SourceName     string `json:"source_name,omitempty"`
}

type MomentLocalMedia struct {
	Path       string `json:"-"`
	Format     string `json:"format"`
	Cipher     string `json:"cipher"`
	Bytes      int64  `json:"bytes"`
	SourceMD5  string `json:"source_md5"`
	ContentMD5 string `json:"content_md5"`
	ProofValue string `json:"proof_value"`
	VerifiedBy string `json:"verified_by"`
	SourceRoot string `json:"-"`
}

type MomentMedia struct {
	EvidenceID            string            `json:"evidence_id"`
	Index                 int               `json:"index"`
	MediaID               string            `json:"media_id,omitempty"`
	TypeCode              *int64            `json:"type_code,omitempty"`
	Kind                  string            `json:"kind"`
	VideoDurationSeconds  *float64          `json:"video_duration_seconds,omitempty"`
	URL                   string            `json:"url,omitempty"`
	ThumbURL              string            `json:"thumb_url,omitempty"`
	MetadataMD5           string            `json:"metadata_md5,omitempty"`
	MetadataMD5Candidates []string          `json:"metadata_md5_candidates,omitempty"`
	Size                  map[string]string `json:"size,omitempty"`
	Title                 string            `json:"title,omitempty"`
	Description           string            `json:"description,omitempty"`
	ResolutionStatus      string            `json:"resolution_status"`
	VerifiedBy            string            `json:"verified_by,omitempty"`
	Local                 *MomentLocalMedia `json:"local,omitempty"`
	remote                momentRemoteMedia
}

type momentRemoteVariant struct {
	URL           string
	Token         string
	Key           string
	EncryptionIdx string
	ExpectedMD5   string
	ExpectedBytes int64
}

type momentRemoteMedia struct {
	Original  momentRemoteVariant
	Thumbnail momentRemoteVariant
}

type MomentIdentity struct {
	XMLMomentID        string `json:"xml_moment_id,omitempty"`
	XMLAuthorUsername  string `json:"xml_author_username,omitempty"`
	MomentIDConsistent bool   `json:"moment_id_consistent"`
	AuthorConsistent   bool   `json:"author_consistent"`
}

type Moment struct {
	EvidenceID    string             `json:"evidence_id"`
	EvidenceType  string             `json:"evidence_type"`
	MomentID      string             `json:"moment_id,omitempty"`
	Author        MomentAuthor       `json:"author"`
	Timestamp     int64              `json:"timestamp,omitempty"`
	Time          string             `json:"time,omitempty"`
	Text          string             `json:"text"`
	TypeCode      *int64             `json:"type_code,omitempty"`
	Location      map[string]string  `json:"location,omitempty"`
	Link          *MomentLink        `json:"link,omitempty"`
	Media         []MomentMedia      `json:"media"`
	Interactions  MomentInteractions `json:"interactions"`
	ParseStatus   string             `json:"parse_status"`
	ParseError    string             `json:"parse_error,omitempty"`
	Identity      MomentIdentity     `json:"identity"`
	SourceDB      string             `json:"source_db"`
	MatchedFields []string           `json:"matched_fields,omitempty"`
	Provenance    map[string]string  `json:"provenance"`
}

type MomentContactReport struct {
	Items                     []MomentContact `json:"items"`
	Available                 bool            `json:"available"`
	Reason                    string          `json:"reason,omitempty"`
	Returned                  int             `json:"returned"`
	MatchingContacts          int             `json:"matching_contacts"`
	MatchingContactsTruncated bool            `json:"matching_contacts_truncated"`
	Scope                     string          `json:"scope"`
}

type MomentReport struct {
	Items    []Moment       `json:"items"`
	Coverage map[string]any `json:"coverage"`
}

func findTableCI(database *sql.DB, table string) string {
	var found string
	_ = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND lower(name)=lower(?) LIMIT 1",
		table,
	).Scan(&found)
	return found
}

func columnCI(available map[string]bool, wanted string) string {
	for name := range available {
		if strings.EqualFold(name, wanted) {
			return name
		}
	}
	return ""
}

func contactDisplays(root string) map[string]string {
	result := map[string]string{}
	contacts, err := Contacts(root, "", 0)
	if err != nil {
		return result
	}
	for _, contact := range contacts {
		result[contact.Username] = contact.Display
	}
	return result
}

func canonicalMomentID(value any) string {
	text := strings.TrimSpace(asString(value))
	if text == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(text), "0x") {
		if number, err := strconv.ParseUint(text[2:], 16, 64); err == nil {
			return strconv.FormatUint(number, 10)
		}
	}
	if signed, err := strconv.ParseInt(text, 10, 64); err == nil {
		return strconv.FormatUint(uint64(signed), 10)
	}
	if unsigned, err := strconv.ParseUint(text, 10, 64); err == nil {
		return strconv.FormatUint(unsigned, 10)
	}
	return text
}

func parseOptionalInt(value string) *int64 {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return nil
	}
	return &number
}

func parseOptionalFloat(value string) *float64 {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil
	}
	return &number
}

func safeRemoteURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return ""
	}
	return value
}

func publicMomentMediaURL(value string) string {
	value = safeRemoteURL(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func isMomentVideoURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(host, "vweixinthumb") {
		return false
	}
	return strings.HasPrefix(host, "snsvideodownload") || strings.HasSuffix(path, ".mp4") || strings.Contains(path, "/video")
}

func xmlLocation(root *xmlNode) map[string]string {
	node := root.direct("location")
	if node == nil {
		node = root.descendant("location")
	}
	if node == nil {
		return nil
	}
	result := map[string]string{}
	for _, attribute := range node.Attrs {
		value := strings.TrimSpace(attribute.Value)
		if value != "" {
			result[attribute.Name.Local] = value
		}
	}
	for _, name := range []string{"poiname", "poiName", "label", "city", "country", "latitude", "longitude", "x", "y"} {
		if value := node.directText(name); value != "" {
			result[name] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func semanticMediaNodes(content *xmlNode) []*xmlNode {
	mediaList := content.descendant("mediaList")
	if mediaList == nil {
		return nil
	}
	var candidates []*xmlNode
	for _, node := range mediaList.descendants("media", "SnsDataItem") {
		if node.descendant("url", "thumb") != nil {
			candidates = append(candidates, node)
		}
	}
	var result []*xmlNode
	for _, candidate := range candidates {
		containsCandidate := false
		for _, descendant := range candidate.descendants("media", "SnsDataItem") {
			for _, other := range candidates {
				if descendant == other {
					containsCandidate = true
					break
				}
			}
			if containsCandidate {
				break
			}
		}
		if !containsCandidate {
			result = append(result, candidate)
		}
	}
	return result
}

func canonicalMD5(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 32 {
		return ""
	}
	if _, err := strconv.ParseUint(value[:16], 16, 64); err != nil {
		return ""
	}
	if _, err := strconv.ParseUint(value[16:], 16, 64); err != nil {
		return ""
	}
	return value
}

func mediaItem(node *xmlNode, index int) MomentMedia {
	urlNode := node.descendant("url")
	thumbNode := node.descendant("thumb", "thumb_url")
	mediaID := firstNonEmpty(node.directText("id", "media_id"), node.descendantText("id", "media_id"))
	typeCode := parseOptionalInt(firstNonEmpty(node.directText("type"), node.descendantText("type")))
	duration := parseOptionalFloat(firstNonEmpty(node.directText("videoDuration"), node.descendantText("videoDuration")))
	rawFullURL := safeRemoteURL(urlNode.text())
	rawThumbURL := safeRemoteURL(thumbNode.text())
	fullURL := publicMomentMediaURL(rawFullURL)
	thumbURL := publicMomentMediaURL(rawThumbURL)
	fullParsed, _ := url.Parse(rawFullURL)
	thumbParsed, _ := url.Parse(rawThumbURL)
	var md5s []string
	for _, candidate := range []string{urlNode.attribute("md5"), thumbNode.attribute("md5"), node.directText("md5"), mediaID} {
		candidate = canonicalMD5(candidate)
		if candidate == "" {
			continue
		}
		found := false
		for _, existing := range md5s {
			found = found || existing == candidate
		}
		if !found {
			md5s = append(md5s, candidate)
		}
	}
	kind := "unknown"
	if duration != nil && *duration > 0 || isMomentVideoURL(rawFullURL) {
		kind = "video"
	} else if typeCode != nil && *typeCode == 2 && (fullURL != "" || thumbURL != "") {
		kind = "image"
	}
	size := map[string]string{}
	if sizeNode := node.descendant("size"); sizeNode != nil {
		for _, attribute := range sizeNode.Attrs {
			if value := strings.TrimSpace(attribute.Value); value != "" {
				size[attribute.Name.Local] = value
			}
		}
		for _, name := range []string{"width", "height", "totalSize", "total_size"} {
			if value := sizeNode.directText(name); value != "" {
				size[name] = value
			}
		}
	}
	if len(size) == 0 {
		size = nil
	}
	item := MomentMedia{
		Index: index, MediaID: mediaID, TypeCode: typeCode, Kind: kind,
		VideoDurationSeconds: duration, URL: fullURL, ThumbURL: thumbURL,
		MetadataMD5Candidates: md5s, Size: size,
		Title: node.directText("title"), Description: node.directText("description"),
		ResolutionStatus: "logical_only", VerifiedBy: "sns_xml_parentage",
	}
	if len(md5s) > 0 {
		item.MetadataMD5 = md5s[0]
	}
	item.remote = momentRemoteMedia{
		Original: momentRemoteVariant{
			URL:           rawFullURL,
			Token:         firstNonEmpty(node.directText("token"), urlNode.attribute("token"), fullParsed.Query().Get("token")),
			Key:           firstNonEmpty(node.directText("key"), urlNode.attribute("key"), fullParsed.Query().Get("key")),
			EncryptionIdx: firstNonEmpty(node.directText("enc_idx"), urlNode.attribute("enc_idx")),
			ExpectedMD5:   canonicalMD5(firstNonEmpty(urlNode.attribute("md5"), node.directText("md5"))),
			ExpectedBytes: asInt64(firstNonEmpty(node.directText("file_size"), node.descendantText("file_size", "totalSize", "total_size"))),
		},
		Thumbnail: momentRemoteVariant{
			URL:           rawThumbURL,
			Token:         firstNonEmpty(node.directText("thumb_url_token", "thumb_token"), thumbNode.attribute("token"), thumbParsed.Query().Get("token"), node.directText("token"), urlNode.attribute("token"), fullParsed.Query().Get("token")),
			Key:           firstNonEmpty(node.directText("thumb_key"), thumbNode.attribute("key"), thumbParsed.Query().Get("key")),
			EncryptionIdx: firstNonEmpty(node.directText("thumb_enc_idx"), thumbNode.attribute("enc_idx")),
			ExpectedMD5:   canonicalMD5(thumbNode.attribute("md5")),
		},
	}
	return item
}

func timelineVideoKey(timeline *xmlNode) string {
	keys := map[string]bool{}
	for _, node := range timeline.descendants("enc") {
		value := firstNonEmpty(node.attribute("key"), node.directText("key"))
		if _, err := strconv.ParseUint(value, 10, 64); err == nil && value != "" {
			keys[value] = true
		}
	}
	if len(keys) != 1 {
		return ""
	}
	for value := range keys {
		return value
	}
	return ""
}

func canonicalInteractionID(value string) string {
	value = canonicalMomentID(value)
	if value == "0" {
		return ""
	}
	return value
}

func xmlCount(node *xmlNode, name string) int {
	value := asInt64(node.directText(name))
	if value < 0 {
		return 0
	}
	return int(value)
}

func commentMediaItem(node *xmlNode, index int) MomentMedia {
	item := mediaItem(node, index)
	item.Kind = "image"
	item.VerifiedBy = "sns_comment_xml_parentage"
	return item
}

func parseMomentInteraction(node *xmlNode, kind, parentEvidenceID string, order int) MomentInteraction {
	commentID := canonicalInteractionID(node.directText("comment_id"))
	comment64ID := canonicalInteractionID(node.directText("comment_64id"))
	interactionID := firstNonEmpty(comment64ID, commentID)
	username := strings.TrimSpace(node.directText("username"))
	content := node.directText("content")
	timestamp := asInt64(node.directText("create_time"))
	if interactionID == "" {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", kind, username, content, timestamp, order)))
		interactionID = fmt.Sprintf("derived-%x", digest[:10])
	}
	item := MomentInteraction{
		EvidenceID:   parentEvidenceID + ":interaction:" + kind + ":" + interactionID,
		EvidenceType: "moment_interaction", Kind: kind, InteractionID: interactionID,
		CommentID: commentID, Comment64ID: comment64ID,
		Actor:     MomentAuthor{Username: username, DisplayName: firstNonEmpty(node.directText("nickname"), username)},
		Timestamp: timestamp, Content: content,
		ContentTypeCode: parseOptionalInt(node.directText("comment_content_type")),
		CommentFlag:     parseOptionalInt(node.directText("comment_flag")),
		SourceCode:      parseOptionalInt(node.directText("source")),
		Deleted:         asInt64(node.directText("b_deleted")) != 0,
		LocallyAdded:    asInt64(node.directText("is_local_added")) != 0,
		ExpectedMedia:   xmlCount(node, "comment_imageinfo_count"),
		ExpectedEmojis:  xmlCount(node, "comment_emojiinfo_count"),
		Media:           []MomentMedia{},
		Provenance: map[string]string{
			"adapter":                 "sns-local-extra-info-xml-v1",
			"database_table":          "SnsTimeLine",
			"interaction_verified_by": "content_xml_parentage",
		},
	}
	if timestamp > 0 {
		item.Time = time.Unix(timestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
	}
	refID := canonicalInteractionID(node.directText("ref_comment_id"))
	ref64ID := canonicalInteractionID(node.directText("ref_comment_64id"))
	if refID != "" || ref64ID != "" {
		item.ReplyTo = &MomentInteractionReply{CommentID: refID, Comment64ID: ref64ID}
	}
	for index, image := range node.descendants("imageinfo") {
		media := commentMediaItem(image, index+1)
		media.EvidenceID = item.EvidenceID + ":media:" + strconv.Itoa(media.Index)
		item.Media = append(item.Media, media)
	}
	if item.ExpectedMedia == 0 && len(item.Media) > 0 {
		item.ExpectedMedia = len(item.Media)
	}
	return item
}

func interactionList(extra *xmlNode, listName, kind, parentEvidenceID string) []MomentInteraction {
	items := []MomentInteraction{}
	seen := map[string]bool{}
	for _, list := range extra.descendants(listName) {
		for _, node := range list.descendants("user_comment") {
			item := parseMomentInteraction(node, kind, parentEvidenceID, len(items)+1)
			if seen[item.EvidenceID] {
				continue
			}
			seen[item.EvidenceID] = true
			items = append(items, item)
		}
	}
	return items
}

func parseMomentInteractions(root *xmlNode, parentEvidenceID string) MomentInteractions {
	result := MomentInteractions{
		ParseStatus: "unavailable", Source: "content_xml_local_extra_info",
		Scope: "locally_retained_visible_only", Likes: []MomentInteraction{}, Comments: []MomentInteraction{},
	}
	extra := root.direct("LocalExtraInfo")
	if extra == nil {
		extra = root.descendant("LocalExtraInfo")
	}
	if extra == nil {
		return result
	}
	result.ParseStatus = "parsed"
	result.ViewerLikeFlag = parseOptionalInt(extra.directText("like_flag"))
	result.Likes = interactionList(extra, "like_user_list", "like", parentEvidenceID)
	result.Comments = interactionList(extra, "comment_user_list", "comment", parentEvidenceID)
	byID := map[string]string{}
	for _, item := range result.Comments {
		for _, id := range []string{item.CommentID, item.Comment64ID} {
			if id != "" {
				byID[id] = item.EvidenceID
			}
		}
	}
	for index := range result.Comments {
		reply := result.Comments[index].ReplyTo
		if reply == nil {
			continue
		}
		for _, id := range []string{reply.Comment64ID, reply.CommentID} {
			if evidenceID := byID[id]; evidenceID != "" {
				reply.EvidenceID = evidenceID
				reply.Resolved = true
				break
			}
		}
	}
	return result
}

func parseMoment(tidValue, usernameValue, contentValue any, display, source string) Moment {
	content := asString(contentValue)
	tid := canonicalMomentID(tidValue)
	username := strings.TrimSpace(asString(usernameValue))
	identity := tid
	if identity == "" {
		digest := sha256.Sum256([]byte(content))
		identity = fmt.Sprintf("%x", digest[:12])
	}
	item := Moment{
		EvidenceID: "moment:" + username + ":" + identity, EvidenceType: "moment",
		MomentID: tid, Author: MomentAuthor{Username: username, DisplayName: firstNonEmpty(display, username)},
		Text: "", Media: []MomentMedia{}, ParseStatus: "unparsed", ParseError: "empty",
		Interactions: MomentInteractions{
			ParseStatus: "unavailable", Source: "content_xml_local_extra_info",
			Scope: "locally_retained_visible_only", Likes: []MomentInteraction{}, Comments: []MomentInteraction{},
		},
		SourceDB: source,
		Identity: MomentIdentity{MomentIDConsistent: true, AuthorConsistent: true},
		Provenance: map[string]string{
			"adapter": "sns-timeline-xml-v1", "database_table": "SnsTimeLine",
			"author_verified_by": "column:user_name", "media_verified_by": "content_xml_parentage",
			"interactions_verified_by": "content_xml_local_extra_info_parentage",
		},
	}
	root, parseError := parseXML(content, false)
	if root == nil {
		item.ParseError = parseError
		return item
	}
	timeline := root
	if localXMLName(root.Name) != "timelineobject" {
		timeline = root.descendant("TimelineObject")
	}
	if timeline == nil {
		item.ParseError = "timeline_object_missing"
		return item
	}
	xmlID := canonicalMomentID(timeline.directText("id", "tid"))
	xmlUsername := timeline.directText("username", "user_name")
	item.Identity = MomentIdentity{
		XMLMomentID: xmlID, XMLAuthorUsername: xmlUsername,
		MomentIDConsistent: tid == "" || xmlID == "" || tid == xmlID,
		AuthorConsistent:   username == "" || xmlUsername == "" || username == xmlUsername,
	}
	item.ParseStatus = "parsed"
	item.ParseError = ""
	item.Timestamp = asInt64(timeline.directText("createTime", "create_time"))
	if item.Timestamp > 0 {
		item.Time = time.Unix(item.Timestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
	}
	item.Text = timeline.directText("contentDesc")
	item.Location = xmlLocation(timeline)
	contentObject := timeline.direct("ContentObject")
	if contentObject == nil {
		contentObject = timeline.descendant("ContentObject")
	}
	item.TypeCode = parseOptionalInt(contentObject.directText("type"))
	link := &MomentLink{
		Title: contentObject.directText("title"), Description: contentObject.directText("description"),
		URL:            safeRemoteURL(firstNonEmpty(contentObject.directText("contentUrl", "content_url"), contentObject.directText("url"))),
		SourceUsername: contentObject.directText("sourceUserName"), SourceName: contentObject.directText("sourceNickName"),
	}
	if link.Title != "" || link.Description != "" || link.URL != "" || link.SourceUsername != "" || link.SourceName != "" {
		item.Link = link
	}
	videoKey := timelineVideoKey(timeline)
	for index, node := range semanticMediaNodes(contentObject) {
		media := mediaItem(node, index+1)
		if media.Kind == "video" && videoKey != "" {
			media.remote.Original.Key = videoKey
		}
		media.EvidenceID = item.EvidenceID + ":media:" + strconv.Itoa(media.Index)
		item.Media = append(item.Media, media)
	}
	item.Interactions = parseMomentInteractions(root, item.EvidenceID)
	if !item.Identity.MomentIDConsistent || !item.Identity.AuthorConsistent {
		item.ParseStatus = "identity_conflict"
		item.Interactions.ParseStatus = "identity_conflict"
		for index := range item.Media {
			item.Media[index].ResolutionStatus = "identity_conflict"
			item.Media[index].VerifiedBy = ""
		}
		for interactionIndex := range item.Interactions.Comments {
			for mediaIndex := range item.Interactions.Comments[interactionIndex].Media {
				media := &item.Interactions.Comments[interactionIndex].Media[mediaIndex]
				media.ResolutionStatus = "identity_conflict"
				media.VerifiedBy = ""
			}
		}
	}
	return item
}

func MomentContacts(root, keyword string, limit int) (MomentContactReport, error) {
	files, err := sqliteFiles(root)
	if err != nil {
		return MomentContactReport{}, err
	}
	displays := contactDisplays(root)
	counts := map[string]int{}
	available := false
	for _, path := range files {
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "SnsTimeLine")
		if table == "" {
			_ = database.Close()
			continue
		}
		available = true
		availableColumns := columns(database, table)
		usernameColumn := columnCI(availableColumns, "user_name")
		if usernameColumn == "" {
			_ = database.Close()
			continue
		}
		rows, queryErr := database.Query("SELECT " + quoteIdentifier(usernameColumn) + ", COUNT(*) FROM " + quoteIdentifier(table) + " WHERE " + quoteIdentifier(usernameColumn) + " IS NOT NULL AND " + quoteIdentifier(usernameColumn) + " <> '' GROUP BY " + quoteIdentifier(usernameColumn))
		if queryErr == nil {
			for rows.Next() {
				var username any
				var count int
				if rows.Scan(&username, &count) == nil {
					counts[asString(username)] += count
				}
			}
			_ = rows.Close()
		}
		_ = database.Close()
	}
	needle := strings.ToLower(strings.TrimSpace(keyword))
	items := make([]MomentContact, 0, len(counts))
	for username, count := range counts {
		display := firstNonEmpty(displays[username], username)
		if needle != "" && !strings.Contains(strings.ToLower(username+"\n"+display), needle) {
			continue
		}
		items = append(items, MomentContact{Username: username, DisplayName: display, LocalMomentCount: count})
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].LocalMomentCount == items[right].LocalMomentCount {
			return strings.ToLower(items[left].DisplayName) < strings.ToLower(items[right].DisplayName)
		}
		return items[left].LocalMomentCount > items[right].LocalMomentCount
	})
	matched := len(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	reason := ""
	if !available {
		reason = "sns_database_or_timeline_missing"
	}
	return MomentContactReport{
		Items: items, Available: available, Reason: reason, Returned: len(items),
		MatchingContacts: matched, MatchingContactsTruncated: matched > len(items),
		Scope: "locally_retained_only",
	}, nil
}

func Moments(root, username string, start, end *int64, limit int) (MomentReport, error) {
	if strings.TrimSpace(username) == "" {
		return MomentReport{}, fmt.Errorf("朋友圈联系人 username 不能为空")
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return MomentReport{}, err
	}
	displays := contactDisplays(root)
	items := []Moment{}
	seen := map[string]bool{}
	coverage := map[string]any{
		"available": false, "adapter": "sns-timeline-xml-v1", "author_username": username,
		"source_databases": 0, "local_rows_for_author": 0, "rows_inspected": 0,
		"parsed": 0, "identity_conflicts": 0, "unparsed": 0,
		"time_unresolved_in_inspected_rows": 0, "logical_media": 0,
		"post_logical_media": 0, "comment_logical_media": 0,
		"visible_likes": 0, "visible_comments": 0, "visible_replies": 0,
		"unresolved_visible_replies": 0, "deleted_visible_interactions": 0,
		"interaction_metadata_unavailable": 0, "comment_media_expected": 0,
		"comment_media_parsed": 0, "comment_media_metadata_incomplete": 0,
		"comment_emojis_expected": 0,
		"verified_local_media":    0, "scope": "locally_retained_only",
		"interaction_scope":            "locally_retained_visible_only",
		"complete_interaction_history": false,
		"complete_remote_history":      false, "remote_fetch_attempted": false,
	}
	missingColumns := map[string]bool{}
	for _, path := range files {
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "SnsTimeLine")
		if table == "" {
			_ = database.Close()
			continue
		}
		coverage["available"] = true
		availableColumns := columns(database, table)
		tidColumn := columnCI(availableColumns, "tid")
		usernameColumn := columnCI(availableColumns, "user_name")
		contentColumn := columnCI(availableColumns, "content")
		for name, value := range map[string]string{"tid": tidColumn, "user_name": usernameColumn, "content": contentColumn} {
			if value == "" {
				missingColumns[name] = true
			}
		}
		if tidColumn == "" || usernameColumn == "" || contentColumn == "" {
			_ = database.Close()
			continue
		}
		coverage["source_databases"] = coverage["source_databases"].(int) + 1
		var count int
		_ = database.QueryRow("SELECT COUNT(*) FROM "+quoteIdentifier(table)+" WHERE "+quoteIdentifier(usernameColumn)+" = ?", username).Scan(&count)
		coverage["local_rows_for_author"] = coverage["local_rows_for_author"].(int) + count
		rows, queryErr := database.Query("SELECT "+quoteIdentifier(tidColumn)+", "+quoteIdentifier(usernameColumn)+", "+quoteIdentifier(contentColumn)+" FROM "+quoteIdentifier(table)+" WHERE "+quoteIdentifier(usernameColumn)+" = ? ORDER BY rowid DESC", username)
		if queryErr != nil {
			_ = database.Close()
			continue
		}
		relative, _ := filepathRel(root, path)
		for rows.Next() {
			var tid, author, content any
			if rows.Scan(&tid, &author, &content) != nil {
				continue
			}
			coverage["rows_inspected"] = coverage["rows_inspected"].(int) + 1
			item := parseMoment(tid, author, content, displays[username], relative)
			if item.Timestamp == 0 {
				coverage["time_unresolved_in_inspected_rows"] = coverage["time_unresolved_in_inspected_rows"].(int) + 1
			}
			if start != nil && (item.Timestamp == 0 || item.Timestamp < *start) {
				continue
			}
			if end != nil && (item.Timestamp == 0 || item.Timestamp > *end) {
				continue
			}
			if seen[item.EvidenceID] {
				continue
			}
			seen[item.EvidenceID] = true
			items = append(items, item)
		}
		if rowErr := rows.Err(); rowErr != nil {
			_ = rows.Close()
			_ = database.Close()
			return MomentReport{}, rowErr
		}
		_ = rows.Close()
		_ = database.Close()
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].Timestamp == items[right].Timestamp {
			return items[left].EvidenceID > items[right].EvidenceID
		}
		return items[left].Timestamp > items[right].Timestamp
	})
	matched := len(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		switch item.ParseStatus {
		case "parsed":
			coverage["parsed"] = coverage["parsed"].(int) + 1
		case "identity_conflict":
			coverage["identity_conflicts"] = coverage["identity_conflicts"].(int) + 1
		default:
			coverage["unparsed"] = coverage["unparsed"].(int) + 1
		}
		for _, media := range item.Media {
			if media.ResolutionStatus == "logical_only" {
				coverage["logical_media"] = coverage["logical_media"].(int) + 1
				coverage["post_logical_media"] = coverage["post_logical_media"].(int) + 1
			}
		}
		applyMomentInteractionCoverage(item, coverage)
	}
	missing := make([]string, 0, len(missingColumns))
	for name := range missingColumns {
		missing = append(missing, name)
	}
	sort.Strings(missing)
	coverage["missing_required_columns"] = missing
	coverage["matching_rows"] = matched
	coverage["returned"] = len(items)
	coverage["truncated"] = matched > len(items)
	return MomentReport{Items: items, Coverage: coverage}, nil
}

func applyMomentInteractionCoverage(item Moment, coverage map[string]any) {
	if item.Interactions.ParseStatus == "unavailable" {
		coverage["interaction_metadata_unavailable"] = coverage["interaction_metadata_unavailable"].(int) + 1
	}
	coverage["visible_likes"] = coverage["visible_likes"].(int) + len(item.Interactions.Likes)
	coverage["visible_comments"] = coverage["visible_comments"].(int) + len(item.Interactions.Comments)
	for _, interaction := range append(append([]MomentInteraction{}, item.Interactions.Likes...), item.Interactions.Comments...) {
		if interaction.Deleted {
			coverage["deleted_visible_interactions"] = coverage["deleted_visible_interactions"].(int) + 1
		}
		if interaction.ReplyTo != nil {
			coverage["visible_replies"] = coverage["visible_replies"].(int) + 1
			if !interaction.ReplyTo.Resolved {
				coverage["unresolved_visible_replies"] = coverage["unresolved_visible_replies"].(int) + 1
			}
		}
		if interaction.Kind == "comment" {
			coverage["comment_media_expected"] = coverage["comment_media_expected"].(int) + interaction.ExpectedMedia
			coverage["comment_media_parsed"] = coverage["comment_media_parsed"].(int) + len(interaction.Media)
			coverage["comment_emojis_expected"] = coverage["comment_emojis_expected"].(int) + interaction.ExpectedEmojis
			if interaction.ExpectedMedia > len(interaction.Media) {
				coverage["comment_media_metadata_incomplete"] = coverage["comment_media_metadata_incomplete"].(int) + 1
			}
		}
		for _, media := range interaction.Media {
			if media.ResolutionStatus == "logical_only" {
				coverage["logical_media"] = coverage["logical_media"].(int) + 1
				coverage["comment_logical_media"] = coverage["comment_logical_media"].(int) + 1
			}
		}
	}
}

func momentMatchedFields(item Moment, keyword string) []string {
	keyword = strings.ToLower(keyword)
	var fields []string
	match := func(name, value string) {
		if value != "" && strings.Contains(strings.ToLower(value), keyword) {
			fields = append(fields, name)
		}
	}
	match("text", item.Text)
	for name, value := range item.Location {
		match("location."+name, value)
	}
	if item.Link != nil {
		match("link.title", item.Link.Title)
		match("link.description", item.Link.Description)
		match("link.source_name", item.Link.SourceName)
	}
	for _, media := range item.Media {
		match(fmt.Sprintf("media.%d.title", media.Index), media.Title)
		match(fmt.Sprintf("media.%d.description", media.Index), media.Description)
	}
	for _, interaction := range item.Interactions.Likes {
		prefix := "interactions.likes." + interaction.InteractionID
		match(prefix+".actor.username", interaction.Actor.Username)
		match(prefix+".actor.display_name", interaction.Actor.DisplayName)
	}
	for _, interaction := range item.Interactions.Comments {
		prefix := "interactions.comments." + interaction.InteractionID
		match(prefix+".actor.username", interaction.Actor.Username)
		match(prefix+".actor.display_name", interaction.Actor.DisplayName)
		match(prefix+".content", interaction.Content)
		for _, media := range interaction.Media {
			match(fmt.Sprintf("%s.media.%d.title", prefix, media.Index), media.Title)
			match(fmt.Sprintf("%s.media.%d.description", prefix, media.Index), media.Description)
		}
	}
	sort.Strings(fields)
	return fields
}

func SearchMoments(root, keyword, contact string, start, end *int64, limit int) (MomentReport, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return MomentReport{}, fmt.Errorf("朋友圈搜索关键词不能为空")
	}
	contacts := []MomentContact{{Username: contact}}
	if contact == "" {
		report, err := MomentContacts(root, "", 0)
		if err != nil {
			return MomentReport{}, err
		}
		contacts = report.Items
	}
	items := []Moment{}
	coverage := map[string]any{
		"available": false, "contacts_inspected": 0, "rows_inspected": 0,
		"scope": "locally_retained_only", "complete_remote_history": false,
		"remote_fetch_attempted": false, "interaction_scope": "locally_retained_visible_only",
		"complete_interaction_history": false, "logical_media": 0,
		"post_logical_media": 0, "comment_logical_media": 0,
		"visible_likes": 0, "visible_comments": 0, "visible_replies": 0,
		"unresolved_visible_replies": 0, "deleted_visible_interactions": 0,
		"interaction_metadata_unavailable": 0, "comment_media_expected": 0,
		"comment_media_parsed": 0, "comment_media_metadata_incomplete": 0,
		"comment_emojis_expected": 0,
	}
	for _, candidate := range contacts {
		report, err := Moments(root, candidate.Username, start, end, 0)
		if err != nil {
			return MomentReport{}, err
		}
		if available, _ := report.Coverage["available"].(bool); available {
			coverage["available"] = true
		}
		coverage["contacts_inspected"] = coverage["contacts_inspected"].(int) + 1
		if inspected, ok := report.Coverage["rows_inspected"].(int); ok {
			coverage["rows_inspected"] = coverage["rows_inspected"].(int) + inspected
		}
		for _, item := range report.Items {
			item.MatchedFields = momentMatchedFields(item, keyword)
			if len(item.MatchedFields) > 0 {
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].Timestamp == items[right].Timestamp {
			return items[left].EvidenceID > items[right].EvidenceID
		}
		return items[left].Timestamp > items[right].Timestamp
	})
	matched := len(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		for _, media := range item.Media {
			if media.ResolutionStatus == "logical_only" {
				coverage["logical_media"] = coverage["logical_media"].(int) + 1
				coverage["post_logical_media"] = coverage["post_logical_media"].(int) + 1
			}
		}
		applyMomentInteractionCoverage(item, coverage)
	}
	coverage["matching_rows"] = matched
	coverage["returned"] = len(items)
	coverage["truncated"] = matched > len(items)
	coverage["matched_fields_available"] = true
	return MomentReport{Items: items, Coverage: coverage}, nil
}

func filepathRel(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path, err
	}
	return filepath.ToSlash(relative), nil
}
