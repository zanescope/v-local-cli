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
| `missing_command`、`unknown_command`、`invalid_arguments` | 运行 `v-local-cli --help` 或 `v-local-cli schema <command>`，按机器契约修正命令和选项顺序。查询的 `--all` 只控制日期范围；条数只用 `--limit`，其中 `--limit 0` 表示无上限。 |
| `key_access_not_authorized` | 只有用户明确同意后才能加 `--allow-key-access`。 |
| `private_state_unavailable` | 检查当前用户缓存目录的所有者、ACL 与剩余空间；不要把 acquisition endpoint 或 resume 文件移到共享目录。 |
| `private_output_invalid` | 只在本机受控验收中设置 `V_LOCAL_CLI_PRIVATE_OUTPUT_PATH`；使用既有、非重解析点私有目录中的绝对新文件路径，不要覆盖现有文件。 |
| `private_output_unavailable` | 检查私有输出父目录的当前用户写权限与剩余空间，换用新的文件名后重试；不要改写系统安全策略或降级到共享目录。 |
| `crash_protection_unavailable` | 停止密钥获取；确认当前进程允许把 Unix `RLIMIT_CORE` 降为 0，或检查 Windows crash reporting/应用控制策略。不得在 crash artifact 防护失败时继续接收 Provider secret。 |
| `external_workflow_state_invalid` | 跨重启 checkpoint 已损坏、过期或包含协议不允许的字段。它不是授权凭据；停止自动继续。账号仍可选时用 `setup --cancel-acquisition --account NAME` 精确清理；账号已不可选或记录无法识别时，取得用户对“仅清理全部跨重启 checkpoint”的明确确认后运行 `setup --cancel-all-external-workflows`。该命令保留 daemon resume、快照和凭据。`doctor` 同样会 fail-closed，不能修复该记录。 |
| `external_workflow_cleanup_failed` | 全局 checkpoint 清理没有完成；不要手工递归删除私有状态目录。检查当前用户对私有 acquisition 目录的权限与剩余空间后重试；快照、凭据和 daemon resume 均不在该命令删除范围内。 |
| `key_provider_failed` | 运行 `v-local-cli provider status`；确认 Provider 是单独安装的正确平台版本。重新登录微信或打开新消息后再试。 |
| `provider status` 显示 `candidate_unverified` | 当前是明确选择的 unsigned early-access；固定安装路径和包内摘要不等于 Authenticode/Developer ID。只在接受候选通道风险时继续，不得据此声称 signed release 或 `latest` 信任。 |
| `key_acquisition_component_untrusted` | 重新运行官方 `@zanescope/v-local-key-provider` 安装器并检查 `provider status` 的 `integrity`。发行构建只接受当前用户固定安装目录中的签名 Provider/helper，不接受 `--provider`、`V_LOCAL_CLI_KEY_PROVIDER`、PATH 同名替代或重签文件；需要自备候选时改用 `setup --keys FILE`。 |
| `key_provider_cancel_failed` | 检查当前用户私有状态目录后重试 `setup --cancel-acquisition --account NAME`；取消只清理 session，不删除已发布 generation。 |
| `key_provider_helper_missing` | 运行一次 `npx @zanescope/v-local-key-provider@latest install`；macOS 安装器会同时配置 helper，无需手工运行或传路径。 |
| `key_provider_helper_failed` | 重新运行同一个 Provider 安装命令以恢复主程序与 helper 的匹配版本，再重试 setup。 |
| `key_provider_process_list_unavailable` | 当前环境无法枚举 macOS 进程；请在普通 macOS Terminal 中重试，或确认环境允许读取进程列表。这不等同于微信未运行。 |
| `key_provider_permission_denied` | 已安装 helper，但 macOS 仍拒绝读取微信进程。保持微信登录并重试；Provider 会自动尝试管理员授权，不要手工运行 helper、改签微信或注入进程。 |
| `key_provider_shadow_approval_required` | Shadow 不属于同一 daemon session 的动作。阅读风险并由用户手工完成准备；之后不带旧 `--confirm-key-action` 重新运行 `setup --allow-key-access`，让新 session 复验二进制、进程、账号和 Catalog。 |
| `key_provider_unsupported` 且 `process_access_error=sip_enabled` | SIP 未经系统证据验证，或 Shadow 仍为 `not_evaluated/available/awaiting_approval`；停止并报告，不得自行升级。 |
| `key_provider_sip_required` | 仅在 Provider 明确返回 `next_action=disable_sip`，且 Shadow 为 `unavailable_in_build`、`unsupported_for_target` 或 `attempted_failed` 并带匹配原因时处理。它是可拒绝的低优先级 fallback，不是当前 daemon 的可恢复动作；结束旧 session，确认无权限 checkpoint 已持久化，完成系统操作和重启后，从新的 `setup --allow-key-access` session 开始，不传旧 `--confirm-key-action`。 |
| `key_provider_sip_restoration_required` | 本次流程曾在 SIP-disabled 状态运行，整体工作流尚未完成。立即在恢复环境开启 SIP 并重启；随后运行 `status`，再用不带旧确认参数的新 `setup --allow-key-access` 让 Provider 以系统证据确认恢复。 |
| `key_provider_hook_trigger_required` | 按 `next_action` 完成只读页面动作，再在原 setup 参数上增加 `--confirm-key-action trigger_database`。普通重跑不会生成回执；拒绝动作但要保留已验真的 partial 时改传 `stop_and_report`。 |
| `key_provider_hook_restart_required` | 保存前台工作、确认影响并只重启绑定进程，再增加 `--confirm-key-action restart_wechat`。Provider 未观测到新进程实例时会拒绝回执；拒绝动作但要保留已验真的 partial 时改传 `stop_and_report`。 |
| `key_provider_action_confirmation_mismatch` | 确认参数与当前 `next_action`、session、route 或进程实例不一致；删除旧确认参数，重新读取当前错误详情。不得用该参数确认 Shadow/SIP。 |
| `key_provider_catalog_drift` | Provider 验证后数据库集合、文件身份或首屏内容发生了变化；本次候选已拒绝发布。保持目标账号不变并重新运行原 setup/refresh，让 Provider 针对新 catalog 重新获取和验证；不要复用旧确认参数或旧候选。 |
| `wechat_not_running` | 启动并登录微信，然后重新运行同一条 setup 命令。 |
| `key_provider_timeout`、`key_provider_no_candidates` | 保持微信登录，打开一条新消息后重试；不要把未命中解释为密钥无效。 |
| `key_provider_account_mismatch` | 当前会话与目标数据账号冲突；切换到目标账号后重试，禁止保存或跨进程合并本次候选。 |
| `key_provider_relogin_required` | 仅在 Provider 给出登录阶段机器证据时，说明扫码/MFA 影响并由用户确认重新登录；不得作为默认首次动作。拒绝重登但要保留已验真的 partial 时传 `--confirm-key-action stop_and_report`。 |
| `key_provider_ambiguous` | 停止自动选择和盲目重试，保留版本、route、catalog 与候选计数诊断供复核。 |
| `key_provider_validator_conflict` | 同一文件/profile 有多个不同 key 通过 HMAC，按验证器或文件漂移故障处理；停止重试和发布，保留脱敏诊断并复核 profile 与首页 HMAC 实现。 |
| `key_provider_unsupported` | 当前版本/架构/指纹无受支持 route；不要自动降级微信或扩大扫描范围。 |
| `refresh_credentials_unavailable` | 在最初 setup 的同一桌面用户身份下重试；仍不可用时用 `--storage keychain` 重新 setup。 |
| `refresh_account_unavailable` | 运行 `v-local-cli accounts` 检查原账号目录；重新登录微信或打开新消息后重试，不要自动改用同名账号。 |
| `snapshot_busy` | 等待同账号当前 setup、refresh 或聊天图片恢复等快照绑定事务结束后重试；操作系统会在进程退出时释放锁状态，不要删除锁文件。聊天图片 challenge 尚未消费时可在事务结束后原样重试。 |
| `snapshot_coverage_regression` | 当前快照未被替换。稍后重试；若源数据库确已删除且明确接受范围缩小，再重新 setup 并增加 `--allow-coverage-regression`。 |
| `fresh_failed` | 本次查询没有取得新快照；先单独运行 `refresh` 并按其错误恢复，不要把旧结果称为最新数据。 |
| `state_recovery_failed`、`state_commit_failed` | 上一代仍应保留。先运行 `v-local-cli doctor`，不要直接修改状态文件；需要交给维护者时使用 `doctor --bundle FILE`。只有用户确认放弃现有 v-local-cli 数据时才走 `forget --dry-run` 与确认删除流程。 |
| `confirmation_required` | 先运行对应的 `--dry-run` 查看删除范围，取得用户明确确认后再执行。 |
| `keychain_delete_failed`、`account_data_delete_failed` | 不要声称已完全删除；保持同一桌面用户身份，运行 `doctor` 后重试。 |
| `database_key_rejected` | 候选没有通过 SQLCipher 首页或 WAL 验证；重新取得候选，不要手工修改数据库。 |
| `database_credential_invalid` | 已保存的结构化凭据无法为当前 catalog 派生并通过首页 HMAC；重新运行 `setup --allow-key-access --storage keychain`，不要手工修改凭据库或数据库。 |
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
| `chat_image_unavailable` | 失败先看 `details.local_resolution_status` 与结构化恢复动作：`run_recover_chat_image_offline_then_request_structured_consent` 表示先运行不带 `--consent` 的离线预检，只有返回 challenge 才询问联网授权；只有 `ask_user_to_open_original_then_refresh_and_retry` 才请用户在微信打开该图片并运行 `refresh --require-media`。`decoder_unavailable` 表示 WXGF 等强关联候选已存在但本构建缺少已验收解码器，错误响应在账号已解析后仍应带 `meta.generation_id`/manifest，不能伪装成成功图片；`local_validation_failed` 检查图片密钥与容器；`content_conflict` 要停止猜测并重新取证。若 `medium|thumbnail` 已成功，应改看 `higher_quality_local_status` 与 `higher_quality_recovery_action`。所有结果都必须保持 `source_original_quality_status=unknown`；high/medium、边长和文件大小不能证明原图。 |
| `chat_image_recovery_network_authorization_required` | 命令仍未联网。向用户说明 `consent_challenge` 中的账号、消息、候选、generation、目标域名、`observed_at`、五分钟到期时间和单次 GET；此时 `descriptor_expiry_status=unknown_without_verified_request`。只在用户对本次 challenge 明确同意后增加 `--consent <id>`。该授权不包含微信 UI 自动化。 |
| `chat_image_recovery_consent_replayed`、`chat_image_recovery_consent_expired`、`chat_image_recovery_consent_not_found`、`chat_image_recovery_consent_invalid` | challenge 已使用、过期、缺失或损坏；不得复用。重新运行不带 `--consent` 的预检，并取得新的明确授权。 |
| `chat_image_recovery_consent_scope_mismatch`、`chat_image_recovery_snapshot_changed`、`chat_image_recovery_descriptor_changed` | 账号、消息、图片、输出目标、generation、manifest 或候选描述符不再与授权一致；CLI 已在联网前停止并消费旧 challenge。基于当前快照重新预检和授权。 |
| `chat_image_recovery_consent_issue_failed`、`chat_image_recovery_consent_state_failed` | 无法安全创建或原子消费账号私有 challenge；检查私有状态目录权限和重解析点，不要把 challenge 改存到项目目录或手工编辑。 |
| `chat_image_recovery_protocol_unavailable` | 当前只有十六进制不透明桌面参数，不能拼接 iLink URL。若 `recovery_action=ask_user_to_open_original_then_refresh_and_retry_once`，请用户手动打开这一张原图；确认后 Agent 只 refresh 并重试一次。CLI 不自动操作微信 UI。 |
| `chat_image_recovery_descriptor_unavailable`、`chat_image_recovery_no_higher_variant`、`chat_image_recovery_local_quality_unknown` | 当前没有可安全尝试的更高缓存层级 full URL、只有同级/更低候选，或本地层级未知。停止联网恢复；刷新快照或人工复核，不能把低层级缓存、LongEdge/ShortEdge 或文件大小升级成成功。 |
| `chat_image_recovery_evidence_unavailable`、`chat_image_remote_candidate_unavailable`、`chat_image_remote_descriptor_binding_insufficient` | 当前 generation 中消息不存在、授权候选已不可用，或描述符缺少 MD5 /“长度 + 成对尺寸”绑定材料。CLI 不联网；刷新快照并重新取得 evidence，不能放宽消息归属校验。 |
| `chat_image_remote_url_rejected` | 当前快照中的 full URL 不再满足精确 HTTPS 主机、路径和唯一查询参数策略。CLI 不联网，不接受用户手工替换 URL；刷新快照重新预检。 |
| `chat_image_remote_authorization_rejected`、`chat_image_remote_resource_unavailable` | URL 或鉴权参数可能已经失效，但 `401/403/404/410` 只证明本次不可用，`descriptor_expiry_known` 仍为 false。刷新快照取得新描述符，生成新 challenge 并重新授权。 |
| `chat_image_remote_redirect_rejected`、`chat_image_remote_non_public_address`、`chat_image_remote_synthetic_proxy_address`、`chat_image_remote_invalid_address` | 保持拒绝；CLI 不跟随重定向，也不把鉴权参数发送到非公网、代理 fake-IP 或越界目标。不要改用通用下载器。 |
| `chat_image_remote_dns_failed`、`chat_image_remote_connection_failed`、`chat_image_remote_request_failed`、`chat_image_remote_request_build_failed`、`chat_image_remote_response_read_failed` | DNS、连接、请求构造或响应读取失败；描述符时效未知且授权已消费。不得无限重试；重新预检并取得新的单次授权。聊天图片不会使用外部 DNS 回退。 |
| `chat_image_remote_direct_dns_failed`、`chat_image_remote_direct_dns_transport_failed` | 聊天图片生产下载器没有外部 DNS fallback，正常路径不会产生这两个兼容分类；若观察到，停止并检查构建/契约漂移，不能据此启用 DNSPod 或代理。 |
| `chat_image_remote_rate_limited`、`chat_image_remote_http_status` | `429` 只表示限流，其他 HTTP 错误也不能证明过期。稍后重新生成 challenge 并授权，不要并发或循环请求。 |
| `chat_image_remote_response_size_invalid`、`chat_image_remote_mime_invalid`、`chat_image_remote_mime_mismatch`、`chat_image_remote_decrypt_failed`、`chat_image_remote_container_invalid` | 响应为空、超限、MIME 伪造、解密失败或不是完整图片；不生成输出，也不回退到缩略图声称成功。 |
| `chat_image_remote_descriptor_size_mismatch`、`chat_image_remote_descriptor_dimensions_mismatch`、`chat_image_remote_descriptor_md5_mismatch`、`chat_image_remote_descriptor_mismatch`、`chat_image_remote_message_binding_mismatch`、`chat_image_recovery_download_binding_mismatch` | 下载内容、当前消息或候选描述符与授权不一致；视为 `response_unverified` 或请求前绑定变化，清理数据并停止。重新取证，不要降低绑定要求。 |
| `chat_image_recovery_temporary_cleanup_failed` | 私有明文临时文件未可靠删除；立即停止其他恢复，查看 `details.output_committed` 判断最终输出是否已发布，并人工清理同目录的 `.v-local-cli-output-*.tmp`。 |
| `chat_image_recovery_output_failed`、`chat_image_recovery_failed` | 没有安全发布可信输出；授权已经消费。修复输出目录或运行 `doctor`，然后生成新 challenge，不要复用旧授权。 |
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
| `moment_media_download_failed_dns_failed`、`moment_media_download_failed_connection_failed`、`moment_media_download_failed_request_failed`、`moment_media_download_failed_direct_dns_failed`、`moment_media_download_failed_direct_dns_transport_failed` | 检查 DNS 和网络；描述符时效仍未知，刷新快照重新取得证据并重新请求单次授权。不要启用环境代理、关闭 TLS 验证或把令牌交给其他下载器。 |
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

