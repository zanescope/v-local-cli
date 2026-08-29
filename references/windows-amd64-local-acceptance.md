# Windows amd64 本机端到端真机验收

本流程用于在一台明确获授权的 Windows x64 真机上，验证 `v-local-cli` 与独立 Key Provider 的完整闭环：密钥获取、Windows Credential Manager 持久化、无进程访问的凭据复用、指定联系人 `username=dong_zzc` 的本地历史记录、聊天图片 high 缓存档位、收藏、朋友圈及朋友圈本地媒体。

当前 Provider registry 已包含一个完成本机 qualification 的精确 Windows amd64 条目，可供标准 development/candidate 构建按登记 route 验收；其他 fingerprint 和架构仍保持未登记。该条目尚未取得绑定 GitHub 候选的正式 live evidence、attestation 和 promotion，因此正式 release 仍只能通过 fail-closed 负向门禁，不能把 qualification-only 结果宣称为正式发布支持。不得用源码构建、mock、x64/ARM64 互推或手工关闭门禁替代正式证据。

## 1. 验收边界

- `dong_zzc` 是目标联系人的稳定 username，不是本机登录微信账号。所有 `--account` 参数使用 `accounts` 返回的本机账号名或 account ID。
- CLI 只读取用户本人拥有或已获明确授权的数据，不发送消息、不修改微信数据库、不自动登录、切换账号、重启或结束微信。
- 基线流程不联网。朋友圈媒体只验收已缓存的本地强绑定候选；`export-moment-media --allow-network` 与聊天图片 `recover-chat-image --consent` 都属独立可选验收，必须再次取得逐项授权。聊天图片授权不包含微信 UI 自动化。
- 历史、收藏和朋友圈都只代表当前不可变 generation 中的本地留存范围。朋友圈的 `complete_remote_history=false` 和 `complete_interaction_history=false` 是正确边界，不是失败。
- 原始查询 JSON、图片和聊天正文只留在本机私有目录，不进入 CI artifact、Issue、PR、聊天记录或 release evidence。可分享证据只保存版本、摘要、枚举、计数、宽高和布尔断言。
- 密钥、候选、Credential Manager 内容、数据库正文、绝对微信路径、CDN token 和 Provider 原始响应不得写入验收证据。

## 2. 固定验收矩阵

| ID | 验收项 | 必须满足 |
| --- | --- | --- |
| W64-00 | 平台与发布件 | 原生 Windows x64；CLI/Provider 均为 `windows/amd64`；正式候选 Authenticode、时间戳、固定安装路径和摘要有效 |
| W64-01 | 未登记目标负向门禁 | registry 不匹配时不读取目标进程内存、不保存 secret，稳定返回 unregistered/unsupported |
| W64-02 | Provider 路由证据 | 精确 fingerprint、signer、product identity、版本/build/amd64 命中 eligible 条目；已验真的 Config.Cipher 或受约束 fallback route 完成 |
| W64-03 | 密钥获取 | 默认同时请求 database/media；结果 complete/ready，数据库完整覆盖，真实 DAT 媒体验证完成 |
| W64-04 | 密钥存储 | `storage=keychain`，数据库 credential 按 Catalog 要求持久化，`image_keys_persisted=true`；输出与日志无 secret |
| W64-05 | 凭据复用 | 新 PowerShell 进程执行 `refresh --require-media`，只用 `saved_keychain`，不访问微信进程、不修改 secret |
| W64-06 | 联系人绑定 | `resolve-contact` 唯一解析到区分大小写完全一致的 `dong_zzc` |
| W64-07 | 历史记录 | 明确日期窗内达到预置最小条数，chat 精确为 `dong_zzc`，数据库 coverage complete，已知收发方向和图片消息存在 |
| W64-08 | 聊天图片恢复矩阵 | 从历史消息的 `image_evidence_id` 导出；覆盖本地较低层级、可解码 high、本地 WXGF、不透明桌面描述符，以及结构化 full URL 单次授权负向/正向门禁；人工打开原图回退最多 refresh/重试一次，full URL challenge 最多请求一次 |
| W64-09 | 收藏 | `favorite.db/fav_db_item` source complete；达到预置最小条数，已知类型过滤结果一致 |
| W64-10 | 朋友圈 | 精确作者、本地行和显式时间窗一致；无 identity conflict/unparsed；已知帖子、互动和本地媒体计数达到预置值 |
| W64-11 | 朋友圈媒体 | 使用具体 `media.evidence_id` 本地导出，`network_access_performed=false`，容器严格验证成功 |
| W64-12 | 代际一致性 | W64-06 至 W64-11 使用同一个 generation 与 manifest；不混用 setup 前后或多次 fresh 的结果 |
| W64-13 | 隐私与回滚 | doctor bundle 脱敏；失败不发布半 generation、不覆盖旧 generation、不泄露密钥或正文 |

W64-00 至 W64-13 全部通过才算本机端到端通过。因本地留存而返回零条的结果是“夹具不充分/不确定”，不能算成功，也不能直接算解析器缺陷；先确认微信本地确有已知夹具。

## 3. 测试数据准备

验收前由操作者在微信中人工确认以下本地夹具；CLI 不负责创建它们：

