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

`quality_tier` 按 hardlink 缓存名区分 `high`（`_h.dat`）、`medium`（基础 `.dat`）、`thumbnail`（`_t.dat`）和 `unknown`，只证明同一消息内的相对缓存层级。它不证明绝对清晰度，也不知道发送前源图的原始尺寸；原图本身可能只有几百像素。`width`/`height` 只作为已解码输出的客观观察值，不设固定边长质量门槛。成功结果用 `quality_claim_scope=wechat_cache_variant_only`、`source_original_dimensions_known=false` 明示这一边界。即使缩略图成功，仍要读 `higher_quality_local_status`：`missing` 才表示没有更高层级可验真候选，此时可询问用户打开原图；用户确认后自动 `refresh --require-media`，以同一个 evidence ID 和新的输出路径最多重试一次。微信仍可能因远端描述符过期或资源不可用而无法补取，重试仍缺失时停止循环。`decoder_unavailable` 与 `higher_quality_detected_format=wxgf|webp` 表示更高层级已落盘但本构建不能严格解码，不能误报成“未缓存”，也不应再建议用户点开；`validation_failed` 则先复核密钥或格式。整条消息没有任何可验真本地结果时使用相应的 `local_file_missing`、`decoder_unavailable` 或 `local_validation_failed`。

消息 XML 中可能保留 high/medium/thumbnail 的 CDN 描述符。CLI 只公开 `remote_descriptor_status=present_expiry_unknown`、缓存档位列表和脱敏的 `remote_descriptor_parse_status`，不公开 URL、token 或 key。`parsed_unverified_protocol` 表示不透明参数与 16 字节 key 结构有效，并且具备明文 MD5，或同时具备长度与成对尺寸作为响应绑定材料；`parsed_partial_unverified_protocol` 表示至少一个候选可解析但另有非法或不完整材料；`present_incomplete`、`present_invalid` 分别表示没有候选具备完整必需材料或结构非法。仅有参数与 key 而没有绑定材料也属于 `present_incomplete`，不会发起资格请求。桌面 XML 常以 `0` 表示未提供可选宽高或长度；解析器把它当作“无观测值”，既不用于响应绑定，也不据此评价图片质量，而不是误报成非法描述符。它们都不证明描述符此刻有效，也不证明桌面协议。当前桌面聊天 CDN 的请求、解密和响应绑定尚未通过真机门禁，因此 `remote_protocol_status=unverified_desktop_protocol`、`remote_acquisition_status=unavailable_unverified_protocol`，`export-chat-image` 没有联网开关。不能仅因 Agent 已询问并获得同意就绕过这项实现边界。

