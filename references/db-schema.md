# 当前读取的数据库结构

CLI 不按微信展示版本号分支，而是在已解密快照中探测当前命令需要的表和字段。

## 联系人

从 `contact.db` 的 `contact` 表读取存在的 `username`、`alias`、`remark`、`nick_name`。显示名按 `remark`、`nick_name`、`username` 依次回退。`@chatroom` 结尾标记群聊，`gh_` 开头标记公众号，其余标记个人。

## 消息

会话表名为 `Msg_<md5(username)>`。CLI 在 `message_*.db` 和 `biz_message_*.db` 分片中读取存在的字段：

- `local_id`、`server_id`、`local_type`
- `sort_seq`、`create_time`
- `real_sender_id`
- `message_content`、`compress_content`、`WCDB_CT_message_content`
- `source`（存在时用于提及列表）

红包消息除读取消息 XML 外，还会尝试关联 `general.db` 的 `redEnvelopeTable`。当前常见 Windows 4.x 表可提供 `message_server_id`、`session_name`、`native_url`、`send_id`、`hb_status` 和 `receive_status`；旧版或变体表可能额外保留 `receive_time`、`receive_amount`。实现按实际列动态读取，缺少的领取日期或金额会明确标记为 `not_retained`，不会推测。

`Name2Id(rowid, user_name)` 用于解析发送者。正文以 zstd 魔数开头或压缩标记为 4 时执行受限解压。聊天查询会把常见 XML 消息转换为紧凑 `content`，并在 `details`、`reply_to`、`mentions`、`voice_duration_ms` 和 `media_md5` 中保留结构；具体映射读取 [message-types.md](message-types.md)。不认识或不安全的结构会保留类型与解析状态，不把整段卡片 XML 当成可读正文。

排序优先使用 `sort_seq`，缺失时回退 `create_time*1000`。`evidence_id` 由会话 username 与 `server_id` 组成；缺少 `server_id` 时使用 `local_id`。

`history`、`search` 和 `export` 的日期范围按 `create_time` 在 SQLite 查询阶段过滤。开始日期包含本地 00:00:00，结束日期包含本地 23:59:59；需要时间过滤但消息表缺少 `create_time` 时，CLI 明确报错而不是返回未过滤结果。

`stats` 只选择 `local_type`、`create_time` 和 `real_sender_id`，通过同库 `Name2Id` 映射发送者；不会选择或解码消息正文。系统消息以 `local_type` 基础类型 10000 识别并单独计数。

朋友圈适配器探测 `SnsTimeLine` 的 `tid`、`user_name` 和 `content`。正文时间位于 `TimelineObject/createTime`，点赞、评论、回复引用及评论图片位于同一行根节点的 `LocalExtraInfo`；日期过滤在安全解析 XML 后执行。表列与 XML 中的作者或记录 ID 冲突时，原帖和评论媒体都拒绝归属。`SnsMessage_tmp3` 属于互动通知存储，不作为原帖完整互动列表的主证据源。

原帖及评论媒体的公开结构只返回逻辑字段、本地解析结果和派生 `evidence_id`。XML 中用于 CDN 访问的 token、数字 key 与加密索引保留为进程内私有描述符，JSON 序列化时不可见；独立图片或普通视频导出只能用 `evidence_id` 重新定位它们，不能从命令行注入 URL、token 或 key。普通视频的 key 来自同一条朋友圈 XML 的外层 `<enc key>`，不会回退到缩略图 key。

公众号适配器只在 `biz_message_*.db` 中查找 `gh_*` 对应的 `Msg_<md5(username)>` 表，并解析 `message_content` 或可用压缩候选中的 `appmsg/mmreader/category/item`。普通服务通知计入覆盖率，但不伪装成文章。

## 能力限制

- 字段缺失时只跳过相应值，不猜测列语义。
- 当前搜索按联系人 username 推导消息表，只扫描有限窗口。
- 本地库、快照或查询结果都不能证明服务器端完整历史。