1. 本机登录的是待验收账号，且 `dong_zzc` 是该账号中的唯一稳定 username。
2. 选择一个明确的 `$Start`/`$End` 日期窗。窗口内至少有一条已知收到的文本、一条已知发出的文本和一条图片消息。
3. 为 W64-08 准备四个独立图片状态：可完整解码的 `medium|thumbnail` 层级存在但更高层级缺失；可完整解码的 high 缓存层级；本地强关联 WXGF 候选（它可能是较低层级旁路成功，也可能因没有可解码回退而返回预期错误）；一条较旧、带结构可解析但协议未验真的远端描述符，且一次人工打开后仍未落盘更高层级候选的消息。最后一类只能证明“远端可能过期或资源不可用”，不能在 CLI 未发出已验真请求时断言确切过期原因。不要设置固定像素门槛：源图本身可能分辨率很低，`quality_tier` 只证明微信缓存层级，`width`/`height` 只记录已解码结果尺寸。
4. 准备至少一条已知收藏，并记录期望类型，例如 `article`、`image` 或 `text`。
5. 确认当前本地可见范围中至少保留一条 `dong_zzc` 的朋友圈。若要验收朋友圈图片，先在微信中打开该媒体，使本地缓存存在。
6. 记录预期最小计数，不把“命令退出成功但返回 0”当作通过。

建议在仅本机可见的操作者清单中填写：

```powershell
$Account = '<accounts 返回的本机账号名或 account_id>'
$TargetUsername = 'dong_zzc'
$Start = '<YYYY-MM-DD>'
$End = '<YYYY-MM-DD>'
$ExpectedHistoryMin = 3
$ExpectedFavoritesMin = 1
$ExpectedMomentsMin = 1
$ExpectedLocalMomentMediaMin = 1
```

这些值是本机私有验收输入，不写入可分享证据。

## 4. 私有输出与脱敏证据目录

聊天图片和临时人工复核结果放入私有目录；脱敏摘要单独保存。两者都必须是普通目录而非 symlink/junction/reparse point：

```powershell
$RunId = Get-Date -Format 'yyyyMMdd-HHmmss'
$PrivateRoot = Join-Path $env:LOCALAPPDATA "v-local\acceptance-private\$RunId"
$EvidenceRoot = Join-Path $env:LOCALAPPDATA "v-local\acceptance-evidence\$RunId"
New-Item -ItemType Directory -Path $PrivateRoot,$EvidenceRoot | Out-Null
if ((Get-Item -LiteralPath $PrivateRoot).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'private root is a reparse point' }
if ((Get-Item -LiteralPath $EvidenceRoot).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'evidence root is a reparse point' }
Get-Acl -LiteralPath $PrivateRoot
Get-Acl -LiteralPath $EvidenceRoot
```

ACL 人工检查至少应确认普通 `Users`/`Everyone` 没有写权限。`$PrivateRoot` 永不上传；`$EvidenceRoot` 只允许写入下文定义的脱敏字段。

## 5. W64-00：平台、架构与发布件预检

使用最终待发布件，不用 `go run` 或 PATH 中的同名文件代替：

```powershell
$Cli = '<固定安装位置的 v-local-cli.exe>'
$cpuArchitecture = Get-CimInstance Win32_Processor | Select-Object -First 1 -ExpandProperty Architecture
if ($cpuArchitecture -ne 9) { throw 'host CPU is not native x64/amd64' }

$doctor = (& $Cli doctor | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $doctor.data.platform -ne 'windows' -or $doctor.data.arch -ne 'amd64') { throw 'CLI runtime is not windows/amd64' }

$providerStatus = (& $Cli provider status --show-paths | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or -not $providerStatus.data.executable_present) { throw 'Provider is unavailable' }
$Provider = $providerStatus.data.path

Get-FileHash -Algorithm SHA256 -LiteralPath $Cli
Get-FileHash -Algorithm SHA256 -LiteralPath $Provider
Get-AuthenticodeSignature -LiteralPath $Cli
Get-AuthenticodeSignature -LiteralPath $Provider
& $Cli schema setup
& $Cli schema refresh
& $Cli schema export-chat-image
& $Cli schema moments
```

正式候选必须同时满足：

- 两个签名的 `Status=Valid`，签名者与 release manifest 一致且带可信时间戳；
- `providerStatus.data.source=fixed_install`；
- `providerStatus.data.integrity=authenticode_verified`；
- CLI 和 Provider 的文件摘要与同版本 release manifest 一致；
- doctor 的 `arch=amd64`，目标微信进程的 Provider 证据也必须是 `process_architecture=amd64`，不能仅依据宿主 CPU。

源码/qualification build 可以用于生成候选兼容性证据，但证据中必须标为 `qualification_only`，不得通过 W64-00 或进入正式发布。

## 6. W64-01/W64-02：先验证 Provider 路由边界

### 当前空 registry 的负向基线

先记录 `status`、`accounts` 和 `setup --dry-run`。dry-run 不启动 Provider、不读微信进程、不保存密钥：

```powershell
$statusBefore = (& $Cli status | ConvertFrom-Json)
$accounts = (& $Cli accounts | ConvertFrom-Json)
$dryRun = (& $Cli setup --account $Account --dry-run | ConvertFrom-Json)
if ($dryRun.data.status -ne 'planned' -or $dryRun.data.process_access_performed -ne $false -or $dryRun.data.secrets_persisted -ne $false) {
    throw 'setup dry-run performed a protected action'
}
```

