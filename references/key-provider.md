# 候选密钥导入格式

`v-local-cli` 自身不获取密钥；它只**消费**外部提供的候选，用于验证并读取用户本人拥有或已明确获授权的本地数据。候选有两个来源：

- 用户通过 `setup --keys <file>` 导入自己合法取得的候选文件；
- 或由用户单独安装的外部密钥组件在本机产生——仅当用户显式运行 `setup --allow-key-access` 时，主 CLI 才通过当前用户私有的认证 IPC 向其索取候选，并对响应做大小限制与格式、类型校验。该组件单独发布、自带安装器；缺失时 `setup --allow-key-access` 返回 `key_acquisition_component_missing`，并在提示中给出其安装命令，下载与校验由该安装器完成，主 CLI 不代为获取。

macOS 包含一个与主程序同目录的 companion helper。Provider 自动发现并调用它；用户仍只运行
`setup --allow-key-access`，不需要 helper 路径、管理员命令或中间候选文件。默认情况下，普通
启动被 `task_for_pid` 拒绝后，Provider 会检查 SIP。只有 development 构建在 SIP 已关闭时保留管理员授权兼容试验；签名发行版禁止 AppleScript 提权，只接受已签名 companion helper 的固定权限模型。macOS route 固定按 `standard -> shadow -> sip_disabled` 排序；当前 Shadow 路线未实现时
返回 `shadow_route_status=unavailable_in_build`，标准访问失败且 SIP 已验证后可以明确返回
`next_action=disable_sip`。它不会伪造 Shadow 运行失败，也不会自动修改系统安全设置。
CLI 只接受 `helper_status`、`process_access_status` 等无密钥状态枚举用于生成恢复提示。
如果 `process_access_status=process_list_unavailable`，表示 `ps` 与 `launchctl` 都无法枚举进程；CLI 会返回
`key_provider_process_list_unavailable`，不能把它解释成微信未运行。应在普通 macOS Terminal 中重试，或检查当前宿主环境的进程读取权限。

Apple Silicon（`arm64`）和 Intel（`x86_64`）的使用步骤相同，但动态 Hook 使用不同的 CPU ABI。
Provider 会读取实际微信进程架构：原生 arm64 微信使用 arm64 寄存器，Apple Silicon 上通过 Rosetta
运行的 x86_64 微信使用 x86_64 寄存器。安装器会安装匹配当前 macOS 架构的 Provider/helper；两种
自动获取密钥的路径按架构、微信版本和候选件摘要分别判断。仓库中的历史 Intel 记录不是签名发布证据；当前构建若没有绑定摘要的 live evidence，x86_64 与 arm64 都不能仅凭构建成功或 Markdown 声明升级为已验证。

候选 Provider 只会为精确 registry 条目输出 `registry_candidate_entry + registry_exact_match`，用于让受控真机 workflow 生成内容寻址 evidence；这不是发行授权。签名发行 Provider 只有在外部 promotion 已验证时才输出 `real_device_evidence_present + registry_exact_match + release_promotion_verified` 并启用精确 macOS standard route，发行 CLI 会拒绝缺 promotion 的候选标记。未知构建的 generic symbol route 仅存在于 development 构建。Windows 的固定 `Config.Cipher` 和通用内存 fallback 都必须由 eligible 同架构 registry 条目锚定；未登记签名者不是“安全回退”，而是拒绝进程内存访问。

Provider 默认由 CLI 启动 acquisition daemon，并按 `prepare -> observe -> finalize/cancel` 工作。endpoint token、只用于 opaque catalog ID 的机器 HMAC key 和 session resume 元数据位于当前桌面用户的私有缓存目录；resume 不包含候选、passphrase 或 catalog key。`prepare/observe` 对客户端无数据库或媒体 secret，只有重新核对 catalog 的 `finalize` 返回一次。单账号 session 硬上限为 15 分钟；catalog、账号目录、scope、provider 可执行文件或 daemon 实例变化时不会复用旧 session。macOS daemon 必须运行于同包 companion helper；helper daemon 不可用时安全回退 one-shot，不直接绕开 helper。

