package app

import (
	"context"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/zanescope/v-local-cli/internal/store"
)

func officialArticleCommandError(err error) error {
	var articleErr *store.OfficialArticleError
	if !errors.As(err, &articleErr) {
		return err
	}
	result := &commandError{typeName: articleErr.Kind, message: "公众号正文获取失败", code: 5}
	switch articleErr.Kind {
	case "official_article_evidence_invalid":
		result.message = "公众号文章证据标识无效"
		result.hint = "先用 official-history 或 official-search 取得 publication: 开头的 evidence_id。"
	case "official_article_evidence_unavailable":
		result.message = "当前快照中没有找到该公众号文章卡片"
		result.hint = "刷新快照后重新取得 evidence_id；不要把 URL 直接作为命令参数。"
	case "official_article_url_rejected":
		result.message = "该卡片不是可安全联网获取的微信公众平台文章 URL"
		result.hint = "当前只允许 mp.weixin.qq.com 的公开文章端点，并会丢弃会话票据和跟踪参数。"
	case "official_article_redirect_rejected":
		result.message = "微信文章端点返回了重定向，CLI 已拒绝跟随"
		result.hint = "重新取得最新卡片；CLI 不会把文章标识转发到其他域名。"
	case "official_article_body_unavailable":
		result.message = "响应中没有可验明的公众号正文"
		result.hint = "文章可能已删除、需要验证或只返回提示页；CLI 不会把提示页误报为正文。"
	case "official_article_rate_limited":
		result.message = "微信文章端点暂时限制了访问频率"
		result.hint = "稍后按同一 evidence_id 重试，不要并发批量抓取。"
	case "official_article_http_status":
		result.message = "微信文章端点没有返回可用正文"
		result.hint = "稍后按同一 evidence_id 重试；不要绕过目标域、Cookie 或重定向限制。"
	case "official_article_authorization_rejected":
		result.message = "微信文章端点拒绝了无会话访问"
		result.hint = "CLI 不会读取或发送浏览器、微信会话 Cookie；该文章当前无法安全取得正文。"
	case "official_article_request_failed":
		result.message = "公众号文章网络请求失败"
		result.hint = "检查网络和系统 DNS 后按同一 evidence_id 重试；若系统只返回 TUN fake-IP，CLI 会拒绝连接且不会为公众号域启用外部 DNS 回退。"
	case "official_article_response_size_invalid", "official_article_content_type_rejected", "official_article_html_invalid":
		result.message = "公众号文章响应未通过格式和大小检查"
		result.hint = "CLI 只接受不超过 8 MiB 的 HTML，并要求存在明确的公众号正文节点。"
	default:
		result.hint = "稍后按同一 evidence_id 重试。"
	}
	return result
}

func runOfficialArticle(args []string) (any, error) {
	set := flag.NewFlagSet("official-article", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	account := set.String("account", "", "已初始化账号")
	allowNetwork := set.Bool("allow-network", false, "允许本次访问微信文章域")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli official-article [--account NAME] [--allow-network] <publication_evidence_id>")
	}
	value, err := resolveInitializedAccount(*account)
	if err != nil {
		return nil, err
	}
	if _, err := store.ValidateOfficialArticle(value.SnapshotPath, set.Args()[0]); err != nil {
		return nil, officialArticleCommandError(err)
	}
	if !*allowNetwork {
		return nil, &commandError{
			typeName: "official_article_network_authorization_required",
			message:  "获取公众号正文需要本次联网授权",
			hint:     "说明影响并取得明确同意后，对同一 evidence_id 增加 --allow-network；不要改传 URL。",
			details: map[string]any{
				"destination": "mp.weixin.qq.com", "sends_chat_content": false, "sends_cookie": false,
				"sends_publication_identifiers": true, "redirects_followed": false,
				"tun_fake_ip_dns_fallback": false, "network_access_performed": false,
			},
			code: 5,
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	article, err := store.FetchOfficialArticle(ctx, value.SnapshotPath, set.Args()[0])
	if err != nil {
		return nil, officialArticleCommandError(err)
	}
	output := outputWithTimeWindow(map[string]any{
		"account": value.AccountName, "article": article,
	}, timeWindow{}, true)
	output.meta["network_access_performed"] = true
	output.meta["network_destination"] = "mp.weixin.qq.com"
	output.meta["redirects_followed"] = false
	return withGeneration(output, value), nil
}