在操作者明确授权本次只读进程访问后，空 registry/未登记目标的 acquisition 必须 fail closed：

```powershell
& $Cli setup --account $Account --allow-key-access --storage keychain
```

预期为非零退出，错误 details 中的 `compatibility_registry_status=unregistered`、`config_cipher_route_status=unavailable_unregistered`，且没有已选 route、没有候选或 credential。随后重新运行 `status`，确认没有新 secret/新 generation 被提交。不得为了让负向测试通过而加入通配 fingerprint、仅凭 WinVerifyTrust 成功读取内存，或启用开发 override。

### 正向路由的前置门禁

只有以下证据均已存在才继续 W64-03：

- 精确 Windows compatibility entry 与本机微信 `version + build + executable_sha256 + signer_sha256 + product_identity + amd64` 完全匹配；
- entry 的 recipe、validated profiles 和 route support 已审查；
- Provider 专用 `TestWindowsLiveAcquisition` 在授权真机通过，并生成不含路径、username、正文、候选或密钥的 evidence；
- evidence 以自身 SHA-256 命名并被 entry 引用；
- release compatibility evidence gate 对 `TARGET=windows ARCH=amd64` 通过。

registry bootstrap 必须使用与正式构建隔离的 qualification 工具/测试 build；先审核 recipe 与脱敏结果，再生成内容寻址 evidence，最后重建并签名正式候选。测试 build 中的临时条目或占位 evidence digest 绝不能进入 release。

## 7. W64-03/W64-04：密钥获取与 Credential Manager 持久化

这是一次新的受保护操作。操作者必须再次明确同意本次 `--allow-key-access` 后运行：

```powershell
$setup = (& $Cli setup --account $Account --allow-key-access --storage keychain | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'setup failed; inspect the structured error locally' }
```

强制检查：

```powershell
if ($setup.data.status -ne 'ready') { throw 'setup is not ready' }
if ($setup.data.acquisition_result_code -ne 'complete') { throw 'acquisition is not complete' }
if ($setup.data.process_access_performed -ne $true) { throw 'Provider path was not exercised' }
if ($setup.data.storage -ne 'keychain' -or $setup.data.keychain_state_persisted -ne $true) { throw 'Credential Manager persistence failed' }
if ($setup.data.database_credential_status -notin @('persisted','not_required_plaintext_only')) { throw 'database credential was not persisted' }
if ($setup.data.media.status -ne 'verified' -or $setup.data.image_keys_persisted -ne $true) { throw 'image keys were not verified and persisted' }
if ($setup.data.database.summary.failed -ne 0 -or $setup.data.database.summary.skipped -ne 0) { throw 'database coverage is incomplete' }
```

如果 Provider 返回 `action_required`，只执行结构化 `next_action` 指定且由用户人工确认的动作。不要自动 `taskkill`、切换账号或重启微信；`dong_zzc` 是联系人，绝不能被当作目标登录账号。每次续接只传回当前 session 要求的精确 `--confirm-key-action`，mismatch、partial 或 stop-and-report 都不能算成功。

不要使用 `cmdkey`、调试器或脚本读取 Credential Manager secret 来“证明”已保存；W64-04 由 setup 的原子提交结果和下一阶段的独立 refresh 共同证明。

## 8. W64-05：新进程复用已保存凭据

关闭运行 setup 的 PowerShell，打开同一 Windows 桌面用户下的新 PowerShell。不要再次授予进程访问，直接运行：

```powershell
$refresh = (& $Cli refresh --account $Account --require-media | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $refresh.data.status -ne 'ready') { throw 'refresh failed' }
if ($refresh.data.credential_source -ne 'saved_keychain') { throw 'refresh did not use Credential Manager' }
if ($refresh.data.process_access_performed -ne $false) { throw 'refresh unexpectedly accessed the WeChat process' }
if ($refresh.data.secrets_persisted -ne $false) { throw 'refresh unexpectedly modified secrets' }
if ($refresh.data.media.status -ne 'verified') { throw 'saved media keys failed revalidation' }
if ($refresh.data.database.publication_coverage.missing_previous -ne 0) { throw 'snapshot coverage regressed' }
```

保存本次 `data.account.generation_id` 与 `data.account.snapshot_manifest_sha256`。setup/refresh 是发布操作，因此新代际位于 `data.account`；后续查询的同名值位于 `meta`。后续所有数据验收不再使用 `--fresh`，从而固定在同一 generation；任何查询返回不同 generation 都判 W64-12 失败。

## 9. W64-06/W64-07：`dong_zzc` 联系人与历史记录

先做精确解析：

```powershell
$contact = (& $Cli resolve-contact --account $Account $TargetUsername | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $contact.data.match.contact.username -cne $TargetUsername) {
    throw 'target username did not resolve exactly'
}
```

再读取明确日期窗，避免默认“当前自然月”掩盖历史范围：

