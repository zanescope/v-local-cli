# 排错

| 错误类型 | 处理 |
|---|---|
| `no_accounts` | 请重新登录微信/打开新消息后重试；自定义目录可设置 `V_LOCAL_CLI_DATA_ROOT` 或 `V_LOCAL_CLI_ACCOUNT_DIR`。 |
| `no_account` | 重新检查账号名称或唯一子串，不要回退到其他账号。 |
| `need_account` | 运行 `v-local-cli accounts`，再用 `--account` 明确选择。 |
| `not_initialized` | 先运行 `v-local-cli setup --dry-run`。 |
| `invalid_date` | 使用严格的 `YYYY-MM-DD` 本地日期。 |
| `invalid_time_window` | 确保开始日期不晚于结束日期。 |
| `conflicting_time_window` | `--all` 与 `--start/--end` 不能同时使用。 |
| `missing_command`、`unknown_command`、`invalid_arguments` | 运行 `v-local-cli --help` 或 `v-local-cli schema <command>`，按机器契约修正命令和选项顺序。 |
| `key_access_not_authorized` | 只有用户明确同意后才能加 `--allow-key-access`。 |
| `key_provider_failed` | 运行 `v-local-cli provider status`；确认 Provider 是单独安装的正确平台版本。重新登录微信或打开新消息后再试。 |
| `key_provider_helper_missing` | 运行一次 `npx @zanescope/v-local-key-provider@latest install`；macOS 安装器会同时配置 helper，无需手工运行或传路径。 |
| `key_provider_helper_failed` | 重新运行同一个 Provider 安装命令以恢复主程序与 helper 的匹配版本，再重试 setup。 |
| `key_provider_process_list_unavailable` | 当前环境无法枚举 macOS 进程；请在普通 macOS Terminal 中重试，或确认环境允许读取进程列表。这不等同于微信未运行。 |
| `key_provider_permission_denied` | 已安装 helper，但 macOS 仍拒绝读取微信进程。保持微信登录并重试；Provider 会自动尝试管理员授权，不要手工运行 helper、改签微信或注入进程。 |
| `key_provider_sip_required` | SIP 仍开启。无 Developer ID 的兼容模式只有在用户明确接受风险后，才能在恢复模式临时关闭 SIP；不希望更改系统安全设置时，改用 `--keys FILE` 导入已取得的候选。 |
| `key_provider_hook_trigger_required` | 当前数据库已经打开，普通切换会话不一定重新创建加密上下文。先完全退出微信并启动下一次 setup；保持终端窗口运行，看到命令尚未返回提示符时从“应用程序”重新打开微信并完成账号登录；不需要手工运行 helper 或 lldb。 |
| `key_provider_hook_restart_required` | 当前微信数据库已在进程启动阶段打开。先完全退出微信并启动下一次 setup；保持终端窗口运行，看到命令尚未返回提示符时从“应用程序”重新打开微信并完成账号登录。连续两次仍失败时停止自动重试并报告诊断。 |
| `wechat_not_running` | 启动并登录微信，然后重新运行同一条 setup 命令。 |
| `key_provider_timeout`、`key_provider_no_candidates` | 保持微信登录，打开一条新消息后重试；不要把未命中解释为密钥无效。 |
| `refresh_credentials_unavailable` | 在最初 setup 的同一桌面用户身份下重试；仍不可用时用 `--storage keychain` 重新 setup。 |
| `refresh_account_unavailable` | 运行 `v-local-cli accounts` 检查原账号目录；重新登录微信或打开新消息后重试，不要自动改用同名账号。 |
| `snapshot_busy` | 等待同账号当前 setup/refresh 结束后重试；操作系统会在进程退出时释放锁状态，不要删除锁文件。 |
| `snapshot_coverage_regression` | 当前快照未被替换。稍后重试；若源数据库确已删除且明确接受范围缩小，再重新 setup 并增加 `--allow-coverage-regression`。 |
| `fresh_failed` | 本次查询没有取得新快照；先单独运行 `refresh` 并按其错误恢复，不要把旧结果称为最新数据。 |
| `state_recovery_failed`、`state_commit_failed` | 上一代仍应保留。先运行 `v-local-cli doctor`，不要直接修改状态文件；需要交给维护者时使用 `doctor --bundle FILE`。只有用户确认放弃现有 v-local-cli 数据时才走 `forget --dry-run` 与确认删除流程。 |
| `confirmation_required` | 先运行对应的 `--dry-run` 查看删除范围，取得用户明确确认后再执行。 |
| `keychain_delete_failed`、`account_data_delete_failed` | 不要声称已完全删除；保持同一桌面用户身份，运行 `doctor` 后重试。 |
| `database_key_rejected` | 候选没有通过 SQLCipher 首页或 WAL 验证；重新取得候选，不要手工修改数据库。 |
| `invalid_key_bundle` | 不打印文件内容；让用户确认文件来源和协议格式。 |
| `media_too_large` | 不绕过 64 MiB 上限；让用户提供更小或正确的输入文件。 |
| `media_decrypt_failed` | 核对账号、输入 DAT 和图片候选是否匹配，再重新 setup。 |
| `media_key_unverified` | 默认 setup，以及带 `--require-media` 的媒体刷新，要求图片候选通过真实 DAT 验证；先打开一条含图片的新消息后重试。若当前确实只处理文本，重新 setup 时显式增加 `--database-only`。 |
| `image_keys_persisted=false` | `snapshot-only` 会有意不保存任何密钥；在 `keychain` 模式下只有显式 `--database-only` 才允许图片密钥缺失。若 keychain 命令未带该标志却出现此结果，应重新运行默认的 `setup --allow-key-access --storage keychain`。 |
| `need_media_keys` | setup 使用了 `snapshot-only`，或系统凭据库写入失败；用 `--storage keychain` 重新 setup。 |
| `voice_dependency_required` | 微信已有转写索引和 v-local-cli 私有暂存均缺少部分文字。先询问用户是否安装本地 whisper.cpp 与多语言模型，或独立 `v-local-cli-sensevoice` 适配器与 SenseVoice 模型；同意后只从各自官方发布源安装并校验摘要，可配置 `V_LOCAL_CLI_WHISPER_BIN`/`V_LOCAL_CLI_WHISPER_MODEL` 或 `V_LOCAL_CLI_ASR_PROVIDER`/`V_LOCAL_CLI_ASR_MODEL`。不同意则用 `voice-search --cached-only`，不要改用在线 ASR。 |
| `voice_evidence_unavailable` | 重新从当前快照的 `history` 取得 `kind=voice` 的 `evidence_id`；确认快照包含相应 `media_*.db`，不要按时间或文件名猜测语音。 |
| `voice_transcription_failed` | 用 `voice-status` 检查 whisper.cpp 或 `v-local-cli-asr/1` 适配器及其模型；whisper.cpp 模型不能是 `.en` 英文专用版本，SenseVoice 模型目录必须含 `model.int8.onnx` 和 `tokens.txt`。失败的临时 WAV 会清理，不要把原始语音写到项目目录排错。 |
| `voice_model_language_mismatch` | 中文或混合语音使用非 `.en` 的多语言 whisper.cpp 模型；仅英文语音才使用 `--language en`。 |
| `ocr_text_not_cached` | 微信兼容索引和 v-local-cli 私有缓存都没有该图片文字；对具体 `image_evidence_id` 运行 `ocr-recognize`，取得本次私有 IPC 明确授权后再增加 `--allow-private-ipc`。不要把零结果解释为图片没有文字。 |
| `image_evidence_unavailable` | 重新从当前快照的 `history` 取得 `kind=image` 的 `evidence_id`，不要按文件名或时间猜测。 |
| `ocr_input_invalid` | 只选择 64 MiB 内、结构验证通过的普通 JPEG、PNG 或 GIF 文件；不要改扩展名绕过。 |
| `chat_image_unavailable` | 先在微信打开该图片并运行 `refresh`；确认 setup 已保存图片密钥。CLI 只接受消息资源标识与 hardlink 映射共同指向且完整解码通过的本地图片。 |
| `ocr_temporary_cleanup_failed` | OCR 已返回但临时明文图片未能删除；停止处理其他图片并运行 `doctor` 检查账号私有临时目录。 |
| `wechat_native_ocr_authorization_required` | 说明这次会启动已安装微信的私有 OCR 子进程、能力与微信版本耦合；只在用户对这一个本地图片明确同意后增加 `--allow-private-ipc`。 |
| `wechat_native_ocr_unavailable` | 用 `ocr-status` 检查平台和已安装微信组件；该实验后端只支持 Windows amd64。不要从非微信安装目录下载或补齐 DLL/模型。 |
| `wechat_native_ocr_failed` | 当前微信版本可能不兼容实验协议。确认临时文件清理状态；不要改用在线 OCR、保留临时组件或绕过每次授权。仍可使用 `ocr-read`/`ocr-search` 读取兼容索引和私有缓存。 |
| `official_article_network_authorization_required` | 说明本次只把这一篇的公开文章标识发送到 `mp.weixin.qq.com`；取得明确同意后，对同一 `publication:` 证据增加 `--allow-network`。 |
| `official_article_evidence_invalid`、`official_article_evidence_unavailable` | 重新从当前快照的 `official-history` 或 `official-search` 取得 `publication:` 证据；不要直接传 URL 或复用旧版本标识。 |
| `official_article_url_rejected`、`official_article_redirect_rejected` | 保持拒绝；不要放宽目标域、公开文章端点或重定向限制。 |
| `official_article_body_unavailable` | 文章可能删除、要求验证或只返回提示页。CLI 不读取 Cookie，也不会把提示页当正文；保留本地卡片元数据即可。 |
| `official_article_request_failed` | 检查网络与系统 DNS 后重试同一证据。公众号正文不使用外部 DNS 回退；TUN fake-IP 环境需让系统为 `mp.weixin.qq.com` 返回真实公网地址，不要关闭目标域、公网地址或重定向限制。 |
| `official_article_rate_limited`、`official_article_http_status`、`official_article_authorization_rejected` | 稍后按同一证据重试；不要并发抓取，也不要引入 Cookie、浏览器会话或通用下载器。 |
| `official_article_response_size_invalid`、`official_article_content_type_rejected`、`official_article_html_invalid` | 响应未通过 8 MiB、HTML 或结构检查；不要保存或分析为正文。 |
| `invalid_output` | `--output` 必须指向普通文件或尚不存在的路径，不能是目录、符号链接、Windows 重解析点或特殊文件；更换目标路径，不要自动扩大覆盖范围。 |
| `output_exists` | 更换新路径；只有用户明确要求覆盖时才使用 `--force`。 |
| `moment_media_network_authorization_required` | 暂停并说明临时令牌将发往该媒体的腾讯 CDN；取得本次明确同意后才增加 `--allow-network`。 |
| `moment_media_not_found` | 重新运行当前版本的朋友圈查询，使用同一快照返回的 `media.evidence_id`。 |
| `moment_media_identity_conflict`、`moment_media_ambiguous` | 刷新快照后重新取证；不要猜测归属。 |
| `moment_media_remote_descriptor_missing` | 刷新本地快照；不要补造 URL、令牌或密钥。 |
| `moment_media_remote_url_rejected` | 保持拒绝；不要改用通用下载器绕过域名或 SSRF 限制。 |
| `moment_media_download_failed` | 令牌可能已过期；刷新本地快照后重新获取 `media.evidence_id` 再试，不要改用通用下载器。 |
| `moment_media_verify_failed` | 媒体没有通过容器或摘要验真，没有生成输出文件；刷新本地快照后重试，不要打开失败负载。 |
| `moment_media_kind_unsupported` | 当前只支持普通图片和普通视频；不要把实况照片等未绑定描述符猜成可导出媒体。 |
| `moment_media_download_failed_authorization_rejected`、`moment_media_download_failed_resource_unavailable` | CDN 令牌或资源可能已失效；刷新快照并重新取得媒体证据后，再重新请求单次联网授权。 |
| `moment_media_download_failed_dns_failed`、`moment_media_download_failed_connection_failed`、`moment_media_download_failed_request_failed`、`moment_media_download_failed_direct_dns_failed` | 检查 DNS 和网络后重试；不要启用环境代理、关闭 TLS 验证或把令牌交给其他下载器。 |
| `moment_media_download_failed_non_public_address`、`moment_media_download_failed_invalid_address`、`moment_media_download_failed_request_build_failed`、`moment_media_download_failed_synthetic_proxy_address` | 目标未通过公网地址或请求构造检查；保持拒绝，不要绕过 SSRF 与 fake-IP 防线。 |
| `moment_media_download_failed_redirect_rejected`、`moment_media_download_failed_rate_limited`、`moment_media_download_failed_http_status` | 不跟随跳转；稍后用当前证据重试，持续失败时刷新快照。 |
| `moment_media_download_failed_response_read_failed`、`moment_media_download_failed_response_size_invalid` | 响应读取失败、为空或超过上限；没有可信输出，不要保留临时文件。 |
| `moment_media_verify_failed_container`、`moment_media_verify_failed_payload_size`、`moment_media_local_unavailable` | 本地或远端媒体未通过大小、解密或容器验证；刷新快照后重试，不要打开失败负载。 |
| `moment_media_export_failed` | 没有生成可信输出；运行 `doctor` 后重试，不要改用通用下载器绕过证据和目标限制。 |
| `skill_bundle_unavailable`、`skill_install_failed` | 使用官方 npm 包重新执行安装，并检查当前用户对 Skill 目录的写入权限；不要从未知目录复制 Skill。 |
| `ambiguous_contact` | 展示 `details.candidates` 中最少必要的显示名、类型和稳定 username，让用户明确选择；不要自动取第一项。 |
| `contact_not_found` | 运行 `v-local-cli contacts` 或 `v-local-cli resolve-contact` 检查名称、备注、昵称、别名和稳定 username。 |
| `not_group_chat` | 当前解析结果不是 `@chatroom`；重新选择群聊，不要把普通联系人当作群成员源。 |
| `index_build_failed` | 当前 generation 的派生索引未成功发布；运行 `v-local-cli index status` 检查绑定与 coverage，再重试 `index build`。快照本身仍保持不可变。 |
| `index_required` | `new-messages` 需要 base/target generation 的完整消息索引；先修复索引覆盖问题，不要用不完整扫描推进游标。 |
| `cursor_not_found` | 指定 consumer 尚未创建；去掉 `--status` 进行首次 poll，并明确选择 `--start now` 或 `--start beginning`。 |
| `cursor_create_failed`、`cursor_poll_failed` | 检查账号私有目录权限、索引状态和同账号操作锁；不要删除或手改游标 JSON。 |
| `cursor_ack_failed` | 只确认当前 pending 响应中的完整 `batch_id`；重新 poll 会重放同一批，不要猜测或跳过批次。 |
| `cursor_delete_failed` | 先检查 consumer 名称和私有目录权限；只有用户确认重置进度时才重试 `--delete --yes`。 |
| `daemon_unavailable` | 在同一桌面用户的独立终端运行 `v-local-cli daemon serve`，再检查 `daemon status`；不要连接非 loopback endpoint。 |
| `daemon_request_failed` | 请求未通过 token、协议、大小、deadline 或 immutable 查询白名单；先直接运行同一只读查询定位参数，不要放宽 daemon 权限。 |
| `daemon_serve_failed` | 检查当前用户私有状态目录与 loopback 监听能力；不要改为公网或局域网地址。 |
| `internal_error` | 停止扩大操作范围，生成脱敏诊断包交给维护者；不要自动归因于数据库损坏。 |

系统凭据按桌面用户身份隔离。若 CLI 在不同用户、服务或沙箱身份下运行，`refresh` 可能看不到原身份保存的凭据；不要把密钥复制到项目文件来绕过隔离，应切回同一桌面用户身份，或重新 setup。

`setup --dry-run` 永远不会启动 Provider。若它的输出显示 `available=false`，这不是数据库损坏，只表示自动候选获取不可用。
