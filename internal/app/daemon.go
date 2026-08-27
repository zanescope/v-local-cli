package app

import (
	"bufio"
	"bytes"
	"container/list"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zanescope/v-local-cli/internal/messageindex"
	"github.com/zanescope/v-local-cli/internal/state"
)

const (
	daemonProtocolVersion  = 1
	maxDaemonRequestBytes  = 1024 * 1024
	maxDaemonResponseBytes = 16 * 1024 * 1024
	maxDaemonCacheEntries  = 64
	maxDaemonCacheBytes    = 64 * 1024 * 1024
	maxDaemonConnections   = 32
)

type daemonInfo struct {
	SchemaVersion int    `json:"schema_version"`
	Address       string `json:"address"`
	Token         string `json:"token"`
	PID           int    `json:"pid"`
	Version       string `json:"version"`
	StartedAt     string `json:"started_at"`
}

type daemonRequest struct {
	SchemaVersion int      `json:"schema_version"`
	Token         string   `json:"token"`
	Command       string   `json:"command"`
	Args          []string `json:"args,omitempty"`
}

type daemonResponse struct {
	SchemaVersion int            `json:"schema_version"`
	ExitCode      int            `json:"exit_code"`
	Stdout        string         `json:"stdout,omitempty"`
	Stderr        string         `json:"stderr,omitempty"`
	Status        string         `json:"status,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type daemonCacheEntry struct {
	key     string
	stdout  string
	bytes   int
	element *list.Element
}

type daemonCache struct {
	mutex sync.Mutex
	items map[string]*daemonCacheEntry
	order *list.List
	bytes int
}

func newDaemonCache() *daemonCache {
	return &daemonCache{items: map[string]*daemonCacheEntry{}, order: list.New()}
}

func (cache *daemonCache) get(key string) (string, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry, found := cache.items[key]
	if !found {
		return "", false
	}
	cache.order.MoveToFront(entry.element)
	return entry.stdout, true
}

func (cache *daemonCache) put(key, stdout string) {
	size := len(stdout)
	if size == 0 || size > maxDaemonResponseBytes {
		return
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if existing := cache.items[key]; existing != nil {
		cache.bytes -= existing.bytes
		existing.stdout, existing.bytes = stdout, size
		cache.bytes += size
		cache.order.MoveToFront(existing.element)
		return
	}
	entry := &daemonCacheEntry{key: key, stdout: stdout, bytes: size}
	entry.element = cache.order.PushFront(entry)
	cache.items[key] = entry
	cache.bytes += size
	for len(cache.items) > maxDaemonCacheEntries || cache.bytes > maxDaemonCacheBytes {
		oldestElement := cache.order.Back()
		if oldestElement == nil {
			break
		}
		oldest := oldestElement.Value.(*daemonCacheEntry)
		delete(cache.items, oldest.key)
		cache.bytes -= oldest.bytes
		cache.order.Remove(oldestElement)
	}
}

func daemonInfoPath() (string, error) {
	root, err := state.DaemonRoot()
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "endpoint.json")
	if err := state.ValidatePrivateTarget(path, false); err != nil {
		return "", err
	}
	return path, nil
}

func randomDaemonToken() (string, error) {
	payload := make([]byte, 32)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func saveDaemonInfo(info daemonInfo) error {
	path, err := daemonInfoPath()
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".endpoint-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	remove = false
	return nil
}

func loadDaemonInfo() (daemonInfo, error) {
	path, err := daemonInfoPath()
	if err != nil {
		return daemonInfo{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return daemonInfo{}, err
	}
	var info daemonInfo
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		return daemonInfo{}, err
	}
	host, _, splitErr := net.SplitHostPort(info.Address)
	ip := net.ParseIP(host)
	if info.SchemaVersion != daemonProtocolVersion || splitErr != nil || ip == nil || !ip.IsLoopback() ||
		len(info.Token) != 64 || info.PID <= 0 {
		return daemonInfo{}, errors.New("daemon endpoint 信息无效")
	}
	if decoded, err := hex.DecodeString(info.Token); err != nil || len(decoded) != 32 {
		return daemonInfo{}, errors.New("daemon endpoint 令牌无效")
	}
	return info, nil
}

func allowedDaemonCommand(command string) bool {
	return map[string]bool{
		"contacts": true, "resolve-contact": true, "sessions": true, "unread": true,
		"members": true, "favorites": true, "history": true, "search": true, "stats": true,
		"moments-contacts": true, "moments": true, "moments-search": true,
		"official-accounts": true, "official-history": true, "official-search": true,
	}[command]
}

// daemonCacheBinding 记录缓存键以及它所绑定的 generation 证据。键必须在执行前
// 构造，因此账号只能按 flag 语法从参数里推断；推断结果与命令实际使用的账号不一致
// 时，键会绑定到另一个账号的 generation，目标账号刷新后旧结果仍会命中。写入缓存前
// 用响应回显的 generation 复核这份绑定，可以让这种情况退化为不缓存而不是给出陈旧
// 证据。
type daemonCacheBinding struct {
	key                    string
	generationID           string
	snapshotManifestSHA256 string
}

func daemonCacheKey(command string, args []string) daemonCacheBinding {
	value, err := resolveInitializedAccount(accountSelectorFromArgs(args))
	if err != nil || value.GenerationID == "" || value.SnapshotManifestSHA256 == "" {
		return daemonCacheBinding{}
	}
	payload, _ := json.Marshal(args)
	indexIdentity := "index-unavailable"
	if indexStatus, indexErr := messageindex.Inspect(value); indexErr == nil {
		indexIdentity = indexStatus.Reason
		if indexStatus.Valid && indexStatus.Manifest != nil {
			indexIdentity = strings.Join([]string{
				indexStatus.Manifest.CreatedAt, fmt.Sprint(indexStatus.Manifest.SchemaVersion),
				fmt.Sprint(indexStatus.Manifest.ParserVersion), fmt.Sprint(indexStatus.Manifest.DocumentCount),
			}, ":")
		}
	}
	return daemonCacheBinding{
		key: strings.Join([]string{
			value.AccountID, value.GenerationID, value.SnapshotManifestSHA256,
			indexIdentity, time.Now().Format("2006-01-02"), command, string(payload), Version,
		}, "\x00"),
		generationID:           value.GenerationID,
		snapshotManifestSHA256: value.SnapshotManifestSHA256,
	}
}

// daemonResponseMatchesBinding 确认响应回显的证据版本与缓存键绑定的一致。命令没有
// 回显 generation 时同样判为不匹配，宁可不缓存。
func daemonResponseMatchesBinding(stdout string, binding daemonCacheBinding) bool {
	var value struct {
		Meta struct {
			GenerationID           string `json:"generation_id"`
			SnapshotManifestSHA256 string `json:"snapshot_manifest_sha256"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		return false
	}
	return value.Meta.GenerationID != "" && value.Meta.GenerationID == binding.generationID &&
		value.Meta.SnapshotManifestSHA256 != "" && value.Meta.SnapshotManifestSHA256 == binding.snapshotManifestSHA256
}

