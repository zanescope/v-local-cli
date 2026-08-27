package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxOfficialArticlesPerMessage = 100

type OfficialAccount struct {
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Alias              string `json:"alias,omitempty"`
	Remark             string `json:"remark,omitempty"`
	Nickname           string `json:"nickname,omitempty"`
	RelationshipStatus string `json:"relationship_status"`
	FollowedCandidate  bool   `json:"followed_candidate"`
	HasLocalMessages   bool   `json:"has_local_messages"`
	LocalMessageCount  int    `json:"local_message_count"`
	MessageShards      int    `json:"message_shards"`
}

type OfficialAccountReport struct {
	Items                     []OfficialAccount `json:"items"`
	Available                 bool              `json:"source_present"`
	Matched                   int               `json:"matched"`
	Returned                  int               `json:"returned"`
	Truncated                 bool              `json:"truncated"`
	LocalContactAccounts      int               `json:"local_contact_accounts"`
	AccountsWithLocalMessages int               `json:"accounts_with_local_messages"`
	Scope                     string            `json:"scope"`
}

type OfficialPublication struct {
	EvidenceID        string       `json:"evidence_id"`
	EvidenceType      string       `json:"evidence_type"`
	PublicationID     string       `json:"publication_id"`
	MessageLocalID    int64        `json:"message_local_id,omitempty"`
	MessageServerID   int64        `json:"message_server_id,omitempty"`
	Position          int          `json:"position"`
	Timestamp         int64        `json:"timestamp,omitempty"`
	Time              string       `json:"time,omitempty"`
	ReceivedTimestamp int64        `json:"received_timestamp,omitempty"`
	ReceivedTime      string       `json:"received_time,omitempty"`
	TimeSource        string       `json:"time_source"`
	Account           MomentAuthor `json:"account"`
	Title             string       `json:"title,omitempty"`
	Description       string       `json:"description,omitempty"`
	URL               string       `json:"url,omitempty"`
	URLDomain         string       `json:"url_domain,omitempty"`
	ThumbnailURL      string       `json:"thumbnail_url,omitempty"`
	Author            string       `json:"author,omitempty"`
	ItemShowType      *int64       `json:"item_show_type,omitempty"`
	DeleteFlag        *int64       `json:"delete_flag,omitempty"`
	ContentLevel      string       `json:"content_level"`
	SourceDB          string       `json:"source_db"`
	MatchedFields     []string     `json:"matched_fields,omitempty"`
}

type OfficialReport struct {
	Items    []OfficialPublication `json:"items"`
	Coverage map[string]any        `json:"official_source_coverage"`
}

type officialMessageRow struct {
	LocalID   int64
	ServerID  int64
	LocalType int64
	Timestamp int64
	Content   string
	SourceDB  string
}

type parsedOfficialArticle struct {
	Position     int
	Title        string
	Description  string
	URL          string
	ThumbnailURL string
	Author       string
	Timestamp    int64
	ItemShowType *int64
	DeleteFlag   *int64
}

type parsedOfficialBundle struct {
	Articles          []parsedOfficialArticle
	DeclaredArticles  int
	Truncated         bool
	PublisherName     string
	PublisherUsername string
}

