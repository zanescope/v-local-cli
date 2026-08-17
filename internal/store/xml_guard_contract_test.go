package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// store 包里有两棵 XML 树：xmlNode 处理朋友圈／公众号 XML，messageXMLNode 处理
// appmsg（载荷经过 HTML 转义）。两者的访问器语义不同，刻意不合并；但它们各自的
// UnmarshalXML 都用了「自定义解码器内部再调 DecodeElement」这种会绕过 encoding/xml
// 内置深度守卫的写法，因此每一个都必须自己检查 maxXMLDepth。
//
// 这个契约测试保证以后新增或改写 UnmarshalXML 时不会漏掉守卫——那会让栈溢出崩溃回归。
func TestEveryUnmarshalXMLImplementationGuardsDepth(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, payload, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "UnmarshalXML" || function.Recv == nil {
				continue
			}
			checked++
			guarded := false
			ast.Inspect(function, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if ok && (identifier.Name == "maxXMLDepth" || identifier.Name == "errXMLTooDeep") {
					guarded = true
					return false
				}
				return true
			})
			if !guarded {
				t.Errorf("%s:%d 的 UnmarshalXML 没有检查 maxXMLDepth；自定义解码器内调用 "+
					"DecodeElement 会把 encoding/xml 的内置深度守卫重置为 0，深层嵌套将耗尽栈并触发 "+
					"无法 recover 的 fatal error", path, fileSet.Position(function.Pos()).Line)
			}
		}
	}
	if checked < 2 {
		t.Fatalf("只找到 %d 个 UnmarshalXML 实现，契约测试可能已失效", checked)
	}
	t.Logf("已校验 %d 个 UnmarshalXML 实现", checked)
}