Provider v1 的诊断使用 `requested_scopes` 回显当前请求，以 `database_target_status` 区分“没有数据库目标”和“已有目标”，并分别以 `database_coverage_status`、`media_coverage_status` 表达两个 scope。未请求的 scope 必须是 `not_requested`；空 Catalog 永远不能成为 database complete，media-only 也不能把数据库标成 `complete`。`result_code` 是整体 RPC 结果的唯一权威字段，只有全部 requested scope 都为 `complete`、工作流 terminal、安全姿态无需恢复且没有待处理动作时才允许为 `complete`。CLI 会拒绝旧的裸 `coverage_status`/`media_status`、scope 回显不一致、未知稳定枚举以及 scope-qualified coverage 状态与已返回证明相矛盾的 v1 响应。

`shadow_route_status` 独立表达 Shadow 的能力和结果：`unavailable_in_build`、`unsupported_for_target` 与 `attempted_failed` 都是可降级终态，但含义不可互换；`not_evaluated`、`available`、`awaiting_approval` 不能进入 SIP fallback。macOS `route_priority` 必须精确为 `[standard, shadow, sip_disabled]`，但它只表达排序；`routes_attempted` 才记录真实执行，未实现的 Shadow 不能出现在其中。CLI 会拒绝状态、blocking reason、SIP 机器证据或顺序互相矛盾的响应。

4.1.x 微信使用动态 CommonCrypto 捕获时，如果 hook 已安装但尚未发生数据库调用，CLI 返回
`key_provider_hook_trigger_required`，错误详情包含不含凭据的 `session_id`、`catalog_id`、`process_instance_id`、`action_stage` 和 `next_action`。Agent 只执行对应的只读页面动作，然后在原 setup 参数上显式增加 `--confirm-key-action trigger_database`；CLI 只为这个精确动作提交 receipt。普通重跑、`--allow-key-access` 或旧 session 的确认参数都不能伪造 `user_confirmed=true`，也不应要求用户手工运行 lldb、helper 或填写候选值。

用户拒绝当前 `trigger_database`、`restart_wechat` 或 `relogin_wechat` 动作时，可以在原 setup 参数上显式传入 `--confirm-key-action stop_and_report`。该值不会生成 action receipt；Provider 只重新核对同一 catalog，终止 session，并返回此前已逐库 HMAC 验真的 partial。错账号、catalog drift 或没有有效候选时仍然 fail closed。

`key_provider_sip_required` 不是 daemon 可恢复动作，也不能从 `process_access_error=sip_enabled` 单独推导。只有标准访问失败、`security_posture_status=sip_enabled_verified`，且 Shadow 为 `unavailable_in_build`、`unsupported_for_target` 或 `attempted_failed` 并带匹配原因时，Provider 才能明确返回 `next_action=disable_sip` 并展示影响和恢复步骤。当前未实现 Shadow 属于第一种终态，不再阻塞 fallback。CLI/Provider 不执行 `csrutil disable/enable`，不把 `--allow-key-access` 当成 SIP 同意，也不接受 `disable_sip`/`reenable_sip` daemon receipt。CLI 保存的跨重启 checkpoint 只含 opaque workflow/account/provider ID、scope、阶段和安全姿态，不含路径、session、token、候选或授权；`status` 可发现它。重启后不带旧 `--confirm-key-action` 创建新 session，并用只读机器证据、新进程实例、版本/架构/签名、账号绑定和 Catalog 重新判定。只要确认 SIP 已关闭，Provider 就必须返回 `security_restoration_required/reenable_sip`：完整结果可以先发布；若实际 fallback route 已运行但失败，附带 `sip_route_failed`；若前置检查先失败，附带 `sip_disabled_route_not_attempted` 且不得伪造执行历史。恢复后由独立的 `revalidate_security_posture` 只读请求验证 `sip_enabled_verified`，不重新获取 credential。若用户根本没有执行关闭操作，后续普通 acquisition 在 SIP 开启状态下完整成功也会清除失效 checkpoint。

