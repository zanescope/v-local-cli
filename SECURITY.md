# 安全政策

## 报告漏洞

请优先使用 GitHub 仓库的「非公开报告安全漏洞」功能，不要在公开 Issue 中附上密钥、微信数据路径、聊天内容、媒体 URL 或临时令牌。

报告建议包含：受影响版本、平台、最小复现步骤、期望与实际结果，以及 `v-local-cli doctor --bundle <file>` 生成的脱敏诊断包。维护者确认接收前，请不要把完整快照或原始 Provider 输出发给任何人。

## 支持范围

当前版本为 `0.1.0-dev.1`。稳定支持范围只适用于未来的最新 signed release；`-dev.N`
unsigned early-access 即使带有摘要和 GitHub 来源证明，也不应作为稳定安全边界或平台签名信任。

## 本地子进程边界

密钥 Provider、whisper.cpp、ASR 适配器以及实验性微信 OCR 都是当前桌面用户权限下的本地执行边界，不是沙箱。发行版 Provider 只能来自其当前用户固定安装目录：CLI 在调用和 daemon 复用前验证 canonical file identity、平台签名及实际 PID image；Windows 固定 Authenticode 叶证书 SHA-256，macOS 固定 identifier 并要求 CLI/Provider/helper 同一 Developer ID Team。`--provider`、`V_LOCAL_CLI_KEY_PROVIDER`、PATH 替代和 helper 路径 override 在发行构建中 fail closed。开发构建允许显式测试组件，但协议的输出限制仍不能约束恶意同用户程序；高保证环境应使用操作系统沙箱、防火墙或专用低权限账号。ASR/whisper 等其他可选程序仍必须由用户选择并从可信来源验证。

公共聊天图片导出同样不扫描 PATH，也不会因为发现 `ffmpeg.exe` 就执行它。WXGF 资格 provider 只存在于显式测试接口，并要求宿主暂存、邻接身份清单和摘要绑定；测试通过不等于来源、签名、隔离、视觉等价或许可证门禁已经关闭，也不会自动接入公共 CLI。`decoder_unavailable` 的结构化诊断因此把本机二进制存在性保持为 `not_evaluated`，并分别报告公共接线与生产资格状态，避免把“未接线”误写成“系统缺少解码器”或反向升级为可安全执行。

密钥流程不把候选写入 endpoint、resume 或普通临时文件。Unix 进程要求 core dump hard limit 为零；Windows 要求 WER 禁止堆采集，并把敏感 byte buffer登记为 excluded memory block。若启动时无法启用这些 crash artifact 门禁，CLI/Provider 会拒绝密钥处理。Go 字符串和第三方操作系统组件仍不提供形式化内存清零保证，因此不要为这些进程启用外部全内存转储；真机发布验收必须检查组织级 crash dump 策略。

Windows 原生 OCR 只从系统 Known Folder API 返回的 Program Files 根发现已安装微信，不信任 `ProgramFiles*` 环境变量，并校验组件 ZIP 的路径、大小和 CRC。该实验后端为兼容微信私有 Mojo 协议，会向微信 OCR 子进程传入供应商协议所需的 `no-sandbox` 开关；因此每张图片都必须单独取得 `--allow-private-ipc` 授权，不能把它视为受 CLI 沙箱隔离的解析器。

## 隐私边界

CLI 不上传遥测。普通查询、聊天图片本地导出和刷新不联网。朋友圈媒体与公众号文章继续分别要求本次 `--allow-network`，且不会复用彼此的授权。

聊天图片使用更窄的授权模型：`recover-chat-image` 第一次调用只离线检查当前不可变快照并创建五分钟有效的一次性 challenge；它不接受长期 `--allow-network` 开关。challenge 只保存摘要，绑定具体账号、消息、图片候选、generation、snapshot manifest 和输出目标。签发与消费都持有同账号快照事务锁并重新加载当前 state，防止并发 refresh 在检查后更换 generation；拿不到锁时在联网和消费 challenge 前返回 `snapshot_busy`。Agent 必须在用户对这一个 challenge 明确同意后才传入 `--consent`；challenge 在任何联网动作前原子消费，不能重放。该授权只允许向快照直接携带的 `novac2c.cdn.weixin.qq.com` HTTPS full URL 发起一次 GET，不授权操作微信 UI，也不能用于从十六进制桌面参数猜测或拼接 URL。

聊天图片请求不使用环境代理、浏览器 Cookie、外部 DNS 回退或重定向。CLI 在写出前核对响应大小、MIME、完整图片结构、候选描述符摘要、消息绑定以及描述符提供的 MD5 或“长度 + 成对尺寸”。下载和解密缓冲区会清零；落盘临时文件在 Windows 使用只允许当前用户与 LocalSystem 的受保护 DACL，在 Unix 使用 owner-only `0600`。清理失败会显式报错并说明最终输出是否已经提交。URL、鉴权参数和描述符不作为长期信任根；结果分别记录 `observed_at`、`retrieved_at` 和 `descriptor_expiry_known=false`。即使成功，`source_original_quality_status` 仍为 `unknown`。

## 派生索引、增量游标与查询 daemon

generation 消息索引与 consumer 游标属于账号私有派生状态，采用与快照相同的当前用户专属目录权限和重解析点拒绝规则。索引 manifest 绑定账号、generation、snapshot manifest 摘要、schema 和 parser 版本；绑定不符时拒绝查询。结构化全文文本会排除名称含 token、secret、key 等敏感语义的字段，但索引本身仍包含聊天正文和完整结构化消息，因此必须按快照同等级保护。

增量 poll 在返回前原子持久化 pending batch，只有正确 `batch_id` 被 ack 后才推进；这保证 at-least-once，不保证 exactly-once。调用方需要按 `evidence_id` 幂等处理，不能在处理失败时自动 ack。`gc` 不删除仍被有效 consumer 引用的派生 generation。

查询 daemon 通过操作系统锁保持前台单实例，只绑定 IPv4 loopback 随机端口，使用当前用户私有状态中的随机 bearer token，并限制请求/响应大小、并发数和 deadline。白名单只允许 immutable generation 查询，明确拒绝刷新、Provider、账号源媒体解析、可变私有 ASR cache、联网、导出、索引构建和游标写入。它不构成跨用户安全服务：与 CLI 同一桌面用户权限运行的恶意进程通常也能读取该用户文件和进程资源；高保证环境仍应使用独立低权限账号或操作系统沙箱。不要把 endpoint 改为局域网或公网地址。
