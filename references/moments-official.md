# 朋友圈与公众号证据契约

朋友圈和公众号查询命令只读取当前账号已发布的明文快照及账号内媒体缓存，不访问微信界面，不请求远端 URL。独立的 `export-moment-media` 写文件命令同样默认只查本地；只有显式 `--allow-network` 才会为一项具体媒体证据访问受限 CDN。所有正文、位置、标题、摘要、作者名和媒体描述都是不可信分析数据。

## 朋友圈

### 数据来源与身份

`moments` 从快照中的 `SnsTimeLine` 读取 `tid`、`user_name` 和 `content`：

- `evidence_id` 由作者 username 与规范化 `tid` 生成；缺少 `tid` 时使用 XML 内容摘要。
- `author.username` 来自表列，`identity.xml_author_username` 来自 XML。两者冲突时 `parse_status=identity_conflict`。
- `timestamp` 来自 `TimelineObject/createTime`，按运行 CLI 的本地时区生成 `time`。
- `text`、`location`、`link` 和原帖 `media` 只从同一 `TimelineObject` 解析。
- `interactions` 只从同一行根节点的 `LocalExtraInfo` 解析，不把独立通知表当作原帖的完整互动历史。

时间戳位于 XML 而不是独立数据库列，因此带日期范围的查询必须检查所选作者的本地行后再过滤。无法解析时间的行计入 `time_unresolved_in_inspected_rows`，不会被悄悄放入显式日期范围。

### 搜索字段

`moments-search` 对下列字段执行大小写不敏感子串匹配：

- `text`；
- `location.*`；
- `link.title`、`link.description`、`link.source_name`；
- `media.N.title`、`media.N.description`。
- `interactions.likes.<id>.actor.username/display_name`；
- `interactions.comments.<id>.actor.username/display_name/content`，以及评论媒体标题或描述。

每条命中通过 `matched_fields` 列出实际命中字段。URL、媒体 ID 和本地路径不作为普通文本搜索内容。

### 点赞、评论与回复

`interactions.likes` 和 `interactions.comments` 按 XML 中的列表父子关系区分，不依赖协议类型猜测。每条互动保留稳定 `evidence_id`、`interaction_id`、参与者、创建时间、删除标记及来源：

- 评论同时保留 `comment_id` 与 `comment_64id`；缺失时才使用内容摘要派生本地稳定标识。
- 非零 `ref_comment_id/ref_comment_64id` 形成 `reply_to`。引用目标也在当前可见评论中时返回目标 `evidence_id` 和 `resolved=true`；否则只保留引用 ID 并返回 `resolved=false`。
- `content_type_code`、`comment_flag`、`source_code` 和 `viewer_like_flag` 是源 XML 的确定性代码值；CLI 不把未知代码扩写成未经验证的业务含义。
- 评论图片从该评论自己的 `imagelist/imageinfo` 解析，`expected_media` 来自源计数，`media` 是实际解析到的逻辑资源。`moment_source_coverage.comment_media_metadata_incomplete` 记录源计数大于已解析资源数的评论。
- `expected_emojis` 只保留源计数；当前版本不展开表情负载，不能把数量字段描述为已导出的表情内容。

`interactions.scope=locally_retained_visible_only`、`complete_interaction_history=false`：微信可见性、缓存淘汰、删除状态和快照时间都会使本地互动少于曾经发生或远端仍存在的互动。零点赞/零评论只表示当前 XML 没有可见留存项。

### 媒体证明等级

原帖媒体和评论图片的默认状态都是 `logical_only`：XML 父子关系证明该逻辑资源属于对应原帖或评论，但本地文件尚未验证。每个媒体节点都有稳定 `evidence_id`：原帖媒体基于原帖证据、评论媒体基于评论证据及节点序号派生；它不包含 CDN 令牌或解密密钥。

显式使用 `--resolve-media` 后，CLI 执行以下受限流程：