func executeDaemonQuery(cache *daemonCache, request daemonRequest) daemonResponse {
	if !allowedDaemonCommand(request.Command) {
		return daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 2, Stderr: "daemon 只允许 immutable generation 白名单查询\n"}
	}
	// 这里刻意不在 `--` 处停止：位置参数里出现 refresh 或可变媒体解析的写法一律
	// 拒绝，宁可误拒也不放行。
	for _, argument := range request.Args {
		if namedFlagArgument(argument, "fresh", "resolve-media") || len(argument) > 64*1024 {
			return daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 2, Stderr: "daemon 拒绝 refresh、超长参数或非只读行为\n"}
		}
	}
	binding := daemonCacheKey(request.Command, request.Args)
	if binding.key != "" {
		if stdout, found := cache.get(binding.key); found {
			return daemonResponse{
				SchemaVersion: daemonProtocolVersion, ExitCode: 0, Stdout: stdout,
				Meta: map[string]any{"cache": "hit"},
			}
		}
	}
	var stdout, stderr bytes.Buffer
	exitCode := mainWithPolicy(append([]string{request.Command}, request.Args...), &stdout, &stderr, true)
	response := daemonResponse{
		SchemaVersion: daemonProtocolVersion, ExitCode: exitCode,
		Stdout: stdout.String(), Stderr: stderr.String(), Meta: map[string]any{"cache": "miss"},
	}
	if exitCode == 0 && binding.key != "" && stdout.Len() <= maxDaemonResponseBytes &&
		daemonResponseMatchesBinding(stdout.String(), binding) {
		cache.put(binding.key, stdout.String())
	}
	return response
}

