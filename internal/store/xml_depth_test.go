package store

import (
	"strings"
	"testing"
)

// xmlTreeDepth 返回已解析树的最大层数，供深度守卫的断言使用。
func xmlTreeDepth(node *xmlNode) int {
	if node == nil {
		return 0
	}
	deepest := 0
	for _, child := range node.Children {
		if value := xmlTreeDepth(child); value > deepest {
			deepest = value
		}
	}
	return deepest + 1
}

// nestedXMLPayload 生成 depth 层嵌套；每层 7 字节，因此可在大小闸门以内做到很深。
func nestedXMLPayload(depth int, prefix, suffix string) string {
	var builder strings.Builder
	builder.WriteString(prefix)
	for index := 0; index < depth; index++ {
		builder.WriteString("<a>")
	}
	for index := 0; index < depth; index++ {
		builder.WriteString("</a>")
	}
	builder.WriteString(suffix)
	return builder.String()
}

// 回归：深层嵌套曾耗尽 Go 协程栈，并触发 recover 无法捕获的致命错误。
// 现在必须返回 too_deep，且进程存活。
func TestParseXMLRejectsExcessiveNestingInsteadOfExhaustingStack(t *testing.T) {
	payload := nestedXMLPayload(500_000, "", "")
	if len(payload) > maxXMLBytes {
		t.Fatalf("测试载荷 %d 字节已超过大小闸门 %d，无法验证深度守卫", len(payload), maxXMLBytes)
	}
	node, status := parseXML(payload, false)
	if status != "too_deep" || node != nil {
		t.Fatalf("超深 XML 未被深度守卫拦截：status=%q node!=nil=%v", status, node != nil)
	}
}

// parseAppMessage 是 history/search/export/stats 每条消息都会经过的路径。
func TestParseAppMessageRejectsExcessiveNestingInsteadOfExhaustingStack(t *testing.T) {
	payload := nestedXMLPayload(560_000, "<msg><appmsg>", "</appmsg></msg>")
	if len(payload) > maxAppMessageBytes {
		t.Fatalf("测试载荷 %d 字节已超过大小闸门 %d，无法验证深度守卫", len(payload), maxAppMessageBytes)
	}
	details, _, handled := parseAppMessage(payload)
	if !handled {
		t.Fatal("appmsg 载荷未被识别")
	}
	if details["parse_error"] != "too_deep" {
		t.Fatalf("超深 appmsg 未被标注为 too_deep：parse_error=%v", details["parse_error"])
	}
}

// 深度守卫不能误伤正常层数的报文。
func TestXMLDepthGuardKeepsRealisticNestingParsable(t *testing.T) {
	node, status := parseXML(nestedXMLPayload(maxXMLDepth-2, "", ""), false)
	if status != "" || node == nil {
		t.Fatalf("上限之内的嵌套被错误拒绝：status=%q node!=nil=%v", status, node != nil)
	}

	details, _, handled := parseAppMessage(
		`<msg><appmsg><title>标题</title><des>说明</des><type>5</type><url>https://example.com</url></appmsg></msg>`)
	if !handled || details["parse_error"] != nil {
		t.Fatalf("常规 appmsg 被深度守卫误伤：handled=%v parse_error=%v", handled, details["parse_error"])
	}
	if details["title"] != "标题" || details["appmsg_type"] != int64(5) {
		t.Fatalf("常规 appmsg 解析结果异常：%v", details)
	}
}
