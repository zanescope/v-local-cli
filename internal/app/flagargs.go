package app

import "strings"

// flagArgument 按 Go flag 包的规则解析单个命令行参数。flag 包把 `-name` 和
// `--name` 视为同一个标志，`-name=value` 与 `--name=value` 同理，因此任何按参数
// 文本判断标志的地方都必须同时覆盖这四种形态：只匹配 `--name` 会把完全合法的
// `-name` 当成普通参数放行。
//
// 返回标志名、`=` 形态携带的值，以及该参数是否为 `=` 形态。位置参数、`-`、`--`
// 和 flag 包会判为语法错误的写法都返回空名字。
func flagArgument(argument string) (string, string, bool) {
	if len(argument) < 2 || argument[0] != '-' {
		return "", "", false
	}
	name := argument[1:]
	if name[0] == '-' {
		if len(name) == 1 {
			return "", "", false
		}
		name = name[1:]
	}
	if name[0] == '-' || name[0] == '=' {
		return "", "", false
	}
	if key, value, found := strings.Cut(name, "="); found {
		return key, value, true
	}
	return name, "", false
}

// namedFlagArgument 判断参数是否为给定标志之一，覆盖 flagArgument 支持的全部形态。
func namedFlagArgument(argument string, names ...string) bool {
	name, _, _ := flagArgument(argument)
	if name == "" {
		return false
	}
	for _, candidate := range names {
		if name == candidate {
			return true
		}
	}
	return false
}

// accountSelectorFromArgs 在命令执行前从参数里取出 --account 的取值。它只按 flag
// 语法做文本推断，并不知道各标志的实际元数，因此调用方必须把推断不出或推断错误当作
// 正常情况处理，不能把它当成账号解析的权威来源。
func accountSelectorFromArgs(args []string) string {
	for index, argument := range args {
		if argument == "--" {
			return ""
		}
		name, value, hasValue := flagArgument(argument)
		if name != "account" {
			continue
		}
		if hasValue {
			return strings.TrimSpace(value)
		}
		if index+1 < len(args) {
			return strings.TrimSpace(args[index+1])
		}
		return ""
	}
	return ""
}