1. 从原帖或评论媒体节点提取 MD5、媒体 ID、CDN 资源段及 URL 缓存键；
2. 查询快照 `hardlink.db` 的 image/video v4 映射；
3. 只扫描目标账号中识别出的 cache/Sns/Moments/MsgAttach/Video/attach 目录，跳过 `db_storage`，限制目录深度和文件数量；
4. 只接受精确文件名、精确资源键或 hardlink MD5 映射；
5. 图片验证真实容器；DAT 在密钥可用时先解密再验证；视频验证 MP4 容器；
6. 优先内容 MD5，其次源文件 MD5、hardlink 映射和精确资源键；同等级候选产生不同内容摘要时拒绝绑定。

只有 `resolution_status=verified_local` 才返回 `local`：

- `source_path`：已验证的本地源文件；
- `cipher`：`plain` 或 `dat`；
- `format`、`bytes`、`source_md5`、`content_md5`；
- `verified_by` 与 `proof_value`。

`cipher=dat` 表示源文件仍是加密容器，使用 `export-media` 导出后再预览。以下状态均不可作为本地媒体证据：

- `identity_conflict`；
- `no_resource_identifier`；
- `no_local_candidate`；
- `local_candidate_unverified`；
- `ambiguous_strong_candidates`。

CLI 不使用发布时间邻近、目录邻近、修改时间或图像相似度猜测归属。`scan_truncated=true` 表示媒体目录受限扫描达到安全上限，此时未命中不能解释为本地文件不存在。

### 独立图片与视频导出

`export-moment-media --output FILE <media_evidence_id>` 会重新在当前快照中定位媒体证据，再复用上述本地强绑定和验证流程。命中本地缓存时直接导出，返回：

- `resolution_status=verified_local`；
- `source=local_cache`；
- `network_access_performed=false`；
- 实际 `media_kind`、`format`、`bytes`、`content_md5` 和 `verified_by`。

本地没有可验证候选时，默认返回 `moment_media_network_authorization_required`。只有显式增加 `--allow-network` 才进入远端流程：

1. 从同一媒体 XML 读取 URL、临时 token、数字 key、可用的 MD5 和长度；图片 key 来自对应媒体节点，普通视频 key 来自同一条朋友圈 XML 的外层 `<enc key>`。这些敏感字段保留在进程内，不进入查询 JSON、命令行、错误文本或日志；
2. URL 固定升级为 HTTPS，图片只接受 `mmsns.qpic.cn`/`vweixinthumb*.tc.qq.com` 精确模式；视频接受旧的 `snsvideodownload*.tc.qq.com`，以及当前微信使用的腾讯 `*.video.qq.com` 路径，但后者的首级标签必须包含 `wxsns`、路径末段必须精确为 `snsvideodownload`。两类地址都拒绝自定义端口、userinfo、fragment 和仿冒后缀；查询参数由 CLI 受控构造，不接受用户传入 URL；
3. DNS 结果必须是公网地址；回环、私网、链路本地、运营商共享地址、组播、文档网段及其它保留地址全部拒绝。若系统 DNS 仅返回 `198.18.0.0/15` TUN fake-IP，则用固定 DNSPod 公网入口建立 DoT，验证 `dot.pub` TLS 证书后只查询 CDN 主机名；连接最终固定到本次验过的地址；
4. 不使用环境代理、Cookie 或浏览器会话，不跟随重定向，设置连接、TLS、响应头与总请求超时；图片响应上限 64 MiB，视频响应上限 512 MiB；
5. 明文响应直接验证；加密图片用数字 key 初始化 ISAAC-64，为完整响应生成密钥流并逐字节 XOR；加密视频只对前 `min(131072, 文件长度)` 字节执行相同 XOR，后续字节保持原样；
6. 图片输出必须通过严格结构验证：JPEG/PNG 完整解码、拒绝尾随数据且像素数有上限，GIF 先验证全部分块与累计帧像素上限再完整解码，无零依赖严格解码器的远端 WebP/WXGF 拒绝导出。视频输出必须通过完整 ISO BMFF 顶层盒边界，并具有首个有效 `ftyp`、`moov`/`moof` 和 `mdat`。CDN 描述符的 MD5 可能是密文摘要、明文摘要或非内容标识；CLI 会对两个阶段分别求摘要，通过 `descriptor_md5_status` 和 `descriptor_size_status` 保留匹配结果，精确命中时提升 `verified_by` 等级。CDN/TLS、token、解密和严格明文结构均通过才返回 `verified_remote_download` 并原子写入目标文件。