这些 `cdn*imgurl` 字段在当前桌面消息样本中表现为加密或不透明的请求参数，不能把字段值本身当作可直接请求的 HTTPS URL；公开的 [iWeChat 消息样例](https://github.com/lefex/iWeChat/blob/master/MESSAGE.md#%E7%9B%B8%E6%9C%BA%E5%9B%BE%E7%89%87)也将其标为加密 URL。iLink Bot 的公开实现虽然使用 `/download?encrypted_query_param=...` 与 AES-128-ECB，但它属于另一套消息协议，不能据此断言桌面聊天描述符可复用同一基座地址、参数编码或解密规则。

第一阶段门禁现已实现：不联网解析器不导出秘密；`synthetic_loopback_crypto_binding_harness_aes_128_ecb_pkcs7` 只允许显式注入的 TLS 假服务，且目标必须是字面量 loopback IP，验证单候选单请求、禁环境代理/cookie/重定向、响应大小上限、严格 PKCS#7、完整容器解码，以及长度、尺寸、MD5 绑定。尺寸在这里仅用于核对描述符与响应是否属于同一证据，仍不是质量门槛。合成结果只能标为 `synthetic_crypto_binding_harness_only`；它只验收加解密、容器和绑定安全壳，不模拟桌面 CDN 的认证请求。任何非 loopback 端点都会在请求前拒绝，也不能转换成 `verified_at_request_time`。

一份 [2023 年非官方经典客户端实现](https://github.com/Rprop/ipad/blob/0278be2947eef98cb73f46579bce4428f7e93b96/clientsdk/cdnrequest.go)曾使用 UIN、AuthKey、动态 CDN 路由、RSA 包装 key，或已认证 MMTLS 与消息 ID。它仅作历史线索：文件的唯一相关提交来自 2023 年，搜索到的同类代码主要是同源复制，不能证明 2026 年当前 Windows 桌面端仍采用这些材料、封包或路由。它也不能反向证明当前描述符一定不能使用某个 HTTPS 下载协议。当前可靠结论只有两点：不能未经验证套用 iLink Bot 协议，也不能未经验证套用这套旧客户端协议；用户同意授权联网并不能补足协议证据。

仓库提供只读的[当前 Windows 客户端静态证据检查器](../scripts/inspect-windows-chat-cdn-static-evidence.ps1)。它只读取本地安装目录中的 `Weixin.dll`、`ilink2.dll` 和 `ilink_wrapper.dll`，将版本、大小和 SHA-256 与固定的非秘密实现标志绑定；不读取账号文件或进程内存，也不联网。2026-08-29 对正在运行的 Weixin 4.1.12.55 复审时，同时观察到消息三个 CDN 字段、`CreateC2CImageDownloadTask`、C2C 下载栈、动态 `getcdndns`、RSA 参数门禁、iLink C2C API、`novac2c` 主机和独立 HTTPS 下载任务，因此旧实现所描述的“会话化 C2C 栈”在当前客户端中仍有架构层参考意义，并非已经消失。

该结果只能标为 `current_client_static_stack_present_unbound`：静态字符串可能属于休眠或其他调用路径，同一二进制内共存也不能证明 XML 描述符实际流向哪一个请求。检查器因此固定返回 `descriptor_to_runtime_request_binding=not_observed`、`runtime_protocol_selection=not_observed` 和 `endpoint_qualification=not_qualified`。未发现某个静态字符串同样不能证明运行时没有该能力；该报告不进入普通 CLI 的能力判断，也不能启用真实端点。

下一阶段应先从受控的当前 Windows 桌面客户端取得当代证据，确认请求端点、参数编码、封包、路由来源、必要认证材料及其生命周期；在证据出现前，不预设它属于 iLink 风格 HTTPS 或旧版二进制 CDN 中的任何一种。随后才允许操作者对一条新鲜 evidence 显式授权真机探测；测试必须覆盖新鲜、疑似过期、错误 key、错误 evidence、限流和传输失败。只有已确认的桌面协议、完整解密、该消息的长度/尺寸/MD5（字段存在时）及容器解码全部一致，才能返回 `verified_at_request_time`；它仍不保证下一次可用。`401/403/404/410` 最多证明本次请求不可用，`429` 只表示限流，传输错误保持未知。过期或失败后的新 descriptor 必须来自新 generation，并重新取得单次授权；不得循环请求、复用旧授权、写回微信缓存或把请求参数/key 写入日志。

## 独立 DAT 图片

`export-media` 仅用于用户已经明确提供的独立 DAT 路径；它不能单独证明文件属于某条历史消息：

```text
v-local-cli export-media --account <account> [--force] --output <file> <input.dat>
```

当前版本不调用 ffmpeg，也不转码 wxgf。若结果是 wxgf，`data.format` 会明确返回 `wxgf`，输出文件保留原容器。

## 朋友圈 CDN 图片

朋友圈 CDN 媒体不是 DAT 容器，也不使用聊天图片的 AES/XOR 候选。`export-moment-media` 对图片从媒体节点取得数字 seed，为完整响应生成 ISAAC-64 大端序密钥流并逐字节 XOR，再以严格完整图片解码验证。普通视频从同一条 XML 的外层 `<enc key>` 取得 seed，只 XOR 前 `min(131072, 文件长度)` 字节，随后验证 ISO BMFF 顶层盒边界及 `ftyp`、`moov`/`moof`、`mdat`。XML 中可用的长度和 MD5 只作描述符辅助依据，精确命中密文或明文时才提升验证等级。图片路径可由现存 [CipherTalk 图片实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1693-L1728) 与 [ISAAC64 实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/isaac64.ts) 交叉核对；视频的外层 key 与前 128 KiB 规则见其 [视频路径](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1378-L1399) 和 [解密代码](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1595-L1623)。

视频 CDN 兼容两代地址：旧的 `snsvideodownload*.tc.qq.com`，以及微信当前使用的 `*.video.qq.com/.../snsvideodownload`。后一类不会按 `*.video.qq.com` 整体放行：首级标签必须包含 `wxsns`，路径末段也必须精确匹配下载端点。该模式同时由本机真实描述符和公开的[朋友圈视频上传响应示例](https://doc.geweapi.com/api-139908345)佐证。

```text
v-local-cli export-moment-media --account <account> --output <file> <media_evidence_id>
v-local-cli export-moment-media --account <account> --allow-network --output <file> <media_evidence_id>
```

第一条命令只尝试本地缓存；第二条命令才授权本次受限 CDN 请求。远端描述符是临时能力：授权前只报告时效未知；成功也只证明 `verified_at_request_time`，未来仍为 `unknown_future`。授权拒绝或资源不可用可能表示令牌/资源已经失效，应刷新快照取得新描述符并重新取得单次授权；限流本身不能判作过期。解密路径不能互换：不要把朋友圈数字 seed 当作 DAT XOR 字节，不要把账号 DAT 密钥用于 CDN 响应，也不要把图片的全文规则套到视频。当前支持图片和普通视频，不展开实况照片的独立视频描述符。
