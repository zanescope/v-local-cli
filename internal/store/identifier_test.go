package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestQuoteIdentifierEscapesClosingBracket(t *testing.T) {
	for _, testCase := range []struct{ name, want string }{
		{"Msg_abc", "[Msg_abc]"},
		{"local_id", "[local_id]"},
		{"evil]--", "[evil]]--]"},
		{"a]b]c", "[a]]b]]c]"},
		{"", "[]"},
	} {
		if got := quoteIdentifier(testCase.name); got != testCase.want {
			t.Errorf("quoteIdentifier(%q) = %q，期望 %q", testCase.name, got, testCase.want)
		}
	}
}

// 回归：方括号转义原先只做了 3 处，其余十几处直接拼接。标识符来自数据库自身内容，
// 必须全部经过 quoteIdentifier，否则个别站点会重新漏掉转义。
func TestNoRawBracketIdentifierConcatenationRemains(t *testing.T) {
	// 匹配 SQL 语句里裸拼接标识符的写法，例如 "... FROM [" + table + "] ..."。
	raw := regexp.MustCompile(`\["\s*\+|\+\s*"\]`)
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// 只检查真正发起查询的文件。像 message_content.go 这类纯解析文件里的
		// "[" + label + "]" 是展示文案（例如 [图片]），与 SQL 标识符无关。
		if !strings.Contains(string(payload), `"database/sql"`) {
			continue
		}
		scanned++
		for index, line := range strings.Split(string(payload), "\n") {
			if raw.MatchString(line) {
				t.Errorf("%s:%d 仍在裸拼接方括号标识符，请改用 quoteIdentifier：%s",
					path, index+1, strings.TrimSpace(line))
			}
		}
	}
	if scanned < 5 {
		t.Fatalf("只扫描到 %d 个发起查询的文件，契约测试可能已失效", scanned)
	}
	t.Logf("已扫描 %d 个发起查询的文件", scanned)
}
