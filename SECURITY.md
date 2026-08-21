# 安全政策

## 报告漏洞

请优先使用 GitHub 仓库的「私密报告安全漏洞」功能，不要在公开 Issue 中附上密钥、微信数据路径、聊天内容、媒体 URL 或临时令牌。

报告建议包含：受影响版本、平台、最小复现步骤、期望与实际结果，以及 `v-local-cli doctor --bundle <file>` 生成的脱敏诊断包。维护者确认接收前，请不要把完整快照或原始 Provider 输出发给任何人。

## 支持范围

当前仅支持最新发布版。仓库仍处于 `0.1.0-dev.1` init 阶段，尚未发布的构建不应作为稳定安全边界。

## 本地子进程边界

密钥 Provider、whisper.cpp、ASR 适配器以及实验性微信 OCR 都是当前桌面用户权限下的本地执行边界，不是沙箱。Provider/ASR/whisper 可执行文件必须由用户选择并从可信来源验证；协议的输出限制和 `network_used=false` 声明不能阻止恶意本地程序读取同一用户可访问的文件或自行联网。高保证环境应使用操作系统沙箱、防火墙或专用低权限账号。

Windows 原生 OCR 只从系统 Known Folder API 返回的 Program Files 根发现已安装微信，不信任 `ProgramFiles*` 环境变量，并校验组件 ZIP 的路径、大小和 CRC。该实验后端为兼容微信私有 Mojo 协议，会向微信 OCR 子进程传入供应商协议所需的 `no-sandbox` 开关；因此每张图片都必须单独取得 `--allow-private-ipc` 授权，不能把它视为受 CLI 沙箱隔离的解析器。

## 隐私边界

CLI 不上传遥测。普通查询和刷新不联网。只有用户对具体朋友圈媒体显式传入 `--allow-network` 时，才会把该记录自带的临时令牌发给它绑定的受限腾讯 CDN；只有用户对具体公众号文章显式传入 `--allow-network` 时，才会把从本地卡片重新验证并清理后的公开文章标识发送给 `mp.weixin.qq.com`。这两类请求都不使用浏览器 Cookie、不跟随重定向，也不会复用彼此的网络授权。

## 派生索引、增量游标与查询 daemon

generation 消息索引与 consumer 游标属于账号私有派生状态，采用与快照相同的当前用户专属目录权限和重解析点拒绝规则。索引 manifest 绑定账号、generation、snapshot manifest 摘要、schema 和 parser 版本；绑定不符时拒绝查询。结构化全文文本会排除名称含 token、secret、key 等敏感语义的字段，但索引本身仍包含聊天正文和完整结构化消息，因此必须按快照同等级保护。

增量 poll 在返回前原子持久化 pending batch，只有正确 `batch_id` 被 ack 后才推进；这保证 at-least-once，不保证 exactly-once。调用方需要按 `evidence_id` 幂等处理，不能在处理失败时自动 ack。`gc` 不删除仍被有效 consumer 引用的派生 generation。

查询 daemon 通过操作系统锁保持前台单实例，只绑定 IPv4 loopback 随机端口，使用当前用户私有状态中的随机 bearer token，并限制请求/响应大小、并发数和 deadline。白名单只允许 immutable generation 查询，明确拒绝刷新、Provider、账号源媒体解析、可变私有 ASR cache、联网、导出、索引构建和游标写入。它不构成跨用户安全服务：与 CLI 同一桌面用户权限运行的恶意进程通常也能读取该用户文件和进程资源；高保证环境仍应使用独立低权限账号或操作系统沙箱。不要把 endpoint 改为局域网或公网地址。