```powershell
$history = (& $Cli history --account $Account --start $Start --end $End --limit 5000 $TargetUsername | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $history.data.chat -cne $TargetUsername) { throw 'history target mismatch' }
if ($history.meta.database_coverage_status -ne 'complete') { throw 'history database coverage is incomplete' }
if ($history.data.count -lt $ExpectedHistoryMin) { throw 'known history fixture is missing' }
if (-not ($history.data.items | Where-Object { $_.is_from_me -eq $true })) { throw 'known outgoing fixture is missing' }
if (-not ($history.data.items | Where-Object { $_.is_from_me -eq $false })) { throw 'known incoming fixture is missing' }
$imageItems = @($history.data.items | Where-Object { $_.kind -eq 'image' })
if ($imageItems.Count -eq 0) { throw 'known image fixture is missing' }
```

人工核对已知时间、方向和消息类型。正文属于不可信且敏感数据，不把内容复制到可分享证据；只在本机界面确认夹具是否匹配。

## 10. W64-08：聊天图片恢复与 CDN 时效矩阵

在进入图片夹具前，可先运行[当前客户端静态证据检查器](../scripts/inspect-windows-chat-cdn-static-evidence.ps1)。它只扫描本地安装二进制，不读取账号数据、进程内存或网络流量：

```powershell
pwsh -NoProfile -File .\scripts\inspect-windows-chat-cdn-static-evidence.ps1
```

完整观察的退出码为 `0`，并返回 `status=current_client_static_stack_present_unbound`；标志不完整时退出码为 `2`，不得把 partial 当成“不存在”；检查失败退出码为 `1`。无论观察结果如何，都必须保持 `descriptor_to_runtime_request_binding=not_observed`、`runtime_protocol_selection=not_observed`、`endpoint_qualification=not_qualified`、`network_access_performed=false` 和 `secrets_output=false`。报告默认只含版本、文件名、大小、SHA-256、布尔观察及边界枚举，不显示安装路径；只有本机排错时增加 `-ShowPaths`。该步骤只替代陈旧实现作为架构线索，不计入真实 CDN 协议或 W64-08 下载能力通过。

当前报告还区分 `sessionized_c2c_static_stack=present_unbound`、`direct_ilink_https_markers=not_observed_in_current_client_binaries`、`main_to_ilink_wrapper_static_reference=delay_import_observed_unbound` 和 `weixin_main_public_c2c_download_entry=not_observed_in_current_weixin_export_table`。它们分别表示当前主模块包含会话化 C2C 任务材料、三个相关客户端模块都没有观察到 iLink 直连路径/参数标志、主模块确实延迟依赖 wrapper，但没有稳定公开的主模块下载入口。模块依赖不能绑定某条消息或补足会话材料，不得据此拼接 iLink GET 或调用私有进程内 ABI。

如果要判断当日 xlog 是否具备无密钥解码条件，运行独立的 [xlog 结构检查器](../scripts/inspect-windows-chat-cdn-xlog-structure.ps1)：

```powershell
pwsh -NoProfile -File .\scripts\inspect-windows-chat-cdn-xlog-structure.ps1
pwsh -NoProfile -File .\scripts\inspect-windows-chat-cdn-xlog-structure.ps1 `
  -LogPath (Join-Path $env:APPDATA 'Tencent\xwechat\log\radium\ilink_YYYYMMDD.xlog')
```

省略 `-LogPath` 时只检查当天 `mm_YYYYMMDD.xlog`；显式路径也必须位于当前用户 `Tencent\xwechat\log` 下。退出码 `0` 仅表示帧结构检查完整，不表示已经解码或取得 CDN 资格。`encrypted_mars_xlog_private_key_required` 表示没有未加密帧，官方无密钥脚本不适用；检查器不读取或猜测私钥，不输出正文、路径、嵌入公钥或指纹。无论结果如何，都必须保持 `plaintext_event_binding=not_observed`、`descriptor_to_runtime_request_binding=not_observed` 和 `endpoint_qualification=not_qualified`。该步骤不计入 W64-08 通过，只用于阻止把加密日志误交给过时第三方工具。

2026-08-29 的哈希绑定静态调用链复审进一步观察到 `start_c2c_download -> _startDownloadMedia -> CreateC2CImageDownloadTask`，并要求 task/root/session 类材料；当前主模块没有稳定公开的 C2C 下载导出。系统连接元数据也不能把加密 TLS 请求绑定到某个消息描述符。因此仍不增加聊天图片长期 `--allow-network`、由十六进制参数构造 URL、包捕获观察器或进程注入路线。后续实现只对当前快照已经携带的严格 HTTPS full URL 提供 `recover-chat-image` 一次性 challenge；不透明参数继续使用用户手动打开指定原图后，Agent 自动 refresh 并对同一 evidence 单次重试。

优先使用仓库内的[半自动验收脚本](../scripts/accept-windows-chat-image-recovery.ps1)。脚本要求 PowerShell 7（`pwsh`）；它会先在同一 generation 采集四个初始夹具。WXGF 若返回预期的 `chat_image_unavailable/decoder_unavailable`，失败响应本身也必须带同一 `generation_id` 和 manifest，不能被伪装成导出成功。脚本只对 `lower_tier_missing` 和 `expiry_unknown_descriptor` 各询问一次，并在每次询问前用当前 generation 重新预检该 evidence；用户输入脚本显示的精确确认词后，脚本自动执行一次 `refresh --require-media` 和一次同 evidence 重试。所有恢复结束后，脚本会在最新 generation 上重新探测四个夹具，避免拼接不同快照的结果：

```powershell
pwsh -NoProfile -File .\scripts\accept-windows-chat-image-recovery.ps1 `
  -Cli 'C:\path\to\v-local-cli.exe' `
  -Account '<本机账号名或 account_id>' `
  -LowerTierMissingEvidenceId '<medium 或 thumbnail 且更高层级缺失的 evidence_id>' `
  -DecodableHighEvidenceId '<可解码 high 缓存档位 evidence_id>' `
  -WxgfCandidateEvidenceId '<本地 WXGF 强候选 evidence_id>' `
  -ExpiryUnknownDescriptorEvidenceId '<时效未知且疑似不可用 evidence_id>' `
  -RecoveryMode Prompt
```

