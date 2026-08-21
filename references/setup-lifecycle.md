# 初始化、刷新与数据生命周期

## 首次初始化

### 1. 预检账号与环境

依次运行：

```text
v-local-cli status
v-local-cli doctor
```

根据结果处理：

- `data.no_accounts=true`：提醒「请重新登录微信/打开新消息后重试」，然后停止。
- 只有一个账号：后续仍可显式传 `--account <name>`，使证据范围清晰。
- 多个账号：运行 `v-local-cli accounts`，向用户展示必要的可识别信息并让其选择；不要泄露完整本机路径。
- 自动候选获取不可用：运行 `v-local-cli provider status` 确认原因。不要静默安装 Provider；让用户安装独立 Provider，或改用其合法持有的候选文件。

### 2. 运行无副作用预演

```text
v-local-cli setup --dry-run --account <account>
```

检查：

- `data.status` 应为 `planned`。
- `data.process_access_performed` 和 `data.secrets_persisted` 应为 `false`。
- `data.key_provider.available` 只表示可执行文件已找到，不表示候选一定正确。
- `setup --dry-run` 不启动 Provider，也不生成快照。
- `data.prevents_coverage_regression=true` 表示重新 setup 也不会静默缩小已有数据库范围。

### 3. 选择候选来源和存储策略

用户明确授权本次进程访问后运行：

```text
v-local-cli setup --account <account> --allow-key-access --storage keychain
```

用户已有合法候选文件时运行：

```text
v-local-cli setup --account <account> --keys <keys.json> --storage keychain
```

两种来源不能同时使用。根据任务选择存储方式：

- 使用 `keychain`：保存已通过格式与真实样本检查的最小候选集合，支持后续刷新和图片导出。
- 使用 `snapshot-only`：只保留已发布的明文只读快照，不保存候选；以后刷新或导出图片需要重新 setup。
- 默认 setup 就是一次性取得完整数据库和图片能力的严格流程；它会在保存前验证图片 DAT，失败时不发布部分完成的 setup。
- 只处理文本时显式增加 `--database-only`；该模式向 Provider 只请求数据库 scope，并明确不能保证图片密钥已保存。

### 4. 判断 setup 是否可用

不要只看 `data.status`，同时检查：

- `data.database.summary` 中已发现、已解密、跳过和失败的数量；
- `data.media.status`、`aes_verified`、`xor_verified`；
- 完整密钥流程还必须看到 `data.database_keys_persisted=true` 和 `data.image_keys_persisted=true`；
- `snapshot-only` 是明确的不保存密钥模式，不应把其中的 `*_keys_persisted=false` 解释为媒体验证失败；
- `data.storage` 是否因系统凭据库失败降级为 `snapshot-only`；
- `data.warnings` 是否影响当前任务。

处理规则：

- `ready`：数据库快照可读，所需检查均已通过。
- 完整媒体 setup 的成功判据是同时满足 `status=ready`、`media.status=verified`、`database_keys_persisted=true` 和 `image_keys_persisted=true`。任一字段不满足都不要宣称已取得完整密钥。
- database-only 例外的成功判据是 `status=ready`、`database_only=true`、`database_keys_persisted=true` 和 `image_keys_persisted=false`；它不具备图片能力。
- `partial`：只在当前任务不依赖失败部分时继续，并向用户说明具体缺口。文本任务要求至少有可读数据库快照；媒体任务要求图片状态为 `verified`。
- setup 失败：不要直接读取原数据库，不要尝试猜测候选，按错误类型恢复。
- 只有源数据库确已删除、用户理解历史范围会缩小并明确同意时，才使用 `--allow-coverage-regression`；不要用它绕过临时读取失败。

### 授权说明

| 来源 | 影响 | 授权要求 |
|---|---|---|
| `setup --allow-key-access` | 只读访问本机微信进程 | 必须先单独说明该影响，取得用户对本次操作的明确同意。不要把一般的「看看记录」推定为进程访问授权。 |
| `setup --keys FILE` | 读取用户指定的敏感候选文件，不访问微信进程 | 只使用用户明确提供或授权的文件，不展示其内容。 |

## 刷新快照

setup 发布的是某一时刻的只读快照，不会随微信实时更新。遇到「刚收到」「最新」「今天新消息」或当前结果明显陈旧时：

1. 告知用户需要刷新快照。
2. 已初始化状态的 `storage=keychain` 时，优先把 `--fresh` 直接加到即将运行的快照查询；需要单独检查刷新结果时运行：

```text
v-local-cli refresh --account <account> --require-media
```

3. 上面的命令用于继续验证图片/DAT 能力；只处理文本时可省略 `--require-media`。刷新不会再次访问 Provider，只复用 setup 保存的密钥。
4. 检查 `credential_source=saved_keychain`、`process_access_performed=false`、`secrets_persisted=false`，以及数据库的发现、解密、跳过、失败和 `publication_coverage`。成功刷新要求 `missing_previous=0`。
5. `ready` 后再查询。`partial` 只在原有数据库仍齐全、当前任务不依赖新增但未解密的分片或媒体时继续，并明确覆盖缺口；旧凭据无法覆盖新数据库分片时，重新 setup 才能取得新候选。
6. 返回 `refresh_credentials_unavailable` 时，说明当前身份没有可复用凭据。只有此时才回到 `setup --dry-run`，并在需要 Provider 时重新取得本次 `--allow-key-access` 的明确授权。

`refresh` 精确绑定初始化时的账号目录，不启动 Provider、不访问微信进程、不联网，也不修改系统凭据。同一账号的 `setup` 和 `refresh` 由操作系统文件锁串行化。候选快照缺少旧快照中的数据库文件时返回 `snapshot_coverage_regression`，当前快照保持不变；该保护只证明数据库文件集合没有减少，不证明每个库的行级历史单调增加。不要把旧快照结果表述为刷新后的最新状态。

`--fresh` 可放在 `contacts`、聊天/朋友圈/公众号、语音/OCR 快照、统计及导出等 `schema` 标记 `fresh_snapshot=true` 的命令中；CLI 会先刷新再查询，并回显 `fresh_requested=true`、`fresh_completed=true`。非快照命令拒绝该参数。刷新凭据不可用时整个命令失败，不会把旧结果伪装成新结果。

每次成功 setup/refresh 都发布不可变 `generation_id` 和 manifest 摘要，状态文件最后才切换当前指针。失败时继续使用上一代；查询或导出结论保留返回的版本信息，不把不同版本的结果当成同一时刻证据。所有 JSON 响应都返回 `meta.snapshot_created_at` 和 `meta.snapshot_age_seconds`；快照命令返回真实值，非快照命令为 `null`。引用「最新」时同时保留这两个字段。

## 本地数据生命周期

清理空间时：

```text
v-local-cli gc --account <account> --dry-run
v-local-cli gc --account <account>
```

`gc` 只处理 v-local-cli 私有版本目录，保留当前版本和一个回滚版本。不要手工删除锁文件、状态文件或某个数据库分片。

彻底删除所选账号数据时：

```text
v-local-cli forget --account <account> --dry-run
v-local-cli forget --account <account> --yes
```

第二条命令只有在用户看到第一条返回的范围并明确确认后才能执行。删除包括明文快照、状态、暂存目录和系统凭据库中的已保存密钥，结果不可由 CLI 恢复。
