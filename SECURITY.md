# 安全政策

## 报告漏洞

请优先使用 GitHub 仓库的「非公开报告安全漏洞」功能，不要在公开 Issue 中附上密钥、微信数据路径、聊天内容、媒体 URL 或临时令牌。

报告建议包含：受影响版本、平台、最小复现步骤、期望与实际结果，以及 `v-local-cli doctor --bundle <file>` 生成的脱敏诊断包。维护者确认接收前，请不要把完整快照或原始 Provider 输出发给任何人。

## 支持范围

当前版本为 `0.1.0-dev.1`。稳定支持范围只适用于未来的最新 signed release；`-dev.N`
unsigned early-access 即使带有摘要和 GitHub 来源证明，也不应作为稳定安全边界或平台签名信任。

## 本地子进程边界

密钥 Provider、whisper.cpp、ASR 适配器以及实验性微信 OCR 都是当前桌面用户权限下的本地执行边界，不是沙箱。发行版 Provider 只能来自其当前用户固定安装目录：CLI 在调用和 daemon 复用前验证 canonical file identity、平台签名及实际 PID image；Windows 固定 Authenticode 叶证书 SHA-256，macOS 固定 identifier 并要求 CLI/Provider/helper 同一 Developer ID Team。`--provider`、`V_LOCAL_CLI_KEY_PROVIDER`、PATH 替代和 helper 路径 override 在发行构建中 fail closed。开发构建允许显式测试组件，但协议的输出限制仍不能约束恶意同用户程序；高保证环境应使用操作系统沙箱、防火墙或专用低权限账号。ASR/whisper 等其他可选程序仍必须由用户选择并从可信来源验证。

密钥流程不把候选写入 endpoint、resume 或普通临时文件。Unix 进程要求 core dump hard limit 为零；Windows 要求 WER 禁止堆采集，并把敏感 byte buffer登记为 excluded memory block。若启动时无法启用这些 crash artifact 门禁，CLI/Provider 会拒绝密钥处理。Go 字符串和第三方操作系统组件仍不提供形式化内存清零保证，因此不要为这些进程启用外部全内存转储；真机发布验收必须检查组织级 crash dump 策略。

Windows 原生 OCR 只从系统 Known Folder API 返回的 Program Files 根发现已安装微信，不信任 `ProgramFiles*` 环境变量，并校验组件 ZIP 的路径、大小和 CRC。该实验后端为兼容微信私有 Mojo 协议，会向微信 OCR 子进程传入供应商协议所需的 `no-sandbox` 开关；因此每张图片都必须单独取得 `--allow-private-ipc` 授权，不能把它视为受 CLI 沙箱隔离的解析器。

## 隐私边界

CLI 不上传遥测。普通查询和刷新不联网。只有用户对具体朋友圈媒体显式传入 `--allow-network` 时，才会把该记录自带的临时令牌发给它绑定的受限腾讯 CDN；只有用户对具体公众号文章显式传入 `--allow-network` 时，才会把从本地卡片重新验证并清理后的公开文章标识发送给 `mp.weixin.qq.com`。这两类请求都不使用浏览器 Cookie、不跟随重定向，也不会复用彼此的网络授权。

## 派生索引、增量游标与查询 daemon

generation 消息索引与 consumer 游标属于账号私有派生状态，采用与快照相同的当前用户专属目录权限和重解析点拒绝规则。索引 manifest 绑定账号、generation、snapshot manifest 摘要、schema 和 parser 版本；绑定不符时拒绝查询。结构化全文文本会排除名称含 token、secret、key 等敏感语义的字段，但索引本身仍包含聊天正文和完整结构化消息，因此必须按快照同等级保护。

增量 poll 在返回前原子持久化 pending batch，只有正确 `batch_id` 被 ack 后才推进；这保证 at-least-once，不保证 exactly-once。调用方需要按 `evidence_id` 幂等处理，不能在处理失败时自动 ack。`gc` 不删除仍被有效 consumer 引用的派生 generation。

查询 daemon 通过操作系统锁保持前台单实例，只绑定 IPv4 loopback 随机端口，使用当前用户私有状态中的随机 bearer token，并限制请求/响应大小、并发数和 deadline。白名单只允许 immutable generation 查询，明确拒绝刷新、Provider、账号源媒体解析、可变私有 ASR cache、联网、导出、索引构建和游标写入。它不构成跨用户安全服务：与 CLI 同一桌面用户权限运行的恶意进程通常也能读取该用户文件和进程资源；高保证环境仍应使用独立低权限账号或操作系统沙箱。不要把 endpoint 改为局域网或公网地址。
