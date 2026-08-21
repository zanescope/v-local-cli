# v-local-cli

> 面向 Agent 的本地微信（WeChat）只读查询工具。

单个 Go 二进制即可完成账号发现、密钥验证、只读快照、会话/未读/群成员/收藏、generation 全文索引、原子增量消息、联系人/聊天/搜索、语音转写与 OCR 读取、朋友圈与公众号查询、图片解密与导出，并可启动只服务 immutable generation 的本机查询 daemon。

项目仅供处理**本人拥有或已获明确授权访问**的数据。CLI 只读，不操作微信界面、不发送消息，普通查询不联网。

## 能做什么

- **账号与密钥验证** — 发现本机微信账号，用 SQLCipher 数据库首页与真实 DAT 样本验证外部提供的候选密钥；候选由用户通过 `--keys` 导入，或由用户单独安装的外部组件提供。
- **只读快照** — 从稳定复制的数据库发布不可变明文快照，查询走 SQLite 只读模式，不碰正在写入的微信库。每次查询都会回显 `snapshot_age_seconds`；需要最新落盘数据时给该次查询加 `--fresh`，先用已保存的密钥刷新再查（仍不访问微信进程，也不联网）。
- **会话与联系人** — 会话/快照未读、群成员、收藏、安全的模糊联系人解析，以及聊天历史的紧凑结构化摘要。
- **generation 索引与增量消息** — 每个不可变快照有独立的结构化全文索引；consumer 以持久化 pending batch 和显式 ack 提供 at-least-once 增量读取，不是后台监听。
- **查询 daemon** — 同一二进制可启动前台单实例、只监听 loopback、随机令牌认证、仅执行 immutable generation 白名单查询的本机 daemon。
- **统计** — 私聊收发、活跃度、消息类型分布、群成员排行；不返回消息正文。
- **语音** — 优先读取微信已生成的转写；转写缺失且用户同意时，可以改用本地 ASR（whisper.cpp 或 `v-local-cli-asr/1` 适配器，如 [SenseVoice](https://github.com/zanescope/v-local-cli-sensevoice)）。
- **OCR** — 优先读取微信已有的 OCR 索引；Windows amd64 可以在逐次授权下调用微信自带 OCR 处理单张图片。
- **朋友圈** — 本地帖子、点赞/评论、媒体；可以逐次授权从受限 CDN 导出图片或视频。
- **公众号** — 账号发现、图文历史、标题/摘要搜索；文章正文可以逐次授权访问 `mp.weixin.qq.com`。
- **导出与解密** — 聊天导出为 JSON/JSONL，DAT 图片解密（v1/v2/v3）。

**平台**：Windows、macOS、Linux 均可构建。微信桌面数据的验证目标目前是 Windows amd64 与 macOS amd64（Intel），两者都有真机真实数据证据。

macOS 上的 Provider 会自动使用同包安装的 companion helper，必要时走管理员授权兼容路径；访问微信进程可能需要用户临时关闭 SIP。Intel 真机上的自动获取密钥、快照与查询已完整走通，`darwin/amd64` 标记为 `real_device_verified`。

Apple Silicon 与 Intel macOS 使用同一套授权流程，动态 Hook 会按实际微信进程选择 `arm64` 或 `x86_64` ABI：Apple Silicon 上的原生 arm64 微信，与通过 Rosetta 运行的 x86_64 微信，会走不同的寄存器路径。正因为寄存器路径不同，Intel 的验证结论不能外推到原生 arm64——`darwin/arm64` 在完成[真机验收](references/macos-acceptance.md)之前仍标记为 `build_only`，在该架构上导入候选文件是保留的可靠路径。微信原生 OCR 与微信已有的语音/OCR 文字索引在两种 macOS 架构上都仍不可用，只有 `windows/amd64` 有布局证据。

## 安装

当前为纯 Go 的 `0.1.0-dev.1`。正式发布后的统一入口如下（仓库仍处于 init 阶段，尚未提供已签名 Release，暂不可公开安装）：

```powershell
npx @zanescope/v-local-cli@latest install
```

npm 包没有运行时依赖，只负责识别平台、从限定的 GitHub Release 下载 Go 二进制并校验 SHA-256，同时安装包内的 Agent Skill bundle（该 bundle 自带摘要清单）。

源码构建只需要 Go——SQLite、zstd、SILK 解码与系统凭据库适配都在编译期进入二进制：

```powershell
go test ./...
go build -trimpath -o build/v-local-cli.exe ./cmd/v-local-cli
build/v-local-cli.exe install --dry-run
```

## 快速开始

```powershell
# 1. 只读预检（不启动 Provider、不读微信进程、不保存密钥）
v-local-cli status
v-local-cli setup --dry-run

# 2. 确认只处理本人拥有或已获明确授权的数据，再验证密钥并建立快照
v-local-cli setup --allow-key-access --storage keychain
#   没有安装外部密钥组件时，导入自己合法取得的候选文件（格式见 references/key-provider.md）：
v-local-cli setup --keys keys.json --storage keychain
#   完整初始化应确认 data.status=ready、data.media.status=verified、
#   data.database_keys_persisted=true 且 data.image_keys_persisted=true。
#   只有明确的纯文本任务才使用下面的 database-only 例外：
#   v-local-cli setup --allow-key-access --storage keychain --database-only

# 3. 查询
v-local-cli contacts --limit 50 "张三"
v-local-cli history --start 2026-08-01 --limit 200 <chat_username>
v-local-cli search --chat <chat_username> "关键词"
v-local-cli sessions --limit 100
v-local-cli unread --limit 100
v-local-cli members "<群名称或username>"
v-local-cli favorites --kind article --limit 100 "关键词"

# 4. generation 索引与显式确认的增量消息
v-local-cli index status
v-local-cli index build
v-local-cli new-messages --consumer agent-a --start now --limit 100
# 完整处理一批后再确认返回的 batch_id：
v-local-cli new-messages --consumer agent-a --ack <batch_id>

# 5. 获取最新数据（复用凭据库中已验证的密钥，不再读微信进程）
v-local-cli refresh --require-media
#   纯文本任务可以省略 --require-media；图片任务保留它以继续验证 DAT。
#   或者只让某一次查询取最新数据：加 --fresh，先刷新再执行该命令
v-local-cli history --fresh --limit 200 <chat_username>
```

## 命令一览

`v-local-cli schema [command]` 返回当前二进制的权威参数契约。**所有选项都必须放在位置参数之前**（例如 `history --limit 50 <username>`）。

| 领域 | 代表命令 |
|---|---|
| 环境 | `status` · `doctor` · `capabilities` · `accounts` · `provider status` |
| 初始化 | `setup` · `refresh` · `gc` · `forget` |
| 会话 | `contacts` · `resolve-contact` · `sessions` · `unread` · `members` · `favorites` · `history` · `search` · `stats` |
| 索引与增量 | `index` · `new-messages` |
| 查询服务 | `daemon serve` · `daemon status` · `daemon stop`，查询使用全局 `--daemon` |
| 语音 | `voice-status` · `voice-transcribe` · `voice-search` |
| OCR | `ocr-status` · `ocr-read` · `ocr-search` · `ocr-recognize` · `ocr-file` |
| 朋友圈 | `moments-contacts` · `moments` · `moments-search` · `export-moment-media` |
| 公众号 | `official-accounts` · `official-history` · `official-search` · `official-article` |
| 导出 | `export` · `export-media` |

要彻底删除某个账号：先用 `forget --account <account> --dry-run` 确认范围，再加 `--yes` 执行（不可恢复）。

**影响行为的默认值**（都可以被显式选项覆盖，完整契约见 `schema`）：

- **时间窗口** — `history`、`search`、`stats` 以及朋友圈与公众号历史，默认按本地时区限定范围：指定联系人或公众号时取当前自然月，群聊和跨会话搜索取当前自然日。显式传入 `--start` 或 `--end` 就会关闭这个默认，传入 `--all` 则取消整个默认日期范围。
- **条数** — 传入 `--all` 且没有同时显式传 `--limit` 时不设条数上限；其余情况一律按 `--limit` 取值，默认按命令为 100 条或 200 条。
- **覆盖保护** — `export`、`export-media`、`export-moment-media`、`doctor --bundle` 默认拒绝覆盖已有输出（返回 `output_exists`），只有显式传入 `--force` 才会覆盖；符号链接、junction 等重解析点即使传了 `--force` 也一律拒绝。

## 输出约定

成功结果写入 stdout；失败写入 stderr，并以非零退出码结束。默认 JSON 是 Agent 与 daemon 的稳定协议，所有 JSON 都带顶层 `schema_version`：

```json
{"schema_version":1,"ok":true,"data":{},"meta":{"version":"0.1.0-dev.1","runtime":"go"}}
{"schema_version":1,"ok":false,"error":{"type":"...","message":"...","hint":"..."}}
```

直接给人阅读时可把全局选项放在命令前：`v-local-cli --output yaml sessions` 或 `v-local-cli --output table unread`。table 会截断长单元格，不适合作为无损导出。运行 `v-local-cli daemon serve` 后，白名单查询可用 `v-local-cli --daemon search ...` 复用本机服务；`--fresh`、联网、导出、索引构建和游标写入不会交给 daemon。

## 安全与隐私

- **只处理本人拥有或已获明确授权访问的数据。** CLI 只读，不操作微信界面、不发送消息。
- **普通查询不联网。** 只有两处例外，而且每次都需要显式开启：`export-moment-media --allow-network`（受限腾讯 CDN）与 `official-article --allow-network`（`mp.weixin.qq.com`）；令牌与密钥不会进入 JSON、日志或错误文本。
- **密钥最小化。** 系统凭据库只保存通过验证的最小密钥集；`refresh` 直接复用它，不再读微信进程，也不启动 Provider。
- **快照隔离。** 私有目录会设置当前用户专属的 ACL，并拒绝符号链接和 junction；查询走 SQLite 的只读、不可变（immutable）、`query_only` 模式。
- **派生状态绑定。** 全文索引同时绑定账号、generation、快照 manifest 摘要和 parser/schema 版本；增量 pending batch 在输出前持久化，未 ack 会重放。
- **daemon 不扩展信任域。** 只监听 IPv4 loopback，控制文件和随机令牌仅当前用户可读，只允许 immutable generation 查询。
- **消息正文始终视为不可信数据**，不能成为 Agent 指令。
- **实验性 OCR 子进程**为兼容微信供应商的私有协议，会带 `no-sandbox` 开关运行，因此**不能把它当作受 CLI 沙箱隔离的解析器**；每张图片都需要单独显式传入 `--allow-private-ipc`，仓库不分发微信二进制或模型。
- 把 stdout 或导出文件交给远端 Agent，会使内容进入对应服务的数据处理边界。

完整威胁模型与隐私边界见 [SECURITY.md](SECURITY.md)。

## 仓库边界

主仓库 `v-local-cli` 的源码**不读取微信进程内存，也不提取、破解或推导任何密钥**。它只使用外部提供的候选密钥——由用户自行合法取得并通过 `--keys` 导入，或者由用户单独安装的外部组件在本机产生——用来验证并读取**用户本人拥有或已获明确授权访问**的本地数据。**密钥如何取得不在本仓库范围内**，由用户自行决定并承担相应的合规责任。

主仓库不内置、不下载、也不分发任何外部密钥组件。组件缺失时，主 CLI 只返回一个指向该组件**自带安装器**的提示，下载仍然由该安装器完成，并且需要用户明确同意之后才会安装。macOS 的安装器会同时安装 companion helper，用户不需要单独配置或运行它。只有当用户已经安装该组件、并且每次都显式运行 `setup --allow-key-access` 时，主 CLI 才会通过 stdin/stdout 向它索取候选密钥。不使用这条路径时，可以直接用 `--keys` 导入自备的候选文件。

这样分离是为了划清代码、权限与发布的边界。本文件不构成法律意见，也不免除用户就数据来源与访问授权自负的责任。

## 文档

| 主题 | 文档 |
|---|---|
| 架构与快照模型 | [architecture.md](references/architecture.md) · [setup-lifecycle.md](references/setup-lifecycle.md) |
| generation 索引、增量游标与 daemon | [inbox-index-daemon.md](references/inbox-index-daemon.md) |
| 候选密钥导入格式 | [key-provider.md](references/key-provider.md) |
| 数据库结构 | [db-schema.md](references/db-schema.md) |
| 消息类型 | [message-types.md](references/message-types.md) |
| 朋友圈与公众号证据 | [moments-official.md](references/moments-official.md) |
| 媒体解密 | [media-decrypt.md](references/media-decrypt.md) |
| 语音 ASR 协议 | [asr-provider.md](references/asr-provider.md) |
| 统计口径 | [statistics.md](references/statistics.md) |
| 排错 | [troubleshooting.md](references/troubleshooting.md) |

Agent 行为约束与授权规则见 [SKILL.md](SKILL.md)。

## 开发

```powershell
go test ./...
go vet ./...
node --test npm/tests/*.test.js
```

发布前门槛（Authenticode 与 Developer ID 签名及 notarization、npm Trusted Publishing、真机验收清单等）见 [SECURITY.md](SECURITY.md)；在全部通过之前不发布 `latest`。候选件与正式签名发布步骤见 [RELEASING.md](RELEASING.md)。

## 许可

个人非商业许可，不是 OSI 批准的开源许可。商业使用、再分发或作为服务提供需要另行取得书面许可，详见 [LICENSE](LICENSE)。