func writeDaemonResponse(connection net.Conn, response daemonResponse) {
	_ = connection.SetWriteDeadline(time.Now().Add(30 * time.Second))
	payload, err := json.Marshal(response)
	if err != nil || len(payload)+1 > maxDaemonResponseBytes {
		payload, _ = json.Marshal(daemonResponse{
			SchemaVersion: daemonProtocolVersion, ExitCode: 5,
			Stderr: "daemon 响应超过安全上限\n",
		})
	}
	payload = append(payload, '\n')
	_, _ = io.Copy(connection, bytes.NewReader(payload))
}

func serveDaemonConnection(connection net.Conn, info daemonInfo, cache *daemonCache, stop chan<- struct{}) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReaderSize(connection, maxDaemonRequestBytes+1)
	payload, err := reader.ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 2, Stderr: "daemon 请求过大或不完整\n"})
		return
	}
	if len(payload) == 0 || len(payload) > maxDaemonRequestBytes {
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 2, Stderr: "daemon 请求大小无效\n"})
		return
	}
	var request daemonRequest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.SchemaVersion != daemonProtocolVersion {
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 2, Stderr: "daemon 请求协议无效\n"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(info.Token)) != 1 {
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, ExitCode: 3, Stderr: "daemon 认证失败\n"})
		return
	}
	switch request.Command {
	case "__ping__":
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, Status: "ready", Meta: map[string]any{"pid": info.PID, "version": info.Version}})
	case "__stop__":
		writeDaemonResponse(connection, daemonResponse{SchemaVersion: daemonProtocolVersion, Status: "stopping"})
		select {
		case stop <- struct{}{}:
		default:
		}
	default:
		writeDaemonResponse(connection, executeDaemonQuery(cache, request))
	}
}

