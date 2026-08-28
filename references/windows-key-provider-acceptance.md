# Windows 密钥获取真机与发布回归

本清单对应 Windows 密钥获取与发布回归。GitHub Windows runner、交叉编译和 mock Provider 只提供构建/协议证据；`Config.Cipher`、进程内存布局、多账号隔离与实际 ARM64 架构必须在明确授权的专用 Windows 真机验证。

面向一台本机 Windows x64 的完整 CLI 数据闭环（Credential Manager 复用、`dong_zzc` 历史记录、强绑定高清图、收藏和朋友圈）读取 [Windows amd64 本机端到端真机验收](windows-amd64-local-acceptance.md)。本文继续作为 Provider 路由和正式发布的上层门禁。

当前 Provider registry 只包含一个完成本机 qualification 的精确 Windows amd64 目标，且
如实登记为 `Config.Cipher` 已审核但无可用结构、仅允许精确身份绑定的 memory fallback。
qualification-only evidence 不属于正式发布证据；在候选 attestation、正式 live evidence 和
promotion 完成前，release 仍保持 fail closed，x64 结论也不能复制到 ARM64 或其他 fingerprint。

## 测试组合

以下组合分别留存脱敏记录，不允许由一种架构推导另一种：

| ID | 环境 | 关键断言 |
| --- | --- | --- |
| WIN-01 | Windows x64、已登记 4.1.x fingerprint | 命中 fingerprint 如实登记的 route；`reviewed_no_structure` 只能走精确绑定 fallback，每库首页 HMAC 唯一通过 |
| WIN-02 | Windows x64、未登记 fingerprint/签名者 | 不使用固定偏移，也不读取目标进程内存；返回稳定的 unregistered/unsupported 诊断 |
| WIN-03 | Windows x64、完整身份精确登记且明确允许 fallback | 主路径不可用时 missing-only fallback 成功，已完成 ID 不重复扫描；仅匹配签名者不足以授权 |
| WIN-04 | Windows ARM64、原生 ARM64 微信（首发范围外） | 不生成首发资产；未来适配时 `process_architecture=arm64`，候选和 fingerprint 不借用 x64 结论 |
| WIN-05 | 多个 Weixin/WeChat、两个测试账号 | 候选按 process instance 隔离；目标 A/当前 B 返回 mismatch 且不保存根凭据 |
| WIN-06 | 一个进程 access denied、另一个可读 | 返回准确 partial/process counts；不得丢弃已验证结果或声称 complete |
| WIN-07 | 候选冲突与极短 deadline | conflict fail closed；deadline 在 session hard limit 内返回已验真的 partial/none |
| WIN-08 | 默认用户流程 | 进程实例前后保持运行；不调用宽泛 taskkill，不要求重启、退出登录或重新登录 |

Provider 仓库的专用真机工作流会在自托管 runner 上执行：

```text
go test -tags=live_regression -run '^TestWindowsLiveAcquisition$' -count=1 .
```

运行前必须准备 `V_LOCAL_KEY_PROVIDER_LIVE_*` 环境变量，并显式确认数据授权。测试不会自动结束进程、切换账号或修改系统策略。
每次运行还必须显式给出预期 `compatibility_registry_status`、
`config_cipher_route_status`、route、目标进程架构和（需要时）账号绑定状态。工作流上传的
`build/live/evidence.json` 只保留版本、二进制摘要、稳定枚举、计数与耗时，不包含用户名、
绝对路径、数据库正文、候选、密钥或进程内存。

## CLI、Credential Manager 与 refresh

1. 从干净的当前用户配置开始，验证 Provider 与 CLI 的 Authenticode、SHA-256 和时间戳；核对 `signature-manifest-windows-<arch>.json` 的编译模式、签名证书 SHA-256 和资产摘要。
2. 执行 `setup --allow-key-access --storage keychain`；确认当前 Catalog 完整时才保存账号级根凭据，partial 只保存逐库 effective override。
3. 关闭终端，在同一桌面用户的新会话执行 `refresh`；确认 `credential_source=saved_keychain`、`process_access_performed=false`。
4. 新增不同 salt 的测试数据库后再次 refresh，确认同 epoch 根凭据自动派生；无法覆盖时保持 partial 并触发覆盖率回退保护。
5. 切换到另一 Windows 用户，确认不能读取 Credential Manager 项、daemon token、resume、快照或 generation manifest。
6. 对测试账号执行 `forget --dry-run` 后再显式 `forget --yes`，确认 Credential Manager、状态、快照、索引与临时文件均清理。

## 文件、WAL、IPC 与异常退出

- 分别覆盖 plaintext、truncated、unreadable、持续变化、发现后替换的 `.db`；symlink/junction/reparse 必须拒绝。
- 在 WAL 不存在、空文件、正常提交、轮转和畸形提交大小下执行快照；源 DB/WAL 在复制期间变化必须返回 catalog drift 或稳定复制失败。
- daemon 只监听 loopback；猜测 token、重复 action receipt 和跨用户读取 endpoint 都失败。
- `provider status` 只接受 `%LOCALAPPDATA%\v-local\key-provider\windows-<arch>\v-local-key-provider.exe`；`--provider`、`V_LOCAL_CLI_KEY_PROVIDER`、PATH 同名文件、symlink/junction/reparse 和由另一张有效证书签名的替代文件都返回 component untrusted。endpoint 中 PID 的实际 image 必须等于该固定 Provider。
- 在 prepare、observe、finalize、快照发布和状态提交位置做进程异常退出；重启后不得泄漏 secret、发布半成品或覆盖旧 generation。
- 检查普通日志、doctor bundle、临时目录和 Windows Error Reporting dump 配置，不得出现数据库 key、passphrase、media key 或 daemon token；确认运行时 WinVerifyTrust 不触发网络，WER `NOHEAP` 已启用，敏感 byte buffer 的 excluded-memory 注册/注销正常。

## 正式发布

- `Get-AuthenticodeSignature` 为 `Valid`，存在可信时间戳；`signtool verify /pa /all` 成功；CLI/Provider 内嵌的叶证书 SHA-256 与 manifest 和实际签名证书三者一致。
- 从最终 npm tgz 安装，下载 URL、资产名和 checksum 一一匹配；安装路径不得经 symlink/junction 重定向；本地二进制 override 必须同时具备路径、development 与 allow 三重授权。
- 签名前确认外部 promotion 为每个架构绑定内容寻址的 `compatibility-evidence/<sha256>.json`，且候选摘要、来源 attestation、目标 fingerprint/签名、route、完整 coverage 与 validated profiles 和 registry 全部匹配；空 registry 或空 promotion 必须阻止 release。
- npm 安装后的二进制运行 `--version`、`schema`、JSON/YAML/table 烟测、setup/refresh/forget；不能用源码构建件替代。
- GitHub artifact attestation 与 npm Trusted Publishing 均成功，发布资产清单包含目标架构。

证据只记录 Windows/CPU/微信/CLI/Provider 版本、二进制摘要、route、计数、耗时和通过/失败枚举；不得记录用户名、绝对路径、数据库正文或任何 secret。
