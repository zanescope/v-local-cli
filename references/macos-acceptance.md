# macOS 真机验收

## 当前边界

当前状态按架构区分：`darwin/amd64` 为 `real_device_verified`，`darwin/arm64` 仍为 `build_only`。

`darwin/amd64` 已在 Intel 真机上用真实微信数据走通自动密钥获取与第二至第五阶段，因此 `data_layout_validation.darwin_amd64` 与 `provider.automatic_key_access_real_device_verified_targets` 都已包含该目标。

`darwin/arm64` 只会构建并在 GitHub macOS runner 上运行通用测试，这只证明 CLI、Unix 文件权限、锁、状态提交和 npm 启动入口能够在 macOS 执行，不证明 Apple Silicon 上当前微信版本的数据目录、数据库布局或本地凭据流程已经可用。

GitHub runner 的架构与临时虚拟机边界以 [GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners) 为准。

- `darwin/arm64` 可构建自动 Provider 及同包 companion helper，但尚无在 Apple Silicon 上成功读取正式微信进程的真机证据；该架构上候选 JSON 导入仍是可靠路径，自动能力继续按实验性 `build_only` 处理。
- Intel 上验证过的架构结论不能外推到 Apple Silicon：动态 Hook 在两种 ABI 上读取的寄存器不同，原生 arm64 微信必须单独验收。
- macOS 不支持微信原生 OCR；`ocr-status` 应明确返回不可用，不能下载或借用 Windows 微信组件绕过。这一条对两种架构都成立。
- 微信已有语音/OCR 文字索引只有 `windows/amd64` 布局证据；macOS 零命中不得解释为聊天中没有对应文字。Intel 真机验收没有覆盖索引布局，因此该声明不随 `darwin/amd64` 一起升级。
- 在 Apple Silicon 完成本清单前，README、`capabilities`、诊断和发布说明对 `darwin/arm64` 必须继续使用 `build_only`。

## 验收环境

`darwin/amd64` 的验收已在一台 Intel Mac 上完成。剩余的必需环境是一台 Apple Silicon Mac，安装当前稳定 macOS 与当前正式版微信；在它完成第二至第五阶段之前，Apple Silicon runner 只提供普通运行时兼容证据。

使用专门的测试账号或明确获授权的数据，并满足：

- 账号具有少量已知文本、群聊、图片、语音、朋友圈和公众号卡片样本；
- 测试者知道预期消息数量与时间范围，但不把正文、密钥、URL、令牌或数据库上传到 CI；
- 候选 JSON 保存在权限受限的本地临时目录，不进入仓库、命令日志或诊断包；
- 记录 macOS、CPU、微信和 CLI 版本，以及二进制 SHA-256；不记录用户名或绝对数据路径。

## 第一阶段：安装与平台边界

1. 验证候选发布件摘要，再在本机运行 `v-local-cli --version`、`capabilities` 和 `schema`。
2. 确认 `runtime.os=darwin`、架构正确，且在 Apple Silicon 上 `data_layout_validation.darwin_arm64=build_only`。
3. 运行 `accounts`、`status`、`doctor` 和 `provider status`。默认输出不得包含绝对路径；只有本地排错副本可以临时增加 `--show-paths`，且不得上传。
4. 确认 `provider.darwin_arm64_setup_source=user_supplied_candidate_file`、`darwin_arm64_automatic_helper=experimental_build_only`，原生 OCR 支持目标只有 `windows/amd64`。
5. 在源码检出目录运行完整 Go/npm 测试；macOS 定向测试必须覆盖账号路径、`0700` 权限、符号链接拒绝、账号锁、状态中断恢复、并发发布和硬链接隔离。

通过条件：命令可启动、JSON 契约稳定、默认输出无路径泄露，且任何未验证能力仍明确标为 `build_only` 或不可用。

## 第二阶段：账号发现与只读快照

1. 先运行 `accounts`，核对是否只发现含 `db_storage` 的真实账号目录；存在多个账号时必须显式选择，不能默认第一个。
2. 自动发现失败时，只在本地用 `V_LOCAL_CLI_ACCOUNT_DIR` 指向已核对的账号目录。记录「自动发现失败」，不能把显式路径成功算作自动发现通过。
3. 使用候选文件执行 `setup --dry-run --account <账号> --keys <本地候选文件> --storage snapshot-only`。
4. 核对计划后执行相同 setup，确认源数据库没有被修改，已发布快照具有 manifest、版本标识和数据库摘要。
5. 对联系人、私聊、群聊和结构化卡片分别运行受限 `contacts`、`history`、`search` 与 `stats`，再用人工已知样本核对时间、发送方、类型和条数。
6. 运行一次 JSON/JSONL 导出，验证默认拒绝覆盖、显式 `--force`、符号链接拒绝以及失败后无临时输出残留。

