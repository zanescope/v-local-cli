package store

import (
	"strings"
	"testing"
)

func FuzzParseXML(f *testing.F) {
	f.Add("<msg><title>example</title></msg>", false)
	f.Add("&lt;msg&gt;&lt;title&gt;example&lt;/title&gt;&lt;/msg&gt;", true)
	f.Add("<!DOCTYPE msg [<!ENTITY x 'bad'>]><msg>&x;</msg>", false)
	// 深度守卫的边界种子：略微超过 maxXMLDepth，让模糊测试在上限附近展开。
	f.Add(strings.Repeat("<a>", maxXMLDepth+8)+strings.Repeat("</a>", maxXMLDepth+8), false)
	f.Fuzz(func(t *testing.T, value string, unescape bool) {
		if len(value) > maxXMLBytes+1024 {
			t.Skip()
		}
		node, status := parseXML(value, unescape)
		upper := strings.ToUpper(value)
		if (strings.Contains(upper, "<!DOCTYPE") || strings.Contains(upper, "<!ENTITY")) && (node != nil || status == "") {
			t.Fatalf("危险 XML 声明被接受")
		}
		if node != nil && xmlTreeDepth(node) > maxXMLDepth {
			t.Fatalf("解析成功的 XML 树深度 %d 超过上限 %d", xmlTreeDepth(node), maxXMLDepth)
		}
	})
}

func FuzzBuildMomentMediaURL(f *testing.F) {
	f.Add("http://mmsns.qpic.cn/example", "token", "image")
	f.Add("https://example.wxsns.video.qq.com/snsvideodownload", "video-token", "video")
	f.Add("file:///etc/passwd", "token", "image")
	f.Fuzz(func(t *testing.T, rawURL, token, kind string) {
		if len(rawURL) > 8192 || len(token) > maxMomentRemoteTokenBytes+128 || len(kind) > 32 {
			t.Skip()
		}
		target, err := buildMomentMediaURL(momentRemoteVariant{URL: rawURL, Token: token}, kind)
		if err != nil {
			return
		}
		if target.Scheme != "https" || target.User != nil || target.Fragment != "" || target.Port() != "" {
			t.Fatalf("成功构造的媒体 URL 越出 HTTPS 限制：%s", target.Redacted())
		}
		query := target.Query()
		if query.Get("token") != strings.TrimSpace(token) || query.Get("idx") != "1" {
			t.Fatalf("令牌或索引未精确绑定")
		}
	})
}

func FuzzCanonicalOfficialArticleURL(f *testing.F) {
	f.Add("http://mp.weixin.qq.com/s?__biz=MzA1234%3D&mid=2247483647&idx=1&sn=0123456789abcdef0123456789abcdef")
	f.Add("https://mp.weixin.qq.com/s/Abc_123-xyz?pass_ticket=secret")
	f.Add("https://user@mp.weixin.qq.com:8443/s/../../admin")
	f.Fuzz(func(t *testing.T, rawURL string) {
		if len(rawURL) > 8192 {
			t.Skip()
		}
		target, err := canonicalOfficialArticleURL(rawURL)
		if err != nil {
			return
		}
		if target.Scheme != "https" || target.Host != "mp.weixin.qq.com" || target.User != nil ||
			target.Port() != "" || target.Fragment != "" ||
			(target.Path != "/s" && !officialShortPath.MatchString(target.Path)) {
			t.Fatalf("成功归一化的公众号 URL 越出目标边界：%s", target.Redacted())
		}
		query := target.Query()
		if target.Path != "/s" && len(query) != 0 {
			t.Fatalf("公众号短路径保留了查询参数：%s", target.Redacted())
		}
		for key := range query {
			switch key {
			case "__biz", "mid", "idx", "sn", "chksm":
			default:
				t.Fatalf("公众号参数端点保留了未授权字段 %q", key)
			}
		}
	})
}

func FuzzParseOfficialArticleHTML(f *testing.F) {
	f.Add([]byte(`<html><body><div id="js_content"><p>正文</p></div></body></html>`))
	f.Add([]byte(`<html><body><script>ignore</script></body></html>`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > 1024*1024 {
			t.Skip()
		}
		article, err := parseOfficialArticleHTML(payload, OfficialPublication{EvidenceID: "publication:gh_fuzz:1:1"})
		if err == nil && (strings.TrimSpace(article.Text) == "" || len(article.Text) > maxOfficialTextBytes || article.ExternalContentTrusted) {
			t.Fatalf("公众号 HTML 解析返回了越界正文")
		}
	})
}