默认私有图片位于 `%LOCALAPPDATA%\v-local\acceptance-private\<run-id>`，脱敏报告位于 `%LOCALAPPDATA%\v-local\acceptance-evidence\<run-id>\w64-08-chat-image-recovery.json`；两个新目录都会移除继承 ACL，只保留当前用户、SYSTEM 和 Administrators。默认控制台结果不显示绝对路径，只有本机操作者明确增加 `-ShowPaths` 才显示。报告不包含账号、evidence ID、图片 SHA-256、微信源路径、CDN URL、token 或 key；私有图片不会自动上传或删除。

退出码 `0` 表示四项本地/人工回退夹具与恢复结果全部通过；`1` 表示安全或契约检查失败；`2` 表示用户没有确认、仅使用了 `-RecoveryMode Skip`、较低层级恢复仍未得到可验真的 high 缓存档位，或时效未知描述符夹具实际仍能恢复，因此证据不足。预期的 WXGF 解码错误只有在格式、恢复动作、无网络边界和快照代际全部匹配时才算该夹具通过。`Skip` 只用于无交互诊断，绝不能标为通过。开发者可用 `-SelfTest` 在不读取微信数据的情况下检查脚本内置四状态契约。该脚本仍不联网；它不会传入聊天图片 `--allow-network` 或 `--consent`。结构化联网恢复使用下一节的独立门禁，不能把两组证据拼成一次真实请求。

### W64-08R：结构化 full URL 恢复门禁

`recover-chat-image` 的产品契约先由 Go 的 hermetic TLS/注入测试覆盖；真实 CDN 验收只有在当前快照确实包含 full URL 且用户对该条图片明确授权时才可选执行。无 full URL 不是失败，而是 `chat_image_recovery_protocol_unavailable` 的预期安全结果。先运行不读取微信数据、不联网的 [PowerShell schema 自检](../scripts/test-chat-image-recovery-consent.ps1)：

```powershell
pwsh -NoProfile -File .\scripts\test-chat-image-recovery-consent.ps1 `
  -Cli 'C:\path\to\v-local-cli.exe'
