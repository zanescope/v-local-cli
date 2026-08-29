# DAT 图片解密

图片候选包含 16 字节 ASCII AES 值和 0..255 的 XOR 值。候选可以来自用户文件或独立 Provider，但最终验证和解密都在主 CLI 完成。

- v1 魔数：`07 08 56 31 08 07`，AES 区使用内置固定 key，不使用账号 AES 候选；若头部声明了 XOR 尾，尾部仍使用账号 XOR 候选。
- v2 魔数：`07 08 56 32 08 07`，AES 区使用账号 AES 候选，头部声明的 XOR 尾使用账号 XOR 候选。
- v3：没有上述魔数，整个文件使用账号 XOR 候选逐字节解密，不包含 AES 区。

v1/v2 头部包含小端序 AES 区长度和 XOR 尾长度。AES 区使用 AES-128-ECB，随后拼接原始区和 XOR 尾。输出必须再次匹配 JPEG、PNG、GIF、WebP 或 wxgf 容器魔数，否则视为失败。

## 聊天消息图片

从 `history` 返回的 `kind=image` 项取得 `evidence_id` 后，优先使用：

```text
v-local-cli export-chat-image --account <account> --output <file> <image_evidence_id>
```

CLI 会从当前不可变 generation 重新定位消息，以 `message_resource.db` 中的资源 stem 与 `hardlink.db` 映射共同证明候选归属，再对明文或 DAT 解密结果做完整容器解码。成功结果返回像素宽高、明文 SHA-256、字节数和 `verified_by=message_resource_stem+hardlink_map+full_decode`；不返回微信源路径且不联网。high/medium/thumbnail 允许因缩放或编码不同而具有不同明文，CLI 选择最高可验真的层级；同一最高层级出现多个不同明文时才拒绝导出，不能按时间或邻近目录消除该歧义。

`quality_tier` 按 hardlink 缓存名区分 `high`（`_h.dat`）、`medium`（基础 `.dat`）、`thumbnail`（`_t.dat`）和 `unknown`，只证明同一消息内的相对缓存层级。它不证明绝对清晰度，也不知道发送前源图的原始尺寸；原图本身可能只有几百像素。`width`/`height` 只作为已解码输出的客观观察值，不设固定边长质量门槛。成功结果用 `quality_claim_scope=wechat_cache_variant_only`、`source_original_dimensions_known=false`、`source_original_quality_status=unknown` 明示这一边界；LongEdge、ShortEdge、文件大小和 high/medium 名称都不能单独升级该 unknown。即使缩略图成功，仍要读 `higher_quality_local_status`：若当前消息含 full URL，恢复动作是 `run_recover_chat_image_offline_then_request_structured_consent`，先做离线预检；只有不透明桌面参数的 `missing` 才继续返回 `ask_user_to_open_original_then_refresh_and_retry`，用户确认打开原图后自动 `refresh --require-media`，以同一个 evidence ID 和新的输出路径最多重试一次。微信仍可能因远端描述符过期或资源不可用而无法补取，重试仍缺失时停止循环。`decoder_unavailable` 与 `higher_quality_detected_format=wxgf|webp` 表示更高层级已落盘但本构建不能严格解码，不能误报成“未缓存”，也不应再建议用户点开；`validation_failed` 则先复核密钥或格式。整条消息没有任何可验真本地结果时使用相应的 `local_file_missing`、`decoder_unavailable` 或 `local_validation_failed`。

消息 XML 中可能保留 high/medium/thumbnail 的 CDN 描述符。CLI 只公开 `remote_descriptor_status=present_expiry_unknown`、缓存档位列表和脱敏的 `remote_descriptor_parse_status`，不公开 URL、token 或 key。`parsed_unverified_protocol` 表示请求引用与 16 字节 key 结构有效，并且具备明文 MD5，或同时具备长度与成对尺寸作为响应绑定材料；`parsed_partial_unverified_protocol` 表示至少一个候选可解析但另有非法或不完整材料；`present_incomplete`、`present_invalid` 分别表示没有候选具备完整必需材料或结构非法。仅有请求引用与 key 而没有绑定材料也属于 `present_incomplete`。桌面 XML 常以 `0` 表示未提供可选宽高或长度；解析器把它当作“无观测值”，既不用于响应绑定，也不据此评价图片质量，而不是误报成非法描述符。这些结构状态都不证明描述符此刻有效，也不证明原图质量。

