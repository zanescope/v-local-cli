package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declaresOutputFlag 判断函数体内是否声明了 --output 命令行标志。
func declaresOutputFlag(function *ast.FuncDecl) bool {
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "String" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if ok && literal.Kind == token.STRING && literal.Value == `"output"` {
			found = true
			return false
		}
		return true
	})
	return found
}

func callsFunction(function *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(function, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if identifier, ok := call.Fun.(*ast.Ident); ok && identifier.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

// 回归：export-moment-media 曾内联一份简化校验，漏掉了符号链接／重解析点与普通文件判定。
// 任何提供 --output 的命令都必须走同一个 prepareOutputTarget，否则各命令的安全边界会漂移。
func TestEveryOutputFlagCommandUsesPrepareOutputTarget(t *testing.T) {
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
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, payload, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !declaresOutputFlag(function) {
				continue
			}
			checked++
			if !callsFunction(function, "prepareOutputTarget") {
				t.Errorf("%s 中的 %s 声明了 --output 但没有调用 prepareOutputTarget，"+
					"符号链接与普通文件校验会被绕过", path, function.Name.Name)
			}
		}
	}
	if checked < 3 {
		t.Fatalf("只找到 %d 个提供 --output 的命令，契约测试可能已失效", checked)
	}
	t.Logf("已校验 %d 个提供 --output 的命令", checked)
}
