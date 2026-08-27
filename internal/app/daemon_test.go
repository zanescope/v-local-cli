package app

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func waitForDaemonInfo(t *testing.T) daemonInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := loadDaemonInfo(); err == nil {
			return info
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon endpoint 未就绪")
	return daemonInfo{}
}

func TestDaemonUsesAuthenticatedLoopbackAndStopsCleanly(t *testing.T) {
	t.Setenv("V_LOCAL_CLI_HOME", t.TempDir())
	done := make(chan error, 1)
	go func() { done <- serveDaemon() }()
	info := waitForDaemonInfo(t)
	if err := serveDaemon(); err == nil || !strings.Contains(err.Error(), "已经运行") {
		t.Fatalf("daemon 单实例锁未生效：%v", err)
	}
	if !strings.HasPrefix(info.Address, "127.0.0.1:") || len(info.Token) != 64 {
		t.Fatalf("daemon 端点不安全：%+v", info)
	}
	path, _ := daemonInfoPath()
	if stat, err := os.Stat(path); err != nil || stat.IsDir() {
		t.Fatalf("daemon endpoint 文件异常：stat=%v err=%v", stat, err)
	}
	ping, err := daemonExchange(info, "__ping__", nil)
	if err != nil || ping.Status != "ready" {
		t.Fatalf("daemon ping 异常：response=%+v err=%v", ping, err)
	}
	denied, err := daemonExchange(info, "refresh", nil)
	if err != nil || denied.ExitCode == 0 || !strings.Contains(denied.Stderr, "白名单") {
		t.Fatalf("daemon 未拒绝 refresh：response=%+v err=%v", denied, err)
	}
	denied, err = daemonExchange(info, "moments", []string{"--resolve-media", "wxid_test"})
	if err != nil || denied.ExitCode == 0 || !strings.Contains(denied.Stderr, "拒绝") {
		t.Fatalf("daemon 未拒绝可变本地媒体解析：response=%+v err=%v", denied, err)
	}
	if _, err := daemonExchange(info, "__stop__", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon 未停止")
	}
}

func TestOutputModesKeepJSONDefaultAndRenderYAMLTable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"schema", "sessions"}, &stdout, &stderr); code != 0 {
		t.Fatalf("默认 JSON schema 失败：code=%d stderr=%s", code, stderr.String())
	}
	var defaultEnvelope envelope
	if err := json.Unmarshal(stdout.Bytes(), &defaultEnvelope); err != nil || defaultEnvelope.CommandStatus != "succeeded" {
		t.Fatalf("默认输出不再是 JSON envelope：output=%s err=%v", stdout.String(), err)
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil || raw["schema_version"] != float64(2) || raw["command_status"] != "succeeded" {
		t.Fatalf("response schema v2 envelope 无效：output=%s err=%v", stdout.String(), err)
	}
	if _, found := raw["ok"]; found {
		t.Fatalf("response schema v2 仍暴露有歧义的顶层 ok：%v", raw)
	}
	if _, found := defaultEnvelope.Meta["output_format"]; found {
		t.Fatalf("默认 JSON 不应增加 output_format：%v", defaultEnvelope.Meta)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"--output", "yaml", "schema", "sessions"}, &stdout, &stderr); code != 0 {
		t.Fatalf("yaml schema 失败：code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"schema_version": 2`) || !strings.Contains(stdout.String(), `"output_format": "yaml"`) {
		t.Fatalf("YAML 输出异常：%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"--output=table", "schema", "sessions"}, &stdout, &stderr); code != 0 {
		t.Fatalf("table schema 失败：code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "FIELD") || !strings.Contains(stdout.String(), "command") {
		t.Fatalf("table 输出异常：%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"--output", "table", "members"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "TYPE") || !strings.Contains(stderr.String(), "invalid_arguments") {
		t.Fatalf("table 错误输出异常：code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Main([]string{"unknown-command"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown command unexpectedly succeeded")
	}
	raw = map[string]any{}
	if err := json.Unmarshal(stderr.Bytes(), &raw); err != nil || raw["command_status"] != "failed" {
		t.Fatalf("failure envelope 缺少明确 command_status：output=%s err=%v", stderr.String(), err)
	}
	if _, found := raw["ok"]; found {
		t.Fatalf("failure envelope 仍暴露有歧义的顶层 ok：%v", raw)
	}
}

func TestFreshNewMessagesOnlyAllowsPoll(t *testing.T) {
	for _, args := range [][]string{
		{"new-messages", "--fresh", "--status"},
		{"new-messages", "--fresh", "--ack", "batch-1"},
		{"new-messages", "--fresh", "--delete", "--yes"},
	} {
		if _, _, err := prepareFreshQuery(args); err == nil || !strings.Contains(err.Error(), "参数无效") {
			t.Fatalf("应在刷新前拒绝控制操作：args=%v err=%v", args, err)
		}
	}
}