`export-chat-image` 始终离线；联网恢复使用独立的 `recover-chat-image`。只有当前快照直接携带严格的 `https://novac2c.cdn.weixin.qq.com/c2c/download?encrypted_query_param=...` full URL，候选绑定材料完整，并且其缓存层级高于当前本地候选时，首次离线预检才返回 `chat_image_recovery_network_authorization_required`。Agent 向用户说明结构化 challenge 后，用户对这个账号、消息、图片、generation、描述符、输出目标和单次 GET 明确同意，才可增加一次性 `--consent <challenge-id>`。签发和消费都在同账号快照事务锁内重新加载 state；拿不到锁时在消费和联网前返回 `snapshot_busy`。challenge 五分钟到期且在联网前原子消费；重放、URL/描述符变化或快照代际变化都在请求前失败。联网授权不授权微信 UI 自动化。

这些 `cdn*imgurl` 字段在当前桌面消息样本中表现为加密或不透明的请求参数，不能把字段值本身当作可直接请求的 HTTPS URL。腾讯当前官方 `openclaw-weixin` 的 iLink 实现会优先使用消息中的 [`full_url`](https://github.com/Tencent/openclaw-weixin/blob/main/src/media/media-download.ts)，否则才由该协议自己的 [`cdn-url.ts`](https://github.com/Tencent/openclaw-weixin/blob/main/src/cdn/cdn-url.ts) 组装 `/download?encrypted_query_param=...`，并按 [`pic-decrypt.ts`](https://github.com/Tencent/openclaw-weixin/blob/main/src/cdn/pic-decrypt.ts) 做 AES-128-ECB 解密。它属于另一套消息协议，只支持“快照已携带完整 URL”这条保守路线，不能据此断言桌面十六进制描述符可复用同一基座地址、参数编码或认证状态。官方仓库仍有[入站图片可能只得到压缩版本的开放问题](https://github.com/Tencent/openclaw-weixin/issues/235)，因此“可下载”也不能升级为“已证明源原图”。

第一阶段门禁现已实现：不联网解析器不导出秘密；`synthetic_loopback_crypto_binding_harness_aes_128_ecb_pkcs7` 只允许显式注入的 TLS 假服务，且目标必须是字面量 loopback IP，验证单候选单请求、禁环境代理/cookie/重定向、响应大小上限、严格 PKCS#7、完整容器解码，以及长度、尺寸、MD5 绑定。尺寸在这里仅用于核对描述符与响应是否属于同一证据，仍不是质量门槛。合成结果只能标为 `synthetic_crypto_binding_harness_only`；它只验收加解密、容器和绑定安全壳，不模拟桌面 CDN 的认证请求。任何非 loopback 端点都会在请求前拒绝，也不能转换成 `verified_at_request_time`。

一份 [2023 年非官方经典客户端实现](https://github.com/Rprop/ipad/blob/0278be2947eef98cb73f46579bce4428f7e93b96/clientsdk/cdnrequest.go)曾使用 UIN、AuthKey、动态 CDN 路由、RSA 包装 key，或已认证 MMTLS 与消息 ID。它仅作历史线索：文件的唯一相关提交来自 2023 年，搜索到的同类代码主要是同源复制，不能证明 2026 年当前 Windows 桌面端仍采用这些材料、封包或路由。它也不能反向证明当前描述符一定不能使用某个 HTTPS 下载协议。当前可靠结论只有两点：不能未经验证套用 iLink Bot 协议，也不能未经验证套用这套旧客户端协议；用户同意授权联网并不能补足协议证据。

仓库提供只读的[当前 Windows 客户端静态证据检查器](../scripts/inspect-windows-chat-cdn-static-evidence.ps1)。它只读取本地安装目录中的 `Weixin.dll`、`ilink2.dll` 和 `ilink_wrapper.dll`，将版本、大小和 SHA-256 与固定的非秘密实现标志绑定；不读取账号文件或进程内存，也不联网。2026-08-29 对正在运行的 Weixin 4.1.12.55 复审时，同时观察到消息三个 CDN 字段、`CreateC2CImageDownloadTask`、C2C 下载栈、动态 `getcdndns`、RSA 参数门禁、iLink C2C API、`novac2c` 主机和独立 HTTPS 下载任务，因此旧实现所描述的“会话化 C2C 栈”在当前客户端中仍有架构层参考意义，并非已经消失。这里的 RSA 字符串只在上传调用分支定位到，不能把它升级为下载路径的已证实要求。

该结果只能标为 `current_client_static_stack_present_unbound`：静态字符串可能属于休眠或其他调用路径，同一二进制内共存也不能证明 XML 描述符实际流向哪一个请求。检查器因此固定返回 `descriptor_to_runtime_request_binding=not_observed`、`runtime_protocol_selection=not_observed` 和 `endpoint_qualification=not_qualified`。未发现某个静态字符串同样不能证明运行时没有该能力；该报告不进入普通 CLI 的能力判断，也不能启用真实端点。

仓库另提供脱敏的 [xlog 结构检查器](../scripts/inspect-windows-chat-cdn-xlog-structure.ps1)。它依据腾讯 Mars 当前的 [`log_magic_num.h`](https://github.com/Tencent/mars/blob/master/mars/xlog/crypt/log_magic_num.h) 和[官方解码器帧布局](https://github.com/Tencent/mars/blob/master/mars/xlog/crypt/decode_mars_nocrypt_log_file.py)，只读取当前用户 `Tencent\xwechat\log` 下单个 xlog 的帧头、长度和结尾标志；不解压或搜索正文，也不输出路径、嵌入公钥或其指纹。2026-08-29 在同一台 Weixin 4.1.12.55 验收机上，日志增长前后的两轮完整扫描中，主日志与 iLink 日志的所有帧均为 `async_zlib_crypt`，没有未加密帧；两份日志各自只有一个嵌入公钥指纹，且没有帧使用 Mars 示例公钥。Mars [官方加密指引](https://github.com/Tencent/mars/wiki/Xlog-%E5%8A%A0%E5%AF%86%E4%BD%BF%E7%94%A8%E6%8C%87%E5%BC%95)要求用与 appender 公钥成对且单独保管的私钥解码，因此 `decode_mars_nocrypt_log_file.py` 不适用，示例私钥也不能替代微信对应私钥。

这项结果只关闭“直接从当日日志正文提取请求”的低敏感度路线：检查器将这种全加密样本标为 `encrypted_mars_xlog_private_key_required`；它证明当前样本需要匹配私钥或微信定制解码器，并不证明日志中一定有或没有某次 C2C 下载事件。检查器固定保持 `payload_decoding_performed=false`、`plaintext_event_binding=not_observed`、`descriptor_to_runtime_request_binding=not_observed` 和 `endpoint_qualification=not_qualified`。不要使用旧第三方解码器猜测私钥，也不要把帧结构识别误写成协议通过。

同一份 `Weixin.dll`（SHA-256 `7ad9753d11c2baf5c900aac50ddf56a8170aa85c46129d661325ff88505befb1`）的进一步静态调用链复审，将下载入口约束为 `CdnCore::start_c2c_download -> CdnCore::_startDownloadMedia -> TaskFactory::CreateC2CImageDownloadTask`。任务构造路径同时引用 `fullpath/aeskey/fileId/clientid/filelen`、CDN root path 和完整 task param；主模块没有导出 C2C/CDN 下载入口，三个相关二进制中也都没有观察到 `/c2c/download` 或 `encrypted_query_param`。PE 表复核纠正了单纯字符串扫描的一个误差：主模块确实 delay-import `ilink_wrapper.dll`，wrapper 也导出网络管理器工厂；但本次安装目录中没有任何 PE 通过普通或延迟导入引用 `CreateNetworkManagerNoPB`，主模块只从 wrapper 导入 context/log/stream 的取得与销毁函数。这只证明组件依赖存在，不能证明主消息描述符走 iLink GET，更不能补出调用参数和会话生命周期；动态符号查找仍可能存在，所以也不能把“没有导入工厂”扩大成运行时不存在。上述组合足以否定“仅凭 XML 描述符照搬 iLink URL 就是已合格方案”的实现前提；复用内部路径还需要未声明 ABI、进程内会话和路由状态，不能通过主 CLI 注入或猜测。

操作系统元数据也没有补上这一缺口：[Get-NetTCPConnection](https://learn.microsoft.com/en-us/powershell/module/nettcpip/get-nettcpconnection?view=windowsserver2025-ps)最多给出连接端点、状态和 owning process；不读取 TLS 请求内容就不能把某个 XML 描述符绑定到一次请求。[Pktmon start](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/pktmon-start) 的默认捕获会记录每包前 128 字节，原始包标志还会把数据写入日志，因此不是等价的低敏感度“元数据确认”。只开 counters 又没有请求级绑定能力。因此本轮不增加后台网络观察器，也不把同进程、同主机或时间邻近当作协议证据。

**2026-08-29 后续恢复链路复审结论：不透明桌面参数仍不得直连；快照自带 full URL 可做单次、响应后验真的恢复。** 新实现没有把端点写死到十六进制参数上，也没有复用旧 iLink 推断。它只接受当前快照给出的严格 HTTPS full URL，拒绝用户手工传 URL、userinfo、端口、额外查询参数、非固定路径、非公网解析和所有重定向；不使用环境代理、Cookie 或外部 DNS 回退。响应上限为 64 MiB 加一个 AES block，MIME 必须与明文或 AES-128-ECB/PKCS#7 解密后的完整图片结构一致，并再次核对消息绑定、候选描述符摘要及 MD5 或“长度 + 成对尺寸”。任何失败都不会静默回退到缩略图并声称成功。

只有十六进制 `cdn*imgurl` 时仍保持 `remote_protocol_status=unverified_desktop_protocol` 和 `remote_acquisition_status=unavailable_unverified_protocol`。此时支持的恢复仍是“Agent 询问用户手动打开这一张原图 -> 用户确认操作已完成 -> Agent 自动执行一次 `refresh --require-media` -> 同一 evidence ID 用新输出路径重试一次”；它不涉及 CLI 联网或微信 UI 自动化。若仍缺失，停止循环并报告描述符可能过期或资源不可用。描述符年龄、字段存在、HTTP 状态、LongEdge、ShortEdge、文件大小、缓存层级和像素尺寸都不能单独判定时效或原始质量，`source_original_quality_status` 必须保持 `unknown`。

若未来要支持仅含不透明参数的消息，仍必须先出现官方或稳定公开 API，或独立的、签名且经过单证据真机门禁的 Provider，明确定义桌面会话材料、端点、参数编码、封包、路由来源和生命周期。当前 full URL 路线的成功只证明这次响应在完整解密、该消息的长度/尺寸/MD5（字段存在时）及容器解码上全部一致，才能返回 `verified_at_request_time`；它仍不保证下一次可用。`401/403/404/410` 最多证明本次请求不可用，`429` 只表示限流，传输错误保持未知。失败后的新 descriptor 必须来自当前或新 generation 的重新预检，并重新取得单次授权；不得循环请求、复用旧授权、写回微信缓存或把 URL、请求参数/key 写入日志。

## 独立 DAT 图片

`export-media` 仅用于用户已经明确提供的独立 DAT 路径；它不能单独证明文件属于某条历史消息：

```text
v-local-cli export-media --account <account> [--force] --output <file> <input.dat>
```

当前公开版本不调用 ffmpeg，也不转码 wxgf。若结果是 wxgf，`data.format` 会明确返回 `wxgf`，输出文件保留原容器。仓库内的 [WXGF 本地解码资格验证](wxgf-decoder-qualification.md) 只是未接入 CLI 的实验门禁；即使实验适配器成功输出 PNG，也固定保持 `production_ready=false`，不能据此改写当前能力声明。

## 朋友圈 CDN 图片

朋友圈 CDN 媒体不是 DAT 容器，也不使用聊天图片的 AES/XOR 候选。`export-moment-media` 对图片从媒体节点取得数字 seed，为完整响应生成 ISAAC-64 大端序密钥流并逐字节 XOR，再以严格完整图片解码验证。普通视频从同一条 XML 的外层 `<enc key>` 取得 seed，只 XOR 前 `min(131072, 文件长度)` 字节，随后验证 ISO BMFF 顶层盒边界及 `ftyp`、`moov`/`moof`、`mdat`。XML 中可用的长度和 MD5 只作描述符辅助依据，精确命中密文或明文时才提升验证等级。图片路径可由现存 [CipherTalk 图片实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1693-L1728) 与 [ISAAC64 实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/isaac64.ts) 交叉核对；视频的外层 key 与前 128 KiB 规则见其 [视频路径](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1378-L1399) 和 [解密代码](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1595-L1623)。

视频 CDN 兼容两代地址：旧的 `snsvideodownload*.tc.qq.com`，以及微信当前使用的 `*.video.qq.com/.../snsvideodownload`。后一类不会按 `*.video.qq.com` 整体放行：首级标签必须包含 `wxsns`，路径末段也必须精确匹配下载端点。该模式同时由本机真实描述符和公开的[朋友圈视频上传响应示例](https://doc.geweapi.com/api-139908345)佐证。

```text
v-local-cli export-moment-media --account <account> --output <file> <media_evidence_id>
v-local-cli export-moment-media --account <account> --allow-network --output <file> <media_evidence_id>
```

第一条命令只尝试本地缓存；第二条命令才授权本次受限 CDN 请求。远端描述符是临时能力：授权前只报告时效未知；成功也只证明 `verified_at_request_time`，未来仍为 `unknown_future`。授权拒绝或资源不可用可能表示令牌/资源已经失效，应刷新快照取得新描述符并重新取得单次授权；限流本身不能判作过期。解密路径不能互换：不要把朋友圈数字 seed 当作 DAT XOR 字节，不要把账号 DAT 密钥用于 CDN 响应，也不要把图片的全文规则套到视频。当前支持图片和普通视频，不展开实况照片的独立视频描述符。