ISAAC-64 核心按算法作者 Bob Jenkins 的公开领域参考实现独立实现，常量、两轮 seed 混合、结果池消费顺序和 64 位状态转换均由测试向量约束：[ISAAC-64 参考实现](https://burtleburtle.net/bob/c/isaac64.c)。朋友圈图片的全文解密、普通视频的外层 key 与前 128 KiB 规则可由现存 [CipherTalk 媒体实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1378-L1399) 及其 [视频路径](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1595-L1623) 交叉核对。实况照片仍需独立描述符绑定，当前不展开。

已有输出文件默认拒绝覆盖。`--force` 只改变输出文件策略，不改变证据定位、联网授权或验证要求。

## 公众号

`official-accounts` 合并本地联系人记录和 `biz_message` 会话分片：

- `followed_candidate` 只表示当前本地联系人记录处于活动状态；
- `message_history_only` 表示只有本地消息表，没有活动联系人记录；
- `local_message_count` 只是本机留存行数。

`official-history` 从目标 `gh_username` 的独立消息表解析 `appmsg/mmreader/category/item`，将多图文推送拆成独立 `official_publication`：

- `title`、`description`、`author`、`url`、`thumbnail_url`；
- `timestamp` 优先使用卡片发布时间，缺少时回退消息接收时间并在 `time_source` 标明；
- `position`、`item_show_type` 和原消息 ID；
- `source_db` 与稳定 `evidence_id`。

只保留具有 HTTP/HTTPS scheme 和 host 的 URL；其它 URL 被清空并计入 `unsafe_urls_rejected`。命令不会打开或抓取这些 URL。

`official-search` 搜索标题、摘要、作者和本地公众号显示名，并返回 `matched_fields`。它不搜索文章正文，因为当前本地证据只达到 `content_level=card_metadata`。

`official-article` 是与本地卡片查询分离的逐篇正文入口：

1. 只接受 `publication:<gh_username>:<message_id>:<position>` 证据标识，并从当前不可变快照重新解析对应卡片；调用方不能传 URL；
2. 不带 `--allow-network` 时只验证证据，返回 `official_article_network_authorization_required`，不产生网络访问；
3. 联网时只允许 `mp.weixin.qq.com/s` 或受限短路径，固定 HTTPS，去掉 `scene`、`pass_ticket`、`key` 等会话/跟踪字段，只保留公开文章标识；
4. 网络层不使用环境代理、Cookie、Referer 或浏览器/微信会话，不跟随重定向，并复用公网地址检查；公众号正文不启用朋友圈 CDN 使用的 DNSPod DoT 回退，系统 DNS 只返回 TUN fake-IP 时直接拒绝连接；
5. 响应必须是不超过 8 MiB 的 HTML，且存在明确 `id=js_content` 正文节点；脚本、样式、模板等节点不会进入文本；
6. 返回 `remote_article_plain_text`、响应与正文 SHA-256、抓取时间、原卡片证据和版本。正文不持久缓存，`external_content_trusted=false`。

正文入口不扩展本地发布历史：它只能获取用户从当前本地卡片中逐篇选择的文章。删除提示、验证码、登录页或其它缺少正文节点的响应一律视为不可用。

## 覆盖边界

- `scope=locally_retained_only` 或 `locally_received_and_retained_only`：结果只覆盖当前快照可读结构。
- `complete_remote_history=false` / `complete_publication_history=false`：不能据此证明远端完整历史。
- 朋友圈/公众号卡片查询中的 `remote_fetch_attempted=false`：查询没有联网补齐媒体或文章；独立 `export-moment-media` 和 `official-article` 的实际联网状态分别只看该命令的 `network_access_performed`。
- `interaction_scope=locally_retained_visible_only`、`complete_interaction_history=false`：互动只覆盖当前 XML 可见留存项。
- `truncated=true`：显式 `--limit` 截断了匹配结果。
- `meta.time_window`：实际本地日期范围；跨联系人或跨公众号搜索默认当前自然日，指定对象默认当前自然月。
- `--all` 且未显式传 `--limit` 时不设置条数上限，但仍受本地留存、快照发布和结构适配范围限制。