func serveDaemon() error {
	lock, err := state.AcquireAccountLock(state.AccountID("daemon-control"))
	if errors.Is(err, state.ErrAccountBusy) {
		return errors.New("查询 daemon 已经运行或正在启动")
	}
	if err != nil {
		return err
	}
	defer lock.Release()
	if existing, err := loadDaemonInfo(); err == nil {
		if response, pingErr := daemonExchange(existing, "__ping__", nil); pingErr == nil && response.Status == "ready" {
			return errors.New("查询 daemon 已经运行")
		}
		path, _ := daemonInfoPath()
		_ = os.Remove(path)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := randomDaemonToken()
	if err != nil {
		return err
	}
	info := daemonInfo{
		SchemaVersion: daemonProtocolVersion, Address: listener.Addr().String(), Token: token,
		PID: os.Getpid(), Version: Version, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveDaemonInfo(info); err != nil {
		return err
	}
	path, _ := daemonInfoPath()
	defer os.Remove(path)
	stop := make(chan struct{}, 1)
	cache := newDaemonCache()
	connections := make(chan struct{}, maxDaemonConnections)
	var active sync.WaitGroup
	for {
		if tcp, ok := listener.(*net.TCPListener); ok {
			_ = tcp.SetDeadline(time.Now().Add(time.Second))
		}
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if timeout, ok := acceptErr.(net.Error); ok && timeout.Timeout() {
				select {
				case <-stop:
					active.Wait()
					return nil
				default:
					continue
				}
			}
			return acceptErr
		}
		select {
		case connections <- struct{}{}:
			active.Add(1)
			go func() {
				defer func() {
					<-connections
					active.Done()
				}()
				serveDaemonConnection(connection, info, cache, stop)
			}()
		default:
			writeDaemonResponse(connection, daemonResponse{
				SchemaVersion: daemonProtocolVersion, ExitCode: 5, Stderr: "daemon 并发请求超过安全上限\n",
			})
			_ = connection.Close()
		}
	}
}

func daemonExchange(info daemonInfo, command string, args []string) (daemonResponse, error) {
	connection, err := net.DialTimeout("tcp4", info.Address, 2*time.Second)
	if err != nil {
		return daemonResponse{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(35 * time.Second))
	request := daemonRequest{SchemaVersion: daemonProtocolVersion, Token: info.Token, Command: command, Args: args}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return daemonResponse{}, err
	}
	var response daemonResponse
	decoder := json.NewDecoder(io.LimitReader(connection, maxDaemonResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return daemonResponse{}, err
	}
	if response.SchemaVersion != daemonProtocolVersion {
		return daemonResponse{}, errors.New("daemon 响应协议版本不匹配")
	}
	return response, nil
}

func runDaemonClient(args []string, stdout, stderr io.Writer, mode outputMode) int {
	if len(args) == 0 {
		writeErrorMode(stderr, invalidArguments("用法：v-local-cli [--output json|yaml|table] --daemon <只读查询命令> [参数]"), mode)
		return 2
	}
	info, err := loadDaemonInfo()
	if err != nil {
		writeErrorMode(stderr, &commandError{typeName: "daemon_unavailable", message: "查询 daemon 不可用", hint: "在独立终端运行 v-local-cli daemon serve。", code: 5}, mode)
		return 5
	}
	response, err := daemonExchange(info, args[0], args[1:])
	if err != nil {
		writeErrorMode(stderr, &commandError{typeName: "daemon_request_failed", message: "查询 daemon 请求失败", hint: err.Error(), code: 5}, mode)
		return 5
	}
	if response.Stdout != "" {
		if mode == outputJSON {
			_, _ = io.WriteString(stdout, response.Stdout)
		} else {
			var value envelope
			if json.Unmarshal([]byte(response.Stdout), &value) == nil {
				writeEnvelope(stdout, value, mode)
			} else {
				_, _ = io.WriteString(stdout, response.Stdout)
			}
		}
	}
	if response.Stderr != "" {
		if mode == outputJSON {
			_, _ = io.WriteString(stderr, response.Stderr)
		} else {
			var value envelope
			if json.Unmarshal([]byte(response.Stderr), &value) == nil {
				writeEnvelope(stderr, value, mode)
			} else {
				_, _ = io.WriteString(stderr, response.Stderr)
			}
		}
	}
	return response.ExitCode
}

func runDaemon(args []string) (any, error) {
	set := flag.NewFlagSet("daemon", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	showPaths := set.Bool("show-paths", false, "显示认证端点路径")
	if err := set.Parse(args); err != nil || len(set.Args()) != 1 {
		return nil, invalidArguments("用法：v-local-cli daemon [--show-paths] <serve|status|stop>")
	}
	action := set.Args()[0]
	switch action {
	case "serve":
		if err := serveDaemon(); err != nil {
			return nil, &commandError{typeName: "daemon_serve_failed", message: "查询 daemon 启动或运行失败", hint: privateStateError(err), code: 5}
		}
		return map[string]any{"status": "stopped"}, nil
	case "status", "stop":
		info, err := loadDaemonInfo()
		if err != nil {
			return nil, &commandError{typeName: "daemon_unavailable", message: "查询 daemon 不可用", hint: "在独立终端运行 v-local-cli daemon serve。", code: 5}
		}
		command := "__ping__"
		if action == "stop" {
			command = "__stop__"
		}
		response, err := daemonExchange(info, command, nil)
		if err != nil {
			return nil, &commandError{typeName: "daemon_request_failed", message: "查询 daemon 请求失败", hint: err.Error(), code: 5}
		}
		result := map[string]any{"status": response.Status, "pid": info.PID, "version": info.Version}
		if *showPaths {
			result["endpoint"], _ = daemonInfoPath()
		}
		return result, nil
	default:
		return nil, invalidArguments("daemon 操作只能为 serve、status 或 stop")
	}
}