密钥如何取得**不在本仓库范围内**，由用户自行决定并承担相应合规责任。`refresh` 不需要任何外部组件或重新取证：它读取系统凭据库中的结构化 credential。已证明的账号级 passphrase 会为新 salt 自动派生逐库 effective key；只有根凭据无法覆盖新数据库、credential epoch 改变、账号绑定冲突或账号目录改变时，才回到 `setup`。

## 候选文件结构

`--keys` 接受的候选文件使用如下结构。数据库密钥为 64 位十六进制；图片密钥由 16 字节 ASCII 的 AES 候选与单字节 XOR 组成：

```json
{
  "database_keys": {
    "contact/contact.db": "64 位十六进制候选",
    "*": "也可提供适用于全部数据库的全局候选"
  },
  "image_keys": {
    "aes": "16 字节 ASCII 候选",
    "xor": 245
  }
}
```

候选文件只接受上面的 `database_keys` 与 `image_keys` 字段，并且只能包含一个 JSON 对象；
Provider 专属的 protocol、catalog、diagnostics 与 `database_credential` provenance 不得由
用户候选文件声明。CLI 会把验真的原始候选归一化为当前 generation 的逐库 key 后再保存。

Provider v1 还可以返回独立的 `database_credential`。其中 `global_passphrase` 是账号级根凭据，`overrides` 是少数数据库的 raw `enc_key`；`database_keys` 始终只是当前 catalog 已派生、已通过首页 HMAC 的逐库 effective key，不再兼作根凭据容器。只有动态 KDF 调用参数证据完整，且同一 passphrase 在同一 profile 下至少跨两个不同 salt 的目标库逐文件通过 HMAC，Provider 才把它提升为账号级根凭据；静态内存探测、不同 passphrase、跨 profile 拼接证据、单库/单 salt 命中只保存当前 effective key override。CLI 只在当前 catalog 完整验真时保存根凭据；partial generation 会移除根 passphrase，并把每个已验证的 effective key 保存成可供 refresh 使用的结构化逐库 override。

回归必须同时执行自动化测试和平台真机清单。macOS 使用 [macOS 真机验收](macos-acceptance.md)，Windows 使用 [Windows 密钥获取真机与发布回归](windows-key-provider-acceptance.md)；普通 CI、交叉编译或 mock Provider 不得替代真实架构、SIP、签名和系统凭据库证据。

Windows 响应只有在目标进程实际架构、可执行文件 SHA-256、Authenticode 签名者证书
SHA-256、版本/build 与产品身份都和带真机证据的 registry 条目精确匹配时，才可把
`config_cipher_route_status` 提升到 `eligible_registered` 或尝试态。CLI 会拒绝伪造/矛盾的
fingerprint、route、账户分类和计数，并要求 `windows_config_cipher` 先于 missing-only
`windows_memory_fallback`；fallback 的签名者也必须由至少一个完整的同架构 registry 条目锚定。任何静态 passphrase 即使覆盖多个 salt，也只能保存逐库
effective override，不能提升为账号级根凭据。Windows 逐库 override 可携带 opaque
`process_instance_ids`；CLI 只接受绑定启动时间、规范可执行路径、实际架构、签名身份、
二进制摘要和产品身份后生成的 `windows-process:<sha256>`，拒绝 PID-only provenance。

