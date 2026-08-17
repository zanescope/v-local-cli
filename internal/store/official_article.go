package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const (
	maxOfficialHTMLBytes = 8 * 1024 * 1024
	maxOfficialTextBytes = 2 * 1024 * 1024
)

var officialShortPath = regexp.MustCompile(`^/s/[A-Za-z0-9_-]{6,256}$`)
var officialBizValue = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{4,128}$`)
var officialNumericValue = regexp.MustCompile(`^[0-9]{1,32}$`)
var officialHexValue = regexp.MustCompile(`^[A-Fa-f0-9]{8,128}$`)

type OfficialArticle struct {
	EvidenceID             string       `json:"evidence_id"`
	EvidenceType           string       `json:"evidence_type"`
	Account                MomentAuthor `json:"account"`
	Title                  string       `json:"title,omitempty"`
	Description            string       `json:"description,omitempty"`
	Author                 string       `json:"author,omitempty"`
	Timestamp              int64        `json:"timestamp,omitempty"`
	Time                   string       `json:"time,omitempty"`
	Text                   string       `json:"text"`
	ContentLevel           string       `json:"content_level"`
	SourceDomain           string       `json:"source_domain"`
	SourceDB               string       `json:"source_db"`
	FetchedAt              string       `json:"fetched_at"`
	ResponseBytes          int          `json:"response_bytes"`
	ResponseSHA256         string       `json:"response_sha256"`
	TextSHA256             string       `json:"text_sha256"`
	NetworkAccessPerformed bool         `json:"network_access_performed"`
	RedirectsFollowed      bool         `json:"redirects_followed"`
	ExternalContentTrusted bool         `json:"external_content_trusted"`
}

type OfficialArticleError struct {
	Kind string
}

func (err *OfficialArticleError) Error() string { return err.Kind }

// FindOfficialPublication 根据本地证据标识重新解析卡片，绝不接受调用方直接传入 URL。
func FindOfficialPublication(root, evidenceID string) (OfficialPublication, error) {
	parts := strings.Split(evidenceID, ":")
	if len(parts) != 4 || parts[0] != "publication" || !strings.HasPrefix(parts[1], "gh_") {
		return OfficialPublication{}, &OfficialArticleError{Kind: "official_article_evidence_invalid"}
	}
	messageID, messageErr := strconv.ParseInt(parts[2], 10, 64)
	position, positionErr := strconv.Atoi(parts[3])
	if messageErr != nil || messageID <= 0 || positionErr != nil || position < 1 || position > maxOfficialArticlesPerMessage {
		return OfficialPublication{}, &OfficialArticleError{Kind: "official_article_evidence_invalid"}
	}
	report, err := OfficialHistory(root, parts[1], nil, nil, 0)
	if err != nil {
		return OfficialPublication{}, err
	}
	for _, item := range report.Items {
		if item.EvidenceID == evidenceID {
			return item, nil
		}
	}
	return OfficialPublication{}, &OfficialArticleError{Kind: "official_article_evidence_unavailable"}
}

// canonicalOfficialArticleURL 只保留访问公开文章所需的标识字段，丢弃会话、跟踪和临时票据。
func canonicalOfficialArticleURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Port() != "" ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		!strings.EqualFold(parsed.Hostname(), "mp.weixin.qq.com") {
		return nil, &OfficialArticleError{Kind: "official_article_url_rejected"}
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		path = "/s"
	}
	if path != "/s" && !officialShortPath.MatchString(path) {
		return nil, &OfficialArticleError{Kind: "official_article_url_rejected"}
	}
	canonical := &url.URL{Scheme: "https", Host: "mp.weixin.qq.com", Path: path}
	if path != "/s" {
		return canonical, nil
	}
	query := parsed.Query()
	biz, mid, index, signature := query.Get("__biz"), query.Get("mid"), query.Get("idx"), query.Get("sn")
	if !officialBizValue.MatchString(biz) || !officialNumericValue.MatchString(mid) ||
		!officialNumericValue.MatchString(index) || !officialHexValue.MatchString(signature) {
		return nil, &OfficialArticleError{Kind: "official_article_url_rejected"}
	}
	clean := url.Values{"__biz": []string{biz}, "mid": []string{mid}, "idx": []string{index}, "sn": []string{signature}}
	if checksum := query.Get("chksm"); officialHexValue.MatchString(checksum) {
		clean.Set("chksm", checksum)
	}
	canonical.RawQuery = clean.Encode()
	return canonical, nil
}

type officialHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func newSafeOfficialDialer() *momentSafeDialer {
	return &momentSafeDialer{
		resolver: net.DefaultResolver,
		dialer:   net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second},
	}
}

func newSafeOfficialHTTPClient() *http.Client {
	dialer := newSafeOfficialDialer()
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 * 1024,
		DisableKeepAlives:      true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func nodeAttribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return strings.TrimSpace(attribute.Val)
		}
	}
	return ""
}

func findHTMLNode(root *html.Node, predicate func(*html.Node) bool) *html.Node {
	if root == nil {
		return nil
	}
	if predicate(root) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNode(child, predicate); found != nil {
			return found
		}
	}
	return nil
}

func htmlNodeText(root *html.Node) string {
	var builder strings.Builder
	lastNewline := true
	var walk func(*html.Node, bool)
	block := map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true, "div": true,
		"figcaption": true, "figure": true, "h1": true, "h2": true, "h3": true, "h4": true,
		"h5": true, "h6": true, "header": true, "li": true, "main": true, "ol": true,
		"p": true, "pre": true, "section": true, "table": true, "tr": true, "ul": true,
	}
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "canvas": true, "template": true}
	newline := func() {
		if builder.Len() > 0 && !lastNewline {
			builder.WriteByte('\n')
			lastNewline = true
		}
	}
	walk = func(node *html.Node, hidden bool) {
		if node == nil || builder.Len() > maxOfficialTextBytes {
			return
		}
		if node.Type == html.ElementNode {
			hidden = hidden || skip[strings.ToLower(node.Data)] || nodeAttribute(node, "aria-hidden") == "true"
			if block[strings.ToLower(node.Data)] || strings.EqualFold(node.Data, "br") {
				newline()
			}
		}
		if node.Type == html.TextNode && !hidden {
			text := strings.Map(func(character rune) rune {
				if unicode.IsSpace(character) {
					return ' '
				}
				return character
			}, node.Data)
			if text != "" {
				builder.WriteString(text)
				lastNewline = false
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
		if node.Type == html.ElementNode && block[strings.ToLower(node.Data)] {
			newline()
		}
	}
	walk(root, false)
	lines := strings.Split(builder.String(), "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			normalized = append(normalized, line)
		}
	}
	return strings.TrimSpace(strings.Join(normalized, "\n"))
}

func metaContent(document *html.Node, key string) string {
	node := findHTMLNode(document, func(node *html.Node) bool {
		if node.Type != html.ElementNode || !strings.EqualFold(node.Data, "meta") {
			return false
		}
		return strings.EqualFold(nodeAttribute(node, "property"), key) || strings.EqualFold(nodeAttribute(node, "name"), key)
	})
	return nodeAttribute(node, "content")
}

func parseOfficialArticleHTML(payload []byte, publication OfficialPublication) (OfficialArticle, error) {
	if len(payload) == 0 || len(payload) > maxOfficialHTMLBytes {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_response_size_invalid"}
	}
	document, err := html.Parse(bytes.NewReader(payload))
	if err != nil {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_html_invalid"}
	}
	content := findHTMLNode(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && nodeAttribute(node, "id") == "js_content"
	})
	if content == nil {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_body_unavailable"}
	}
	text := htmlNodeText(content)
	if text == "" || len(text) > maxOfficialTextBytes {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_body_unavailable"}
	}
	title := strings.TrimSpace(metaContent(document, "og:title"))
	if title == "" {
		titleNode := findHTMLNode(document, func(node *html.Node) bool {
			return node.Type == html.ElementNode && nodeAttribute(node, "id") == "activity-name"
		})
		title = htmlNodeText(titleNode)
	}
	author := strings.TrimSpace(metaContent(document, "author"))
	if author == "" {
		authorNode := findHTMLNode(document, func(node *html.Node) bool {
			return node.Type == html.ElementNode && nodeAttribute(node, "id") == "js_name"
		})
		author = htmlNodeText(authorNode)
	}
	if title == "" {
		title = publication.Title
	}
	if author == "" {
		author = publication.Author
	}
	responseDigest := sha256.Sum256(payload)
	textDigest := sha256.Sum256([]byte(text))
	return OfficialArticle{
		EvidenceID: publication.EvidenceID, EvidenceType: "official_article_body", Account: publication.Account,
		Title: title, Description: publication.Description, Author: author,
		Timestamp: publication.Timestamp, Time: publication.Time, Text: text,
		ContentLevel: "remote_article_plain_text", SourceDomain: "mp.weixin.qq.com", SourceDB: publication.SourceDB,
		ResponseBytes: len(payload), ResponseSHA256: hex.EncodeToString(responseDigest[:]),
		TextSHA256: hex.EncodeToString(textDigest[:]), NetworkAccessPerformed: true,
		RedirectsFollowed: false, ExternalContentTrusted: false,
	}, nil
}

func fetchOfficialArticle(ctx context.Context, publication OfficialPublication, client officialHTTPDoer) (OfficialArticle, error) {
	target, err := canonicalOfficialArticleURL(publication.URL)
	if err != nil {
		return OfficialArticle{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_request_failed"}
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; v-local-cli/0.1; +https://github.com/zanescope/v-local-cli)")
	response, err := client.Do(request)
	if err != nil {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_request_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		kind := "official_article_http_status"
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			kind = "official_article_redirect_rejected"
		} else if response.StatusCode == http.StatusTooManyRequests {
			kind = "official_article_rate_limited"
		} else if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
			kind = "official_article_authorization_rejected"
		}
		return OfficialArticle{}, &OfficialArticleError{Kind: kind}
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if contentType != "" && !strings.HasPrefix(contentType, "text/html") && !strings.HasPrefix(contentType, "application/xhtml+xml") {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_content_type_rejected"}
	}
	if response.ContentLength > maxOfficialHTMLBytes {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_response_size_invalid"}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxOfficialHTMLBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxOfficialHTMLBytes {
		return OfficialArticle{}, &OfficialArticleError{Kind: "official_article_response_size_invalid"}
	}
	article, err := parseOfficialArticleHTML(payload, publication)
	if err != nil {
		return OfficialArticle{}, err
	}
	article.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return article, nil
}

// FetchOfficialArticle 从本地证据重新取得公开 URL，并只向微信文章域发送清理后的公开文章标识。
func FetchOfficialArticle(ctx context.Context, root, evidenceID string) (OfficialArticle, error) {
	publication, err := ValidateOfficialArticle(root, evidenceID)
	if err != nil {
		return OfficialArticle{}, err
	}
	return fetchOfficialArticle(ctx, publication, newSafeOfficialHTTPClient())
}

// ValidateOfficialArticle 验证本地证据存在且卡片 URL 能收敛到允许的公开文章端点，不执行联网。
func ValidateOfficialArticle(root, evidenceID string) (OfficialPublication, error) {
	publication, err := FindOfficialPublication(root, evidenceID)
	if err != nil {
		return OfficialPublication{}, err
	}
	if _, err := canonicalOfficialArticleURL(publication.URL); err != nil {
		return OfficialPublication{}, err
	}
	return publication, nil
}
