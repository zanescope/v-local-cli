package store

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type officialRoundTripFunc func(*http.Request) (*http.Response, error)

func (function officialRoundTripFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCanonicalOfficialArticleURLStripsSessionParameters(t *testing.T) {
	value := "http://mp.weixin.qq.com/s?__biz=MzA1234%3D&mid=2247483647&idx=1&sn=0123456789abcdef0123456789abcdef&chksm=abcdef0123456789&scene=21&pass_ticket=secret#rd"
	target, err := canonicalOfficialArticleURL(value)
	if err != nil {
		t.Fatal(err)
	}
	query := target.Query()
	if target.Scheme != "https" || target.Host != "mp.weixin.qq.com" || query.Get("pass_ticket") != "" || query.Get("scene") != "" {
		t.Fatalf("公众号文章 URL 未正确归一化：%s", target.Redacted())
	}
	if query.Get("__biz") == "" || query.Get("mid") == "" || query.Get("idx") == "" || query.Get("sn") == "" {
		t.Fatalf("公众号文章公开标识被错误移除：%s", target.Redacted())
	}
	if _, err := canonicalOfficialArticleURL("https://example.com/s?__biz=x"); err == nil {
		t.Fatal("不应接受非微信公众平台域名")
	}
}

func TestCanonicalOfficialArticleURLAcceptsOnlyRestrictedShortPaths(t *testing.T) {
	target, err := canonicalOfficialArticleURL("http://mp.weixin.qq.com/s/Abc_123-xyz?scene=21&pass_ticket=secret#rd")
	if err != nil {
		t.Fatal(err)
	}
	if target.Scheme != "https" || target.Host != "mp.weixin.qq.com" || target.Path != "/s/Abc_123-xyz" || target.RawQuery != "" || target.Fragment != "" {
		t.Fatalf("公众号短路径未正确归一化：%s", target.Redacted())
	}
	for _, value := range []string{
		"https://mp.weixin.qq.com/s/short",
		"https://mp.weixin.qq.com/s/valid-token/extra",
		"https://mp.weixin.qq.com/s/%2e%2e%2fsecret",
		"https://example.com/s/Abc_123-xyz",
	} {
		if _, err := canonicalOfficialArticleURL(value); err == nil {
			t.Fatalf("不应接受公众号短路径：%s", value)
		}
	}
}

func TestFetchOfficialArticleExtractsVerifiedBody(t *testing.T) {
	publication := OfficialPublication{
		EvidenceID: "publication:gh_test:9001:1", EvidenceType: "official_publication",
		Account: MomentAuthor{Username: "gh_test", DisplayName: "测试公众号"},
		Title:   "卡片标题", Description: "卡片摘要", Author: "卡片作者", Timestamp: 1700000000,
		URL:      "https://mp.weixin.qq.com/s?__biz=MzA1234%3D&mid=2247483647&idx=1&sn=0123456789abcdef0123456789abcdef&pass_ticket=secret",
		SourceDB: "biz_message/biz_message_0.db",
	}
	page := `<!doctype html><html><head><meta property="og:title" content="远端标题"><meta name="author" content="远端作者"></head><body><div id="js_content"><p>第一段</p><p>第二段<strong>加粗</strong></p><script>不要读取</script></div></body></html>`
	client := officialRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "mp.weixin.qq.com" || request.URL.Query().Get("pass_ticket") != "" {
			t.Fatalf("请求目标或查询参数异常：%s", request.URL.Redacted())
		}
		if request.Header.Get("Cookie") != "" || request.Header.Get("Referer") != "" {
			t.Fatal("公众号正文请求不应携带 Cookie 或 Referer")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(page)),
			Request:    request,
		}, nil
	})
	article, err := fetchOfficialArticle(context.Background(), publication, client)
	if err != nil {
		t.Fatal(err)
	}
	if article.Title != "远端标题" || article.Author != "远端作者" ||
		!strings.Contains(article.Text, "第一段") || !strings.Contains(article.Text, "第二段加粗") || strings.Contains(article.Text, "不要读取") {
		t.Fatalf("公众号正文提取异常：%+v", article)
	}
	if article.SourceDomain != "mp.weixin.qq.com" || !article.NetworkAccessPerformed || article.RedirectsFollowed || article.ExternalContentTrusted {
		t.Fatalf("公众号正文网络边界异常：%+v", article)
	}
}

func TestOfficialArticleRejectsPromptPage(t *testing.T) {
	publication := OfficialPublication{EvidenceID: "publication:gh_test:1:1", Title: "卡片标题"}
	_, err := parseOfficialArticleHTML([]byte(`<html><body><p>请完成验证</p></body></html>`), publication)
	articleErr, ok := err.(*OfficialArticleError)
	if !ok || articleErr.Kind != "official_article_body_unavailable" {
		t.Fatalf("提示页不应被误报为正文：%v", err)
	}
}

func TestOfficialArticleDoesNotUseCDNDNSFallback(t *testing.T) {
	dialer := newSafeOfficialDialer()
	if dialer.fallbackResolver != nil {
		t.Fatal("公众号正文请求不应复用仅为朋友圈 CDN 授权的 DNSPod 回退")
	}
}

func TestSafeOfficialClientDisablesAmbientNetworkState(t *testing.T) {
	client := newSafeOfficialHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.TLSClientConfig == nil ||
		transport.TLSClientConfig.MinVersion != tls.VersionTLS12 || transport.TLSClientConfig.InsecureSkipVerify ||
		!transport.DisableKeepAlives {
		t.Fatal("公众号正文客户端未禁用环境代理或未固定 TLS 安全配置")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://mp.weixin.qq.com/s", nil)
	if !errors.Is(client.CheckRedirect(request, nil), http.ErrUseLastResponse) || client.Jar != nil {
		t.Fatal("公众号正文客户端允许重定向或携带 Cookie")
	}
}
