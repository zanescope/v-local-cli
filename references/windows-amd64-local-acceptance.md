# Windows amd64 本机端到端真机验收

本流程用于在一台明确获授权的 Windows x64 真机上，验证 `v-local-cli` 与独立 Key Provider 的完整闭环：密钥获取、Windows Credential Manager 持久化、无进程访问的凭据复用、指定联系人 `username=dong_zzc` 的本地历史记录、聊天高清图、收藏、朋友圈及朋友圈本地媒体。

当前 Provider registry 已包含一个完成本机 qualification 的精确 Windows amd64 条目，可供标准 development/candidate 构建按登记 route 验收；其他 fingerprint 和架构仍保持未登记。该条目尚未取得绑定 GitHub 候选的正式 live evidence、attestation 和 promotion，因此正式 release 仍只能通过 fail-closed 负向门禁，不能把 qualification-only 结果宣称为正式发布支持。不得用源码构建、mock、x64/ARM64 互推或手工关闭门禁替代正式证据。

## 1. 验收边界

- `dong_zzc` 是目标联系人的稳定 username，不是本机登录微信账号。所有 `--account` 参数使用 `accounts` 返回的本机账号名或 account ID。
- CLI 只读取用户本人拥有或已获明确授权的数据，不发送消息、不修改微信数据库、不自动登录、切换账号、重启或结束微信。
- 基线流程不联网。朋友圈媒体只验收已缓存的本地强绑定候选；`export-moment-media --allow-network` 属独立可选验收，必须再次取得逐项授权。
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
| W64-08 | 聊天高清图 | 从该历史消息的 `image_evidence_id` 导出；资源 stem + hardlink 双绑定、完整解码、SHA-256 一致，达到夹具最低像素要求 |
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
3. 在微信中打开该图片的原图，使完整本地资源已缓存。记录最低分辨率要求，例如长边至少 1920、短边至少 1080；不要只根据文件名或 UI 的“原图”标签判断。
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
$MinImageLongEdge = 1920
$MinImageShortEdge = 1080
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

## 10. W64-08：按历史证据导出高清图

从 `$imageItems` 中选择预先准备的那一条，而不是盲目使用第一条。把其证据标识保存在当前 PowerShell 变量中：

```powershell
$imageItems | Select-Object timestamp,evidence_id,media_md5
$ImageEvidenceId = '<人工选中的已知夹具 evidence_id>'
if (-not ($imageItems.evidence_id -ccontains $ImageEvidenceId)) { throw 'image evidence is outside the selected history result' }
$ImageOutput = Join-Path $PrivateRoot 'dong-zzc-history-image.bin'
$imageExport = (& $Cli export-chat-image --account $Account --output $ImageOutput $ImageEvidenceId | ConvertFrom-Json)
if ($LASTEXITCODE -ne 0) { throw 'evidence-bound chat image export failed' }
```

验证强绑定和真实像素，而非扩展名：

```powershell
if ($imageExport.data.verified_by -ne 'message_resource_stem+hardlink_map+full_decode') { throw 'image lacks strong binding' }
if ($imageExport.data.container_validation -ne 'full_decode' -or $imageExport.data.network_access_performed -ne $false) { throw 'image validation boundary failed' }
$longEdge = [Math]::Max([int]$imageExport.data.width,[int]$imageExport.data.height)
$shortEdge = [Math]::Min([int]$imageExport.data.width,[int]$imageExport.data.height)
if ($longEdge -lt $MinImageLongEdge -or $shortEdge -lt $MinImageShortEdge) { throw 'resolved image is below the known high-resolution fixture threshold' }
$actualImageHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $ImageOutput).Hash.ToLowerInvariant()
if ($actualImageHash -cne $imageExport.data.sha256) { throw 'exported image digest mismatch' }
```

输出文件扩展名不构成格式证据，以 `data.format` 为准。若命令报告不同强候选产生不同内容，必须停止，不能按文件大小、时间或目录邻近选择所谓“原图”。

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

比较 contact、history、image export、favorites、moments 和 moment media 的 `meta.generation_id` 与 `meta.snapshot_manifest_sha256`，必须全部等于 W64-05 refresh 发布的值。若其中任何命令使用了 `--fresh` 或 generation 变化，废弃该组结果并从一次新的 refresh 重新开始。

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
  "schema_version": 1,
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
    "network_access_performed": false
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

最终将 W64-00 至 W64-13 的状态逐项标为 pass/fail/inconclusive，并由操作者和审阅者分别签名确认。只要 registry、签名、完整 coverage、高清图强绑定、Credential Manager 复用或隐私检查任一项未通过，整体不得标记为 pass，也不得把平台状态从 `build_only` 晋级。