通过条件：发现范围、解密报告和查询结果与人工样本一致；任何缺失分片、未知消息类型或时间差异都有明确覆盖说明。

## 第三阶段：Keychain 与刷新

只在专用测试账号上执行：

1. 用相同候选重新 setup，选择 `--storage keychain`，确认只保存已经真实样本验证的最小候选集合。
2. 新增一条已知测试消息后执行 `refresh`，确认 `credential_source=saved_keychain`、`process_access_performed=false`、`secrets_persisted=false`。
3. 验证刷新生成新不可变版本；制造覆盖减少的测试副本时必须返回 `snapshot_coverage_regression` 并保留当前版本。
4. 退出终端并在同一桌面用户的新会话中再次刷新，验证凭据可读；其他用户身份不得获得这些候选。
5. 运行 `forget --dry-run` 核对范围。只有可丢弃测试数据才执行 `forget --yes`，并确认状态、快照、临时文件和 Keychain 项全部删除。

通过条件：保存、跨进程读取、刷新和删除均成功；失败时不得声称已刷新或已完全删除。

## 第四阶段：媒体、语音与 OCR

- 用已知本地图片样本验证 DAT 解密、摘要、容器校验和输出覆盖语义。
- `voice-status` 分开记录微信索引行数与 v-local-cli 私有缓存行数；可选 whisper.cpp 只用本地模型，失败后确认临时 WAV/文本已删除。
- `ocr-read`/`ocr-search` 只能报告实际读到的已有索引或私有缓存；`ocr-status` 在 macOS 必须保持原生后端不可用。
- 朋友圈和公众号网络命令继续逐项授权；平台验收不得批量联网，也不得把微信会话、Cookie 或聊天正文带出本机。

通过条件：本地媒体经过严格验证；语音临时明文无残留；原生 OCR 没有被误报为 macOS 能力。

## 第五阶段：签名、notarization 与安装

候选发布件应使用 Developer ID、Hardened Runtime 和安全时间戳签名，再提交 Apple notarization。验收至少包括：

没有 Developer ID 的本机兼容测试可以使用 ad-hoc helper；Provider 会在普通访问被拒绝后检查
SIP，并在 SIP 已关闭时请求管理员授权重试。该路径只能作为用户明确接受风险的实验路径，不能
替代正式发布签名，也不能把 SIP 已关闭误报为平台能力已验证。

签名与提交要求以 Apple 的 [Notarizing macOS software before distribution](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution) 为准。

- `codesign --verify --strict --verbose=2 <binary>` 成功；
- `spctl --assess --type execute --verbose=4 <binary>` 对最终下载件成功；
- 审阅 notarization 日志，不忽略警告；使用 PKG/DMG 时同时验证附加票据；
- 从干净目录经 npm 安装，重新校验下载摘要并运行 `version`、`capabilities` 和一次只读查询；
- 确认一次 npm 安装同时落地主程序与 helper，`provider status` 返回 `helper_available=true`，用户无需直接运行 helper；
- 分别测试直接下载和带 quarantine 属性的首次启动体验。

签名或 notarization 成功不替代真实微信数据验收。

## 证据与状态升级

每次验收只保存以下脱敏证据：

| 字段 | 内容 |
|---|---|
| platform | macOS 版本与 `arm64`/`amd64` |
| wechat | 微信版本，不含账号信息 |
| cli | CLI 版本、提交与二进制 SHA-256 |
| discovery | 自动或显式路径；不保存实际路径 |
| database | discovered/decrypted/skipped/failed 汇总 |
| coverage | 测试的命令、时间范围和已知样本类型 |
| security | 路径脱敏、Keychain、清理、签名和 notarization 结果 |
| gaps | 未覆盖分片、索引、媒体或架构 |

状态升级按架构分别判断，不得由一种架构的结果推导另一种。某个架构只有在真机完成第二至第五阶段后，才能标成「真实设备验证的候选文件导入模式」；只有该架构的自动密钥来源、至少一次独立复验以及真实数据测试也通过后，才可对它使用不带限定的 `real_device_verified`。

`darwin/amd64` 已按上述条件升级为 `real_device_verified`。Apple Silicon 未做真实数据测试时，`darwin/arm64` 必须继续保持 `build_only`；索引布局、索引行与原生 OCR 的验收字段不随本次升级变化，仍只声明 `windows/amd64`。