- `database_keys` 的键是相对数据库路径；`*` 表示对所有数据库尝试同一候选。
- `image_keys.aes` 为 16 字节 ASCII，`image_keys.xor` 为 0–255 的单字节整数。
- 完整初始化应同时包含 `database_keys` 和 `image_keys`，并用严格模式一次性验证和保存两类密钥：
  `v-local-cli setup --allow-key-access --storage keychain`（导入文件时将
  `--allow-key-access` 换成 `--keys <file>`）。
- 默认 setup 就要求图片候选通过 DAT 验证；缺少图片候选时会失败，不会悄悄只保存数据库密钥。
- 只有明确的纯文本任务才使用 `--database-only`，例如 `v-local-cli setup --allow-key-access --storage keychain --database-only`；该模式向 Provider 只请求 `database` scope，并且不会保存图片候选。

## 验证与边界

主 CLI 对候选**独立验证**，不信任来源自报的正确性：Provider v1 必须同时返回 `catalog_entries`，逐库携带相对路径、平台文件身份、大小、mtime、首页摘要、classification 和 profile。CLI 拒绝链接/reparse point、大小写或 Unicode 归一化碰撞，并在主库稳定复制前后及 WAL 复制后重新核对文件身份和首页摘要；任何漂移都会取消本次 generation。数据库候选还必须同时通过页头解密和首页 HMAC；图片候选用真实 DAT 样本与容器检查。generation manifest 保存 `catalog_id`、源首页摘要与选中的 profile，但不保存 secret。默认 setup 把数据库和图片候选作为一个整体处理，任一 scope 未验证就不发布或保存；使用 `--storage keychain` 的完整 setup 成功时应检查 `status=ready`、`media.status=verified`，并按 catalog 是否含加密数据库检查 `database_keys_persisted`，同时确认 `image_keys_persisted=true`。只有显式 `--database-only` 才允许不验证图片；该模式用 keychain 时输出 `database_only=true`、`image_keys_persisted=false`。`snapshot-only` 即使完成图片验证也不会保存任何密钥。旧的 `--require-media` 仍作为兼容性的显式严格标志接受。候选不出现在命令行参数中；从外部组件读入的 stdout 只在进程内使用，不写入日志、不交给 Agent。

开发/候选构建可用 `--provider <明确路径>` 或 `V_LOCAL_CLI_KEY_PROVIDER` 指向隔离测试组件；CLI 会先把路径解析为没有 symlink/junction/reparse 祖先的绝对普通文件，并把子进程工作目录固定到组件目录。没有显式路径时，candidate CLI 会优先解析 Provider npm 安装器维护的当前用户固定目录，并把完整性报告为 `candidate_unverified`，而不是平台签名可信。Signed release 不接受这些 override 或 PATH 同名文件，只解析同一固定目录：首发 Windows `%LOCALAPPDATA%\v-local\key-provider\windows-amd64\v-local-key-provider.exe`，macOS `~/Library/Application Support/v-local/key-provider/darwin-<arch>/v-local-key-provider`。Windows 运行时使用不联网的 WinVerifyTrust，并要求 CLI、Provider 和 daemon 实际进程匹配编译期绑定的 Authenticode 叶证书 SHA-256；macOS 要求 CLI/Provider/helper 的固定 identifier、Developer ID、同一 Team ID、可信 owner/权限/canonical sibling，并核对 daemon PID image。任何一项不符都返回 `key_acquisition_component_untrusted`，不会回退 one-shot 绕过。

Provider 与 CLI 在进入密钥流程前还必须启用 crash artifact 防护：Unix 把 core dump hard limit 设为零；Windows 通过 WER `NOHEAP` 禁止堆采集，并对持有请求/响应的可清理 byte buffer登记 excluded memory。获取请求、响应和 helper 提权交换只走 stdin/stdout 或带 256-bit token 的当前用户私有 loopback IPC，不把 key 写入 endpoint、resume 或普通临时文件。

把外部密钥组件与主 CLI 拆分的收益是最小权限、独立审计、可选安装与故障隔离，并不保证避免任何平台投诉或法律程序。