```

自动化门禁必须覆盖：未授权零请求、challenge 重放、challenge 过期、snapshot generation/manifest 变化、同账号快照事务锁竞争时在消费 challenge 前停止、候选描述符错配、URL/主机/路径/查询越界、重定向拒绝且无第二请求、超大响应、伪造 MIME、下载中断、CDN 鉴权或资源不可用、明文 MD5/长度/成对尺寸错配、临时文件清理失败，以及只能得到同级或更低缓存候选。成功响应必须同时满足：

- challenge scope 为 `single_account_message_image_candidate_attempt`，只消费一次且只发生一次网络尝试；
- `observed_at` 来自授权所绑定快照，`retrieved_at` 来自本次已验证响应；
- `remote_descriptor_status=verified_at_request_time`、`descriptor_expiry_known=false`、`descriptor_expiry_status=unknown_future`；
- HTTPS、公网目标、零重定向、64 MiB 加一个 AES block 上限、MIME、完整解码和消息/候选绑定全部通过；
- `quality_claim_scope=wechat_remote_variant_only`、`source_original_dimensions_known=false`、`source_original_quality_status=unknown`；LongEdge、ShortEdge、文件大小和 high/medium 名称均不参与原图证明。

真实请求若返回 `401/403/404/410`，只记录 `unknown_unavailable_at_request_time`，刷新快照并生成新 challenge；不得写成“已确认过期”。`429` 只记录限流，传输失败保持时效未知。`snapshot_busy` 在消费前停止，等待事务完成后可原样重试；一旦 challenge 已消费，后续任何失败都不得复用授权、自动循环、跟随重定向或回退到缩略图声称成功。

WXGF 的实验解码与人工视觉等价复审属于独立的[资格验证流程](wxgf-decoder-qualification.md#人工视觉等价复审)，不改变本节公开 CLI 的预期 `decoder_unavailable` 行为。该流程要求独立参考 PNG、内容/方向/裁剪/颜色四项人工确认、至少 4 个不同 WXGF 与感知指纹、两个复审时安装的微信版本及每版本 `high+medium` 覆盖；缓存档位和像素边长都不代表发送前源图质量。若操作者只查看解码图但跳过参考图，必须记录 `inconclusive/skipped`，不能关闭 W64-08 或任何发布门禁。
这些资格协议从未发布，最终格式直接收敛为 v1。矩阵只接受同时绑定宿主计算的邻接清单、
provider 与 FFmpeg SHA-256 的最终 v1 记录；宿主绑定前的早期草案记录只有在使用旧
identity basis 且缺少全部宿主绑定/资格状态字段时，才计入
`pre_binding_records_excluded`，不得静默补字段或拼入最终 v1。

下面保留等价的人工步骤，供审阅脚本输出或定位失败阶段。

从 `$imageItems` 中选择预先准备的夹具，而不是盲目使用第一条。所有 evidence ID 只保存在本机私有变量；首次诊断写入新的私有临时路径，避免恢复后的重试被 `output_exists` 阻断：

```powershell
$imageItems | Select-Object timestamp,evidence_id,media_md5
$FixtureId = '<lower-tier-missing|decodable-high|wxgf-candidate|expiry-unknown-descriptor>'
$ImageEvidenceId = '<人工选中的已知夹具 evidence_id>'
if (-not ($imageItems.evidence_id -ccontains $ImageEvidenceId)) { throw 'image evidence is outside the selected history result' }
$recoveredImage = $null
$recoveredExitCode = $null
$ImageProbeOutput = Join-Path $PrivateRoot "$FixtureId-before-recovery.bin"
$imageRaw = @(& $Cli export-chat-image --account $Account --output $ImageProbeOutput $ImageEvidenceId 2>&1)
$imageExitCode = $LASTEXITCODE
$imageExport = (($imageRaw | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine) | ConvertFrom-Json
if ($FixtureId -eq 'wxgf-candidate' -and $imageExitCode -eq 5) {
  if ($imageExport.command_status -cne 'failed' -or $imageExport.error.type -cne 'chat_image_unavailable' -or
      $imageExport.error.details.local_resolution_status -cne 'decoder_unavailable' -or
      $imageExport.error.details.detected_format -cne 'wxgf' -or
      [string]::IsNullOrWhiteSpace([string]$imageExport.meta.generation_id)) { throw 'WXGF failure contract or generation binding failed' }
} elseif ($imageExitCode -ne 0 -or $imageExport.command_status -cne 'succeeded') {
  throw 'evidence-bound chat image export failed'
}
```

固定验收矩阵如下。四项都要由独立夹具命中；不能把同一输出人工改字段后重复使用：

| 场景 | 必须观察到的结果 |
| --- | --- |
| 可解码较低层级、更高层级缺失 | `quality_tier=medium|thumbnail`、`higher_quality_local_status=missing`、`higher_quality_recovery_action=ask_user_to_open_original_then_refresh_and_retry`；两种较低层级语义相同，无论是否有描述符都必须 `network_access_performed=false` |
| 可解码 high 缓存层级 | 选择 `quality_tier=high`、`quality_claim_scope=wechat_cache_variant_only`、`source_original_dimensions_known=false`；不设置固定像素门槛；即使较低层级与 high 层级 SHA-256 不同也不构成冲突；同一 high 层级若出现不同内容才必须 `content_conflict` |
| 本地 WXGF 强候选 | 若另有可解码较低层级，成功响应必须带 `higher_quality_local_status=decoder_unavailable`、`higher_quality_detected_format=wxgf`；若没有可解码回退，则必须以退出码 `5` 返回 `chat_image_unavailable`，并在 `error.details` 中给出 `local_resolution_status=decoder_unavailable`、`detected_format=wxgf`、`recovery_action=do_not_request_redownload_same_candidate`。两种形态都必须绑定 generation、保持无网络，且不得再次要求用户打开原图 |
| 描述符时效未知/资源可能不可用 | 描述符存在且结构完整时返回 `remote_descriptor_status=present_expiry_unknown`、`remote_descriptor_parse_status=parsed_unverified_protocol`；后者不证明时效或协议。仍必须为 `remote_protocol_status=unverified_desktop_protocol`、`remote_acquisition_status=unavailable_unverified_protocol`；用户确认后只 refresh/重试一次，仍缺失就停止并报告“可能过期或不可用”，不得声称已经验证过期，也不得联网 |

只有成功返回 `higher_quality_local_status=missing` 的场景（任一可解码较低层级，或用于验证时效未知描述符边界的夹具）允许进入询问流程。Agent 先在当前 generation 重新预检同一 evidence，再明确请用户只在微信中打开该条原图；没有用户确认就停在此处。用户确认后，Agent 自动执行下列步骤，不让用户复制命令：

```powershell
$refresh = (& $Cli refresh --account $Account --require-media | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $refresh.data.credential_source -ne 'saved_keychain') { throw 'recovery refresh failed' }
if ($refresh.data.process_access_performed -ne $false -or $refresh.data.secrets_persisted -ne $false) { throw 'recovery crossed the saved-credential boundary' }

$RecoveredImageOutput = Join-Path $PrivateRoot "$FixtureId-after-single-refresh.bin"
$recoveredRaw = @(& $Cli export-chat-image --account $Account --output $RecoveredImageOutput $ImageEvidenceId 2>&1)
$recoveredExitCode = $LASTEXITCODE
$recoveredImage = (($recoveredRaw | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine) | ConvertFrom-Json
if ($recoveredImage.meta.generation_id -cne $refresh.data.account.generation_id) { throw 'recovery retry did not use refreshed generation' }
if ($recoveredExitCode -eq 0) {
  if ($recoveredImage.data.evidence_id -cne $ImageEvidenceId) { throw 'recovery changed evidence binding' }
  if ($recoveredImage.data.network_access_performed -ne $false) { throw 'chat image recovery unexpectedly used network' }
} elseif ($recoveredExitCode -eq 5 -and $recoveredImage.error.details.local_resolution_status -ceq 'decoder_unavailable' -and
          $recoveredImage.error.details.recovery_action -ceq 'do_not_request_redownload_same_candidate' -and
          $recoveredImage.error.details.network_access_performed -eq $false) {
  # 本地 WXGF 等候选已经存在；停止，不再要求用户打开原图。
} else {
  throw 'single recovery retry failed'
}
```

若成功响应中的 `$recoveredImage.data.higher_quality_local_status` 仍为 `missing`，本用例到此结束并记录 `stop_after_single_refresh`。描述符即使存在也可能在用户确认前、确认期间或首次落盘尝试时失效；不要第二次提示用户、不要循环 refresh，也不要把 `present_expiry_unknown` 改写成“可下载”或“已确认过期”。若成功响应中的更高层级或失败响应中的本地候选变为 `decoder_unavailable`，按 WXGF 分支处理，同样不再提示打开原图。

对“可解码 high 缓存层级”夹具（包括一次恢复后取得 high 层级的情况），最终结果验证相对缓存层级、强绑定和完整解码。像素尺寸继续记录，但不参与通过判定：

```powershell
$FinalImage = if ($recoveredImage -and $recoveredExitCode -eq 0) { $recoveredImage } else { $imageExport }
$FinalImageOutput = if ($recoveredImage -and $recoveredExitCode -eq 0) { $RecoveredImageOutput } else { $ImageProbeOutput }
if ($FinalImage.data.quality_tier -ne 'high') { throw 'fixture did not resolve to the high cache tier' }
if ($FinalImage.data.quality_claim_scope -ne 'wechat_cache_variant_only' -or $FinalImage.data.source_original_dimensions_known -ne $false) { throw 'image quality claim exceeds available evidence' }
if ($FinalImage.data.verified_by -ne 'message_resource_stem+hardlink_map+full_decode') { throw 'image lacks strong binding' }
if ($FinalImage.data.container_validation -ne 'full_decode' -or $FinalImage.data.network_access_performed -ne $false) { throw 'image validation boundary failed' }
$actualImageHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $FinalImageOutput).Hash.ToLowerInvariant()
if ($actualImageHash -cne $FinalImage.data.sha256) { throw 'exported image digest mismatch' }
```

输出文件扩展名不构成格式证据，以 `data.format` 为准。不同质量层级的不同摘要是正常现象；若命令报告同一最高质量层级内的不同强候选产生 `content_conflict`，必须停止，不能按文件大小、时间或目录邻近选择所谓“原图”。

## 11. W64-09：收藏

```powershell
$favorites = (& $Cli favorites --account $Account --limit 5000 | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'favorites query failed' }
if ($favorites.data.favorite_source_coverage.status -ne 'complete') { throw 'favorite source coverage is incomplete' }
if ($favorites.data.count -lt $ExpectedFavoritesMin) { throw 'known favorite fixture is missing' }

$favoriteArticle = (& $Cli favorites --account $Account --kind article --limit 5000 | ConvertFrom-Json)
```

根据预置夹具选择实际存在的 `--kind`，确认过滤后的每个 item.kind 都等于该类型。可分享证据只记录总数、按 kind 计数和 coverage 枚举，不记录 title、text、URL、from 或 chat。

## 12. W64-10/W64-11：朋友圈与本地媒体

先确认本地朋友圈联系人来源中存在目标，再读取显式日期窗：

```powershell
$momentContacts = (& $Cli moments-contacts --account $Account --limit 5000 $TargetUsername | ConvertFrom-Json)
$matchingMomentContact = @($momentContacts.data.result.items | Where-Object { $_.username -ceq $TargetUsername })
if ($matchingMomentContact.Count -ne 1) { throw 'target is not uniquely present in local moment contacts' }
$moments = (& $Cli moments --account $Account --start $Start --end $End --limit 5000 --resolve-media $TargetUsername | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $moments.data.contact -cne $TargetUsername) { throw 'moments target mismatch' }
$momentCoverage = $moments.data.moment_source_coverage
if ($momentCoverage.source_present -ne $true) { throw 'SnsTimeLine source is absent' }
if ($momentCoverage.identity_conflicts -ne 0 -or $momentCoverage.unparsed -ne 0) { throw 'moments contain identity conflicts or unparsed fixtures' }
if ($moments.data.count -lt $ExpectedMomentsMin) { throw 'known moment fixture is missing' }
if ($momentCoverage.complete_remote_history -ne $false -or $momentCoverage.complete_interaction_history -ne $false) { throw 'remote completeness was overstated' }
if ($momentCoverage.media_resolution.verified_local_media -lt $ExpectedLocalMomentMediaMin) { throw 'known local moment media fixture is missing' }
```

从已知帖子的具体 `media.evidence_id` 做本地导出：

```powershell
$MomentMediaEvidenceId = '<人工选中的已知朋友圈媒体 evidence_id>'
$MomentMediaOutput = Join-Path $PrivateRoot 'dong-zzc-moment-media.bin'
$momentMedia = (& $Cli export-moment-media --account $Account --output $MomentMediaOutput $MomentMediaEvidenceId | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'local moment media export failed' }
if ($momentMedia.data.network_access_performed -ne $false -or $momentMedia.data.source -ne 'local_cache') { throw 'baseline unexpectedly used network' }
if ([string]::IsNullOrWhiteSpace([string]$momentMedia.data.container_validation)) { throw 'moment media container validation failed' }
```

如果本地候选不存在，基线应以“夹具不充分”停止。不要自动重试 `--allow-network`。远端下载是单独用例：只在用户对这一个 evidence ID 明确授权后运行，并单独记录 `verified_remote_download`、域名约束和实际联网状态。

## 13. W64-12/W64-13：代际、隐私与失败原子性

比较 contact、history、最终 image export、favorites、moments 和 moment media 的 `meta.generation_id` 与 `meta.snapshot_manifest_sha256`，必须全部相同。通常它们等于 W64-05 refresh 发布的值；若 W64-08 经用户确认执行了恢复 refresh，则以该次新 generation 为唯一基线，废弃此前 W64-06/W64-07 的查询结果并重新执行，不能把恢复前后的证据拼在一起。除此之外若任何命令使用了 `--fresh` 或 generation 意外变化，废弃该组结果并从一次新的 refresh 重新开始。

生成 CLI 自带的脱敏诊断包：

```powershell
$DoctorBundle = Join-Path $EvidenceRoot 'doctor.json'
$doctorBundleResult = (& $Cli doctor --bundle $DoctorBundle | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0 -or $doctorBundleResult.data.diagnostic_bundle.sanitized -ne $true) { throw 'sanitized doctor bundle failed' }
```

还需验证：

- setup/refresh/query 失败时当前 generation 指针保持旧值；
- `$PrivateRoot` 以外没有明文聊天图片或原始查询转储；
- 普通日志、doctor bundle、Provider live evidence、WER 配置和临时目录不含 key、passphrase、media key、daemon token、username、正文或绝对微信路径；
- CLI/Provider 进程退出后没有遗留包含 secret 的命令行或环境转储；
- 不对真实主账号执行 `forget --yes` 作为例行验收清理。若专用测试账号确需清理，必须先运行 `forget --dry-run`，展示范围并另行取得不可恢复删除确认。

## 14. 可分享证据模板

可分享 summary 只允许以下形状；`null` 项必须由真实证据补齐，不能手工猜测：

```json
{
  "schema_version": 2,
  "run_status": "pass|fail|inconclusive",
  "environment": {
    "windows_build": null,
    "host_arch": "amd64",
    "cli_version": null,
    "cli_sha256": null,
    "provider_version": null,
    "provider_sha256": null,
    "wechat_version": null,
    "wechat_build": null,
    "wechat_executable_sha256": null,
    "wechat_signer_sha256": null
  },
  "provider": {
    "compatibility_registry_status": "registered_supported",
    "config_cipher_route_status": null,
    "route_selected": null,
    "process_architecture": "amd64",
    "target_binding_status": null,
    "compatibility_evidence_sha256": null
  },
  "credential": {
    "acquisition_result_code": "complete",
    "database_coverage_status": "complete",
    "media_coverage_status": "complete",
    "keychain_state_persisted": true,
    "refresh_credential_source": "saved_keychain",
    "refresh_process_access_performed": false,
    "secrets_included": false
  },
  "snapshot": {
    "generation_id": null,
    "manifest_sha256": null,
    "same_generation_for_all_queries": true
  },
  "target_assertion": {
    "exact_username_match_performed": true,
    "username_included": false
  },
  "history": {
    "window_recorded_privately": true,
    "count": null,
    "incoming_present": true,
    "outgoing_present": true,
    "image_present": true,
    "content_included": false
  },
  "chat_image": {
    "width": null,
    "height": null,
    "bytes": null,
    "digest_compared_locally": true,
    "content_digest_included": false,
    "verified_by": "message_resource_stem+hardlink_map+full_decode",
    "network_access_performed": false,
    "recovery_matrix": {
      "lower_tier_missing": "pass|fail|inconclusive",
      "distinct_decodable_high_selected": true,
      "wxgf_candidate_not_misreported_missing": true,
      "descriptor_freshness_not_overstated": true,
      "maximum_automatic_retries": 1,
      "still_missing_outcome": "stop_after_single_refresh"
    }
  },
  "favorites": {
    "count": null,
    "counts_by_kind": {},
    "source_status": "complete",
    "content_included": false
  },
  "moments": {
    "count": null,
    "parsed": null,
    "identity_conflicts": 0,
    "unparsed": 0,
    "verified_local_media": null,
    "complete_remote_history": false,
    "complete_interaction_history": false,
    "content_included": false
  },
  "privacy": {
    "contains_username": false,
    "contains_absolute_wechat_paths": false,
    "contains_database_content": false,
    "contains_chat_or_favorite_or_moment_content": false,
    "contains_urls_tokens_or_secrets": false,
    "private_artifacts_uploaded": false
  }
}
```

最终将 W64-00 至 W64-13 的状态逐项标为 pass/fail/inconclusive，并由操作者和审阅者分别签名确认。只要 registry、签名、完整 coverage、聊天图片 high 缓存档位强绑定、Credential Manager 复用或隐私检查任一项未通过，整体不得标记为 pass，也不得把平台状态从 `build_only` 晋级。
