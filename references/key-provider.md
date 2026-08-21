# 候选密钥导入格式

`v-local-cli` 自身不获取密钥；它只**消费**外部提供的候选，用于验证并读取用户本人拥有或已明确获授权的本地数据。候选有两个来源：

- 用户通过 `setup --keys <file>` 导入自己合法取得的候选文件；
- 或由用户单独安装的外部密钥组件在本机产生——仅当用户显式运行 `setup --allow-key-access` 时，主 CLI 才通过 stdin/stdout 向其索取候选，并对响应做大小限制与格式、类型校验。该组件单独发布、自带安装器；缺失时 `setup --allow-key-access` 返回 `key_acquisition_component_missing`，并在提示中给出其安装命令，下载与校验由该安装器完成，主 CLI 不代为获取。

macOS 包含一个与主程序同目录的 companion helper。Provider 自动发现并调用它；用户仍只运行
`setup --allow-key-access`，不需要 helper 路径、管理员命令或中间候选文件。默认情况下，普通
启动被 `task_for_pid` 拒绝后，Provider 会检查 SIP，并在 SIP 已关闭时弹出系统管理员授权后
自动重试；SIP 开启时返回 `key_provider_sip_required`，不会自动修改系统安全设置。
CLI 只接受 `helper_status`、`process_access_status` 等无密钥状态枚举用于生成恢复提示。
如果 `process_access_status=process_list_unavailable`，表示 `ps` 与 `launchctl` 都无法枚举进程；CLI 会返回
`key_provider_process_list_unavailable`，不能把它解释成微信未运行。应在普通 macOS Terminal 中重试，或检查当前宿主环境的进程读取权限。

Apple Silicon（`arm64`）和 Intel（`x86_64`）的使用步骤相同，但动态 Hook 使用不同的 CPU ABI。
Provider 会读取实际微信进程架构：原生 arm64 微信使用 arm64 寄存器，Apple Silicon 上通过 Rosetta
运行的 x86_64 微信使用 x86_64 寄存器。安装器会安装匹配当前 macOS 架构的 Provider/helper；两种
自动获取密钥的路径按架构分别判断：x86_64 已在 Intel 真机上验证，arm64 仍属于实验性 `build_only`，不能仅凭构建成功就说它已验证。

4.1.x 微信使用动态 CommonCrypto 捕获时，如果断点已安装但尚未发生数据库调用，CLI 返回
`key_provider_hook_trigger_required`。Agent 应先完全退出微信，再启动下一次 setup；提示用户保持 setup 终端窗口运行，
看到命令尚未返回提示符时从“应用程序”重新打开微信并完成账号登录；普通切换一次会话后重试；不应要求用户手工运行 lldb、helper 或填写候选值。

当错误为 `key_provider_sip_required`，Agent 必须告诉用户 macOS SIP 仍开启，并说明需要在恢复
模式临时关闭 SIP（执行 `csrutil disable`）；回到桌面后先启动本次授权操作，保持 setup 终端窗口运行，
看到命令尚未返回提示符时，从“应用程序”打开微信并完成登录。CLI/Provider 不会自动启动、退出或重启微信。成功获取候选并确认 setup
完成后，用户必须再次进入恢复模式恢复保护：Apple 芯片 Mac 关机后长按电源键，选择“选项” > “继续”；
Intel Mac 重启时按住 `Command-R`。在恢复环境顶部菜单选择“实用工具” > “终端”，执行
`csrutil enable`，再执行 `csrutil status` 确认状态为 `enabled`，然后执行 `reboot`。回到桌面后
再次确认 `csrutil status` 为 `enabled`。用户完成恢复模式操作前，不要自动重试、要求手工运行 helper，
或要求用户提供私钥、密码和候选值。

密钥如何取得**不在本仓库范围内**，由用户自行决定并承担相应合规责任。`refresh` 不需要任何外部组件或重新取证：它只读取系统凭据库中由 `setup` 先前验证并最小化保存的密钥，重新发布同一账号的快照。只有凭据缺失、账号目录改变，或出现没有已保存密钥的新数据库分片时，才需回到 `setup` 流程。

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

- `database_keys` 的键是相对数据库路径；`*` 表示对所有数据库尝试同一候选。
- `image_keys.aes` 为 16 字节 ASCII，`image_keys.xor` 为 0–255 的单字节整数。
- 完整初始化应同时包含 `database_keys` 和 `image_keys`，并用严格模式一次性验证和保存两类密钥：
  `v-local-cli setup --allow-key-access --storage keychain`（导入文件时将
  `--allow-key-access` 换成 `--keys <file>`）。
- 默认 setup 就要求图片候选通过 DAT 验证；缺少图片候选时会失败，不会悄悄只保存数据库密钥。
- 只有明确的纯文本任务才使用 `--database-only`，例如 `v-local-cli setup --allow-key-access --storage keychain --database-only`；该模式向 Provider 只请求 `database` scope，并且不会保存图片候选。

## 验证与边界

主 CLI 对候选**独立验证**，不信任来源自报的正确性：数据库候选用 SQLCipher 首页校验，图片候选用真实 DAT 样本与容器检查。默认 setup 把数据库和图片候选作为一个整体处理，任一 scope 未验证就不发布或保存；使用 `--storage keychain` 的完整 setup 成功时应检查 `status=ready`、`media.status=verified`、`database_keys_persisted=true` 和 `image_keys_persisted=true`。只有显式 `--database-only` 才允许不验证图片；该模式用 keychain 时输出 `database_only=true`、`image_keys_persisted=false`。`snapshot-only` 即使完成图片验证也不会保存任何密钥。旧的 `--require-media` 仍作为兼容性的显式严格标志接受。候选不出现在命令行参数中；从外部组件读入的 stdout 只在进程内使用，不写入日志、不交给 Agent。

开发构建可用 `--provider <明确路径>` 或 `V_LOCAL_CLI_KEY_PROVIDER` 指向已安装的外部组件；正式发布还应在安装层验证签名与校验和。CLI 在启动前把该路径解析为绝对普通文件，并把子进程工作目录固定到其自身目录，避免从调用方工作区解析相对依赖。

把外部密钥组件与主 CLI 拆分的收益是最小权限、独立审计、可选安装与故障隔离，并不保证避免任何平台投诉或法律程序。