聊天图片恢复在没有取得可验证内容的失败路径中保留描述符的 `observed_at`，并明确返回 `retrieved_at=null`；快照变化还分开报告授权时的 `observed_at` 与 `current_snapshot_observed_at`。这些字段不能被 HTTP 状态、缓存层级或文件尺寸替代。

系统凭据按桌面用户身份隔离。若 CLI 在不同用户、服务或沙箱身份下运行，`refresh` 可能看不到原身份保存的凭据；不要把密钥复制到项目文件来绕过隔离，应切回同一桌面用户身份，或重新 setup。

`setup --dry-run` 永远不会启动 Provider。若它的输出显示 `key_provider.executable_present=false`，这不是数据库损坏，只表示没有解析到 Provider 可执行文件；即使为 `true` 也不能替代 `integrity`、协议和真机 route 验证。

朋友圈远端错误会在可判断时通过 `remote_descriptor_status`、`descriptor_expiry_status`、`retry_policy`、`authorization_scope` 和 `network_access_performed` 给出不含令牌的状态。`present_expiry_unknown` / `unknown` 配合 `single_evidence_single_attempt` 表示当前只验证到描述符存在，尚未联网；`missing` / `not_available` 配合 `refresh_snapshot_for_new_descriptor` 表示没有可用描述符。`rejected_by_policy` 仍需刷新描述符，不能绕过目标限制。`request_failed` / `unknown_after_request_failure` 使用 `refresh_snapshot_for_new_descriptor_and_reauthorize`；`expired_or_rejected` 或 `resource_unavailable_or_expired` 使用 `requires_new_descriptor_and_new_authorization`。`temporarily_rate_limited` 的策略是 `retry_requires_new_single_attempt_authorization`，不能直接判定过期；普通 `http_error` 使用 `retry_then_refresh_descriptor`。`response_unverified` / `unknown_after_unverified_response` 表示虽然收到了数据，但尚未验证为目标媒体，也不能据此断言描述符仍有效；容器协议异常时使用 `inspect_protocol_then_refresh_descriptor_and_reauthorize`，不能盲目重复下载。
