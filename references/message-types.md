# 聊天消息类型与结构化输出

聊天消息先按 `local_type` 的低 32 位判一级类型。大整数类型的高 32 位是应用消息子类型；普通 `local_type=49` 则优先读取外层 `<appmsg><type>`，不会误取 `<refermsg>` 中被引用消息的类型。

| 一级类型 | `kind` | 输出 |
|---|---|---|
| 1 / 3 / 34 / 42 / 43 | `text` / `image` / `voice` / `card` / `video` | 文本、媒体占位、语音时长/已有转写、微信名片及媒体 MD5 短指纹 |
| 47 / 48 / 50 / 10000 | `sticker` / `location` / `voip` / `system` | 表情、地点、语音或视频通话、清洗后的系统消息 |
| 49 | 见下表 | 紧凑 `content` 加结构化 `details` |

常见应用消息子类型：3 音乐、5/49 链接、6/8/24 文件、19 合并聊天记录、33/36 小程序、51 视频号、53 接龙、57 引用回复、62 拍一拍、87 群公告、115 礼物、2000 转账、2001 红包。未知子类型不会伪装成链接，而是输出 `[应用消息·<type>]`。

`history` 和 `search` 的消息对象还会提供：

- `base_type`、`sub_type`、`type_label` 和稳定英文 `kind`；
- `details`：卡片标题、摘要、URL、来源、文件标识与大小、公众号多图文文章，或合并聊天记录的 `items[]`；
- `reply_to`：引用对象、被引用文本、服务端标识和媒体 MD5；
- `mentions`、`voice_duration_ms`、`voice_transcript`、`voice_transcript_source`、`media_md5`；
- `sender_username`、`sender_nickname`、`sender_remark`、`sender_contact_display`、`sender_group_nickname`、`sender_identity` 和 `is_from_me`。`sender` 按「群昵称 → 联系人显示名 → 微信昵称 → username」选择；`sender_identity` 是基于本地状态字段的 `self`、`contact` 或 `unknown` 兼容性判定，不是服务器身份凭证。

专属结构：

- 微信名片：`details.username`、`nickname`、`alias`、地区、签名、头像、认证及品牌名片字段；`content` 使用 `[微信名片] 昵称｜username`。
- 小程序：`details.mini_program` 保存 `app_id`、`username`、`page_path`、版本、服务/子类型、分享标识、图标和页面缩略图。
- 视频号：`details.channels` 保存 `share_url`、对象/nonce 标识、账号、昵称、描述、feed/live/关联商家字段，以及 `media[]` 的 URL、缩略图、封面、尺寸和时长；视频号名片另存于 `name_card`。
- 红包：`details.red_packet` 保存支付消息标识、场景和本地保留的描述字段，并用会话 username 与 `message_server_id` 优先关联 `general.db/redEnvelopeTable`。`receive_status_code=0/2` 分别映射为 `not_received`/`received`；其他码返回 `unknown` 并保留原始值。`packet_status_code=2/4/5` 分别映射为 `available`/`fully_claimed`/`expired`。表不存在时为 `receive_status=not_retained`，表存在但没有强关联记录时为 `unmatched`，不会按相邻红包猜测。`message_timestamp`、`message_time`、`message_date` 来自消息 `create_time`；只有表实际含 `receive_time` 时才返回领取时间，否则 `receive_time_status=not_retained`。金额优先读取消息 XML 的 `redenvelopereceiveamount`、`amount` 或 `totalamount`，旧版表实际含 `receive_amount` 时可作为后备；返回 `amount_minor_units`、`amount_currency=CNY`、格式化 `amount`、精确 `amount_source` 及区分领取金额/卡片金额/总金额的 `amount_kind`。常见 Windows 微信 4.1 表没有领取时间和金额列，届时明确返回不可用状态，不会从祝福语或相邻记录猜测。
- 语音：命中微信已有索引或 v-local-cli 私有暂存后，`content` 在语音占位后附加 `转文字：...`，并保留独立转写与来源字段。仅搜索转写内容时使用 `voice-search`。

文本、引用文本、合并聊天记录摘要和已知的微信方括号表情会统一归一化为 Unicode；只转换白名单中的微信表情名，未知的 `[内容]` 原样保留，避免误改金额或普通括号文本。

合并聊天记录最多展开 500 项，公众号多图文最多展开 100 项；超限会明确标记 `truncated`。应用消息原文超过 4 MB，或含 `DOCTYPE`/`ENTITY` 声明时拒绝 XML 解析。畸形 XML 只提取少量安全字段并标记回退状态。`search` 会匹配紧凑摘要、详情、引用文本和提及列表。

统计命令仍直接按数据库行及 `local_type` 计数，不加载消息正文。因此卡片解析失败不会改变总消息数，也不会把一张合并聊天记录按内部条数重复计入基础统计。