func officialContactMap(root string) map[string]OfficialAccount {
	result := map[string]OfficialAccount{}
	files, err := sqliteFiles(root)
	if err != nil {
		return result
	}
	for _, path := range files {
		if !strings.EqualFold(filepath.Base(path), "contact.db") {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, "contact")
		if table == "" {
			_ = database.Close()
			continue
		}
		available := columns(database, table)
		usernameColumn := columnCI(available, "username")
		if usernameColumn == "" {
			_ = database.Close()
			continue
		}
		wanted := []string{usernameColumn}
		for _, name := range []string{"alias", "remark", "nick_name", "delete_flag"} {
			if column := columnCI(available, name); column != "" {
				wanted = append(wanted, column)
			} else {
				wanted = append(wanted, "")
			}
		}
		selected := []string{quoteIdentifier(wanted[0])}
		for _, value := range wanted[1:] {
			if value == "" {
				selected = append(selected, "NULL")
			} else {
				selected = append(selected, quoteIdentifier(value))
			}
		}
		rows, queryErr := database.Query("SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier(table))
		if queryErr == nil {
			for rows.Next() {
				var username, alias, remark, nickname, deleted any
				if rows.Scan(&username, &alias, &remark, &nickname, &deleted) != nil {
					continue
				}
				name := asString(username)
				if !strings.HasPrefix(name, "gh_") {
					continue
				}
				active := deleted == nil || asInt64(deleted) == 0
				item := OfficialAccount{
					Username: name, Alias: asString(alias), Remark: asString(remark), Nickname: asString(nickname),
					FollowedCandidate: active,
				}
				item.DisplayName = firstNonEmpty(item.Remark, item.Nickname, item.Username)
				if active {
					item.RelationshipStatus = "local_contact_active"
				} else {
					item.RelationshipStatus = "local_contact_inactive"
				}
				result[name] = item
			}
			_ = rows.Close()
		}
		_ = database.Close()
	}
	return result
}

func officialSessions(root string) (map[string]int, map[string]int, error) {
	counts := map[string]int{}
	shards := map[string]int{}
	files, err := sqliteFiles(root)
	if err != nil {
		return nil, nil, err
	}
	contacts := officialContactMap(root)
	for _, path := range files {
		if !bizMessageDatabase(path) {
			continue
		}
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		usernames := map[string]bool{}
		for username := range contacts {
			usernames[username] = true
		}
		for _, username := range name2ID(database) {
			if strings.HasPrefix(username, "gh_") {
				usernames[username] = true
			}
		}
		for username := range usernames {
			table := findTableCI(database, messageTable(username))
			if table == "" {
				continue
			}
			var count int
			if database.QueryRow("SELECT COUNT(*) FROM "+quoteIdentifier(table)).Scan(&count) == nil {
				counts[username] += count
				shards[username]++
			}
		}
		_ = database.Close()
	}
	return counts, shards, nil
}

func OfficialAccounts(root, keyword string, limit int) (OfficialAccountReport, error) {
	contacts := officialContactMap(root)
	counts, shards, err := officialSessions(root)
	if err != nil {
		return OfficialAccountReport{}, err
	}
	usernames := map[string]bool{}
	for username := range contacts {
		usernames[username] = true
	}
	for username := range counts {
		usernames[username] = true
	}
	needle := strings.ToLower(strings.TrimSpace(keyword))
	items := make([]OfficialAccount, 0, len(usernames))
	for username := range usernames {
		item, found := contacts[username]
		if !found {
			item = OfficialAccount{Username: username, DisplayName: username, RelationshipStatus: "message_history_only"}
		}
		item.LocalMessageCount = counts[username]
		item.MessageShards = shards[username]
		item.HasLocalMessages = item.LocalMessageCount > 0
		haystack := strings.ToLower(strings.Join([]string{item.Username, item.DisplayName, item.Alias, item.Remark, item.Nickname}, "\n"))
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		if items[left].HasLocalMessages != items[right].HasLocalMessages {
			return items[left].HasLocalMessages
		}
		if items[left].FollowedCandidate != items[right].FollowedCandidate {
			return items[left].FollowedCandidate
		}
		return strings.ToLower(items[left].DisplayName) < strings.ToLower(items[right].DisplayName)
	})
	matched := len(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return OfficialAccountReport{
		Items: items, Available: len(usernames) > 0, Matched: matched, Returned: len(items),
		Truncated: matched > len(items), LocalContactAccounts: len(contacts),
		AccountsWithLocalMessages: len(counts), Scope: "locally_known_only",
	}, nil
}

func parseOfficialBundle(content string) (parsedOfficialBundle, string) {
	root, parseError := parseXML(content, true)
	if root == nil {
		return parsedOfficialBundle{}, parseError
	}
	app := root
	if localXMLName(root.Name) != "appmsg" {
		app = root.descendant("appmsg")
	}
	if app == nil {
		return parsedOfficialBundle{}, "appmsg_missing"
	}
	mmreader := app.descendant("mmreader")
	category := mmreader.descendant("category")
	if category == nil {
		return parsedOfficialBundle{}, "article_category_missing"
	}
	bundle := parsedOfficialBundle{}
	if count := parseOptionalInt(category.attribute("count")); count != nil {
		bundle.DeclaredArticles = int(*count)
	}
	if publisher := mmreader.descendant("publisher"); publisher != nil {
		bundle.PublisherName = publisher.directText("nickname")
		bundle.PublisherUsername = publisher.directText("username")
	}
	var nodes []*xmlNode
	for _, child := range category.Children {
		if localXMLName(child.Name) == "item" {
			nodes = append(nodes, child)
		}
	}
	if bundle.DeclaredArticles == 0 {
		bundle.DeclaredArticles = len(nodes)
	}
	if len(nodes) > maxOfficialArticlesPerMessage {
		bundle.Truncated = true
		nodes = nodes[:maxOfficialArticlesPerMessage]
	}
	for index, node := range nodes {
		timestamp := asInt64(firstXMLText(node,
			[]string{"pub_time"}, []string{"pubtime"}, []string{"publish_time"},
		))
		article := parsedOfficialArticle{
			Position:    index + 1,
			Title:       firstXMLText(node, []string{"title"}, []string{"title_v2"}, []string{"text_title"}, []string{"itemtitle"}),
			Description: firstXMLText(node, []string{"digest"}, []string{"des"}, []string{"summary"}),
			URL:         firstXMLText(node, []string{"url"}, []string{"longurl"}, []string{"shorturl"}),
			ThumbnailURL: firstXMLText(node,
				[]string{"cover"}, []string{"share_cover", "cdn_url"}, []string{"cover_16_9"},
				[]string{"cover_1_1"}, []string{"cover_3_4"}, []string{"cover_235_1"},
				[]string{"cover_url"}, []string{"thumburl"},
			),
			Author:       firstNonEmpty(firstXMLText(node, []string{"author"}, []string{"sources", "source", "name"}, []string{"sourcename"}), bundle.PublisherName),
			Timestamp:    timestamp,
			ItemShowType: parseOptionalInt(node.directText("itemshowtype")),
			DeleteFlag:   parseOptionalInt(node.directText("del_flag")),
		}
		if article.Title != "" || article.Description != "" || article.URL != "" {
			bundle.Articles = append(bundle.Articles, article)
		}
	}
	return bundle, ""
}

func readOfficialRows(database *sql.DB, table, source string, start, end *int64) ([]officialMessageRow, error) {
	available := columns(database, table)
	contentColumn := columnCI(available, "message_content")
	if contentColumn == "" {
		return nil, fmt.Errorf("公众号消息表缺少 message_content")
	}
	createColumn := columnCI(available, "create_time")
	if (start != nil || end != nil) && createColumn == "" {
		return nil, fmt.Errorf("公众号消息表缺少 create_time，无法应用时间范围")
	}
	selected := []string{"0", "0", "0", "0", "NULL", "NULL", "0"}
	for index, name := range []string{"local_id", "server_id", "local_type", "create_time", "message_content", "compress_content", "WCDB_CT_message_content"} {
		if column := columnCI(available, name); column != "" {
			selected[index] = quoteIdentifier(column)
		}
	}
	conditions := []string{}
	arguments := []any{}
	if start != nil {
		conditions = append(conditions, quoteIdentifier(createColumn)+" >= ?")
		arguments = append(arguments, *start)
	}
	if end != nil {
		conditions = append(conditions, quoteIdentifier(createColumn)+" <= ?")
		arguments = append(arguments, *end)
	}
	query := "SELECT " + strings.Join(selected, ",") + " FROM " + quoteIdentifier(table)
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY "
	if sortColumn := columnCI(available, "sort_seq"); sortColumn != "" {
		query += quoteIdentifier(sortColumn) + " DESC"
	} else if createColumn != "" {
		query += quoteIdentifier(createColumn) + " DESC"
	} else {
		query += "rowid DESC"
	}
	rows, err := database.Query(query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []officialMessageRow
	for rows.Next() {
		var localID, serverID, localType, timestamp, content, compressed, flag any
		if rows.Scan(&localID, &serverID, &localType, &timestamp, &content, &compressed, &flag) != nil {
			continue
		}
		decoded := decodeValue(content, asInt64(flag))
		if strings.TrimSpace(decoded) == "" {
			decoded = decodeValue(compressed, asInt64(flag))
		}
		result = append(result, officialMessageRow{
			LocalID: asInt64(localID), ServerID: asInt64(serverID), LocalType: asInt64(localType),
			Timestamp: asInt64(timestamp), Content: decoded, SourceDB: source,
		})
	}
	return result, rows.Err()
}

func OfficialHistory(root, publisher string, start, end *int64, limit int) (OfficialReport, error) {
	if !strings.HasPrefix(strings.TrimSpace(publisher), "gh_") {
		return OfficialReport{}, fmt.Errorf("公众号 username 必须以 gh_ 开头")
	}
	accounts := officialContactMap(root)
	display := publisher
	if account, found := accounts[publisher]; found {
		display = account.DisplayName
	}
	files, err := sqliteFiles(root)
	if err != nil {
		return OfficialReport{}, err
	}
	coverage := map[string]any{
		"source_present": false, "account_username": publisher, "account_known": false,
		"message_history_available": false, "matched_message_tables": 0,
		"rows_inspected": 0, "article_messages": 0, "non_article_messages": 0,
		"parse_errors": 0, "unsafe_urls_rejected": 0, "articles": 0,
		"declared_articles_in_bundles": 0, "truncated_article_bundles": 0,
		"content_level": "card_metadata", "article_body_available": false,
		"scope": "locally_received_and_retained_only", "complete_publication_history": false,
		"remote_fetch_attempted": false,
	}
	items := []OfficialPublication{}
	seenRows := map[string]bool{}
	seenPublications := map[string]bool{}
	for _, path := range files {
		if !bizMessageDatabase(path) {
			continue
		}
		coverage["source_present"] = true
		database, openErr := openReadOnly(path)
		if openErr != nil {
			continue
		}
		table := findTableCI(database, messageTable(publisher))
		if table == "" {
			_ = database.Close()
			continue
		}
		coverage["matched_message_tables"] = coverage["matched_message_tables"].(int) + 1
		coverage["message_history_available"] = true
		relative, _ := filepathRel(root, path)
		rows, readErr := readOfficialRows(database, table, relative, start, end)
		_ = database.Close()
		if readErr != nil {
			return OfficialReport{}, readErr
		}
		for _, row := range rows {
			rowIdentity := fmt.Sprintf("%d:%d:%d", row.ServerID, row.LocalID, row.Timestamp)
			if seenRows[rowIdentity] {
				continue
			}
			seenRows[rowIdentity] = true
			coverage["rows_inspected"] = coverage["rows_inspected"].(int) + 1
			bundle, parseError := parseOfficialBundle(row.Content)
			if parseError != "" || len(bundle.Articles) == 0 {
				coverage["non_article_messages"] = coverage["non_article_messages"].(int) + 1
				if parseError != "article_category_missing" && parseError != "appmsg_missing" {
					coverage["parse_errors"] = coverage["parse_errors"].(int) + 1
				}
				continue
			}
			coverage["article_messages"] = coverage["article_messages"].(int) + 1
			coverage["declared_articles_in_bundles"] = coverage["declared_articles_in_bundles"].(int) + bundle.DeclaredArticles
			if bundle.Truncated {
				coverage["truncated_article_bundles"] = coverage["truncated_article_bundles"].(int) + 1
			}
			for _, article := range bundle.Articles {
				messageID := row.ServerID
				if messageID == 0 {
					messageID = row.LocalID
				}
				publicationID := fmt.Sprintf("%d:%d", messageID, article.Position)
				evidenceID := "publication:" + publisher + ":" + publicationID
				if seenPublications[evidenceID] {
					continue
				}
				seenPublications[evidenceID] = true
				timestamp := article.Timestamp
				timeSource := "article"
				if timestamp == 0 {
					timestamp = row.Timestamp
					timeSource = "message"
				}
				articleURL := safeRemoteURL(article.URL)
				thumbnail := safeRemoteURL(article.ThumbnailURL)
				if article.URL != "" && articleURL == "" {
					coverage["unsafe_urls_rejected"] = coverage["unsafe_urls_rejected"].(int) + 1
				}
				if article.ThumbnailURL != "" && thumbnail == "" {
					coverage["unsafe_urls_rejected"] = coverage["unsafe_urls_rejected"].(int) + 1
				}
				domain := ""
				if articleURL != "" {
					parsed, _ := url.Parse(articleURL)
					domain = strings.ToLower(parsed.Host)
				}
				item := OfficialPublication{
					EvidenceID: evidenceID, EvidenceType: "official_publication", PublicationID: publicationID,
					MessageLocalID: row.LocalID, MessageServerID: row.ServerID, Position: article.Position,
					Timestamp: timestamp, ReceivedTimestamp: row.Timestamp, TimeSource: timeSource,
					Account: MomentAuthor{Username: publisher, DisplayName: display},
					Title:   article.Title, Description: article.Description, URL: articleURL,
					URLDomain: domain, ThumbnailURL: thumbnail, Author: article.Author,
					ItemShowType: article.ItemShowType, DeleteFlag: article.DeleteFlag,
					ContentLevel: "card_metadata", SourceDB: row.SourceDB,
				}
				if timestamp > 0 {
					item.Time = time.Unix(timestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
				}
				if row.Timestamp > 0 {
					item.ReceivedTime = time.Unix(row.Timestamp, 0).In(time.Local).Format("2006-01-02 15:04:05")
				}
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
	coverage["account_known"] = accounts[publisher].Username != "" || coverage["message_history_available"].(bool)
	coverage["articles"] = matched
	coverage["returned"] = len(items)
	coverage["truncated"] = matched > len(items)
	return OfficialReport{Items: items, Coverage: coverage}, nil
}

func officialMatchedFields(item OfficialPublication, keyword string) []string {
	keyword = strings.ToLower(keyword)
	var fields []string
	match := func(name, value string) {
		if value != "" && strings.Contains(strings.ToLower(value), keyword) {
			fields = append(fields, name)
		}
	}
	match("title", item.Title)
	match("description", item.Description)
	match("author", item.Author)
	match("account.display_name", item.Account.DisplayName)
	sort.Strings(fields)
	return fields
}

func SearchOfficial(root, keyword, publisher string, start, end *int64, limit int) (OfficialReport, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return OfficialReport{}, fmt.Errorf("公众号搜索关键词不能为空")
	}
	publishers := []string{publisher}
	if publisher == "" {
		accounts, err := OfficialAccounts(root, "", 0)
		if err != nil {
			return OfficialReport{}, err
		}
		publishers = make([]string, 0, len(accounts.Items))
		for _, account := range accounts.Items {
			publishers = append(publishers, account.Username)
		}
	}
	items := []OfficialPublication{}
	coverage := map[string]any{
		"source_present": false, "publishers_inspected": 0, "rows_inspected": 0,
		"scope": "locally_received_and_retained_only", "content_level": "card_metadata",
		"article_body_available": false, "complete_publication_history": false,
		"remote_fetch_attempted": false, "matched_fields_available": true,
	}
	for _, candidate := range publishers {
		report, err := OfficialHistory(root, candidate, start, end, 0)
		if err != nil {
			return OfficialReport{}, err
		}
		if present, _ := report.Coverage["source_present"].(bool); present {
			coverage["source_present"] = true
		}
		coverage["publishers_inspected"] = coverage["publishers_inspected"].(int) + 1
		if inspected, ok := report.Coverage["rows_inspected"].(int); ok {
			coverage["rows_inspected"] = coverage["rows_inspected"].(int) + inspected
		}
		for _, item := range report.Items {
			item.MatchedFields = officialMatchedFields(item, keyword)
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
	coverage["matching_publications"] = matched
	coverage["returned"] = len(items)
	coverage["truncated"] = matched > len(items)
	return OfficialReport{Items: items, Coverage: coverage}, nil
}

func bizMessageDatabase(path string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Base(path)), "biz_message_")
}
