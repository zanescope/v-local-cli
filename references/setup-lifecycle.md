# 初始化、刷新与数据生命周期

## 首次初始化

### 1. 预检账号与环境

每次首次初始化、Agent 重启后的恢复或重新 setup 都依次运行：

```text
v-local-cli status
v-local-cli doctor
```

根据结果处理：

- `data.no_accounts=true`：提醒「请重新登录微信/打开新消息后重试」，然后停止。
- `data.external_key_workflows` 非空：只把它当作无权限跨重启阶段提示；`prior_requested_action` 只记录重启前请求过什么，不表示还要执行。检查 `revalidation_stage`、`session_resumable=false`、`authorization_carried_forward=false` 和 `machine_revalidation_required=true`，不得恢复旧 daemon session、重复历史动作或复用旧授权。
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
- `data.key_provider.executable_present` 只表示可执行普通文件已解析，不表示协议兼容、签名可信或候选一定正确；同时查看 `integrity`。
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

Windows Credential Manager 的单个 `CredentialBlob` 上限是 `5*512` bytes。CLI 的新写入固定使用 schema v1 manifest、2000-byte 分片和 a/b 双槽：先完整写入 inactive slot，再原子切换 manifest；旧的单条 `gzip+base64` 记录只保留读取兼容。应用最多接受 64 个分片，超过预算时不截断、不只保存部分凭据，而是让 keychain 写入失败并按上面的 `snapshot-only` 降级语义报告。manifest 提交后的旧分片清理若失败，新凭据仍按已提交处理，setup 返回 warning，后续 setup 或 `forget` 会再次执行有界清理。上限来源见 [Microsoft CREDENTIAL 结构说明](https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentiala)。

### 4. 判断 setup 是否可用

不要只看 `data.status`，同时检查：

- `data.database.summary` 中已发现、已解密、跳过和失败的数量；
- `data.media.status`、`aes_verified`、`xor_verified`；
- 完整密钥流程必须看到 `data.database_credential_status=persisted|not_required_plaintext_only` 和 `data.image_keys_persisted=true`；`database_keys_persisted` 只表示是否保存了逐库 key，不能表示 plaintext-only 或结构化根凭据失败；
- `snapshot-only` 是明确的不保存密钥模式，不应把其中的 `*_keys_persisted=false` 解释为媒体验证失败；
- `data.storage` 是否因系统凭据库失败降级为 `snapshot-only`；
- `data.warnings` 是否影响当前任务。

处理规则：

- `ready`：数据库快照可读，所需检查均已通过。
- 完整媒体 setup 的成功判据是同时满足 `status=ready`、`media.status=verified`、`database_credential_status=persisted|not_required_plaintext_only` 和 `image_keys_persisted=true`。plaintext-only Catalog 合法地没有数据库凭据。
- database-only 的成功判据是 `status=ready`、`database_only=true`、`database_credential_status=persisted|not_required_plaintext_only` 和 `image_keys_persisted=false`；它不具备图片能力。
- `status=security_restoration_required` 表示验真 credential/generation 可以已经发布，但 macOS SIP 工作流尚未完成；先恢复 SIP 并重启，再用不带旧确认参数的新 setup 取得 `sip_enabled_verified`。该值只证明 SIP 已开启，不代表整机总体安全。
- `partial`：只在当前任务不依赖失败部分时继续，并向用户说明具体缺口。文本任务要求至少有可读数据库快照；媒体任务要求图片状态为 `verified`。
- setup 失败：不要直接读取原数据库，不要尝试猜测候选，按错误类型恢复。
- 只有源数据库确已删除、用户理解历史范围会缩小并明确同意时，才使用 `--allow-coverage-regression`；不要用它绕过临时读取失败。

### 授权说明

| 来源 | 影响 | 授权要求 |
|---|---|---|
| `setup --allow-key-access` | 只读访问本机微信进程 | 必须先单独说明该影响，取得用户对本次操作的明确同意。不要把一般的「看看记录」推定为进程访问授权。 |
| `setup --keys FILE` | 读取用户指定的敏感候选文件，不访问微信进程 | 只使用用户明确提供或授权的文件，不展示其内容。 |

### 跨重启 Shadow/SIP 交接

`approve_shadow_mode`、`disable_sip`、`reenable_sip` 会结束旧 acquisition session。CLI 的私有 checkpoint 只提示阶段，不携带授权：`status`/`setup --dry-run` 会显示 `prior_requested_action`、`revalidation_stage`、`session_resumable=false`、`authorization_carried_forward=false` 和 `machine_revalidation_required=true`。其中 `prior_requested_action` 不能当成新的操作指令。完成外部动作或重启后，重新取得本次 `--allow-key-access` 授权并以 checkpoint 原 scopes 启动新 setup；不同 scope 会 fail closed，绝不覆盖旧 checkpoint，也不复用旧 `--confirm-key-action`。恢复阶段由一次独立、只读、无 credential 且不依赖账号目录仍存在的 `revalidate_security_posture` 请求收口；若用户未执行 `disable_sip`，后来一次普通 setup 在 SIP 开启状态下完整成功也会清理失效 checkpoint。若普通 acquisition 在产生姿态诊断前失败，CLI 仅补做这次只读姿态检查，并且只有确认 SIP 已关闭时才转入恢复阶段。checkpoint 损坏或过期时按 `external_workflow_state_invalid` 停止，不根据用户口述跳过验证。

损坏记录通常用 `setup --cancel-acquisition --account <account>` 精确清理。若账号已不存在或记录无法归属，只有用户明确同意丢弃全部跨重启阶段提示后才运行 `setup --cancel-all-external-workflows`；该入口只删除 `external-*.checkpoint.json[.old]`，保留 daemon resume、快照、索引与系统凭据。

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
