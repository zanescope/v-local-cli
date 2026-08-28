package app

import "testing"

func TestFlagArgumentTreatsSingleAndDoubleDashAlike(t *testing.T) {
	for _, argument := range []string{"--account=alice", "-account=alice"} {
		name, value, hasValue := flagArgument(argument)
		if name != "account" || value != "alice" || !hasValue {
			t.Fatalf("flagArgument(%q)=(%q,%q,%v)", argument, name, value, hasValue)
		}
	}
	for _, argument := range []string{"--account", "-account"} {
		name, value, hasValue := flagArgument(argument)
		if name != "account" || value != "" || hasValue {
			t.Fatalf("flagArgument(%q)=(%q,%q,%v)", argument, name, value, hasValue)
		}
	}
	// 位置参数与 flag 包判为语法错误的写法都不能被当成标志。
	for _, argument := range []string{"alice", "", "-", "--", "---account", "-=alice", "="} {
		if name, _, _ := flagArgument(argument); name != "" {
			t.Fatalf("flagArgument(%q) 把非标志识别成了 %q", argument, name)
		}
	}
}

func TestNamedFlagArgumentCoversEveryAcceptedForm(t *testing.T) {
	for _, argument := range []string{"--fresh", "-fresh", "--fresh=1", "-fresh=1"} {
		if !namedFlagArgument(argument, "fresh", "resolve-media") {
			t.Fatalf("namedFlagArgument 漏掉了合法形态 %q", argument)
		}
	}
	for _, argument := range []string{"fresh", "--freshen", "-freshen", "--fresh-media"} {
		if namedFlagArgument(argument, "fresh", "resolve-media") {
			t.Fatalf("namedFlagArgument 误判了 %q", argument)
		}
	}
}

// Go 的 flag 包对 `-account=x` 与 `--account=x` 一视同仁。只识别双短横线会让
// `-account=x` 被当成位置参数，缓存键因此绑定到默认账号的 generation，目标账号
// 刷新后旧结果仍会命中。
func TestAccountSelectorFromArgsAcceptsSingleDashForms(t *testing.T) {
	for _, args := range [][]string{
		{"--account=alice"}, {"-account=alice"},
		{"--account", "alice"}, {"-account", "alice"},
		{"--chat", "wxid_x", "-account=alice"},
	} {
		if got := accountSelectorFromArgs(args); got != "alice" {
			t.Fatalf("accountSelectorFromArgs(%v)=%q, want alice", args, got)
		}
	}
	for _, args := range [][]string{
		{}, {"--account"}, {"wxid_x"},
		// `--` 之后是位置参数，flag 包不会把它们解析成标志。
		{"--", "--account=alice"},
	} {
		if got := accountSelectorFromArgs(args); got != "" {
			t.Fatalf("accountSelectorFromArgs(%v)=%q, want empty", args, got)
		}
	}
}

func TestDaemonResponseMatchesBindingRequiresEchoedGeneration(t *testing.T) {
	binding := daemonCacheBinding{key: "k", generationID: "gen-1", snapshotManifestSHA256: "manifest-1"}
	matching := `{"schema_version":1,"command_status":"succeeded","data":{},` +
		`"meta":{"generation_id":"gen-1","snapshot_manifest_sha256":"manifest-1"}}`
	if !daemonResponseMatchesBinding(matching, binding) {
		t.Fatal("回显证据与绑定一致的响应被判为不匹配")
	}
	for name, stdout := range map[string]string{
		"generation 不同":   `{"meta":{"generation_id":"gen-2","snapshot_manifest_sha256":"manifest-1"}}`,
		"manifest 不同":     `{"meta":{"generation_id":"gen-1","snapshot_manifest_sha256":"manifest-2"}}`,
		"没有回显 generation": `{"schema_version":1,"command_status":"succeeded","data":{}}`,
		"meta 为空":         `{"meta":{}}`,
		"不是 JSON":         "not json",
	} {
		if daemonResponseMatchesBinding(stdout, binding) {
			t.Fatalf("%s 的响应仍被写入缓存", name)
		}
	}
}
