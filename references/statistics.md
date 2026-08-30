# 基础统计契约

`stats` 对选定账号、可选会话和时间范围执行确定性聚合，不读取或返回 `message_content`。它只查询消息类型、发送者编号和时间字段，适合先回答数量与分布问题，再按需用 `history` 或 `messages` 获取正文证据。

```text
v-local-cli stats [--account NAME] [--fresh] [--start YYYY-MM-DD] [--end YYYY-MM-DD] [--date YYYY-MM-DD|today|yesterday | --last-24h | --all] [--top N] [username]
```

## 公共字段

- `source_rows`：选定时间范围内成功扫描的原始消息行，包括系统消息。
- `total_messages`：排除系统消息后的统计基数。
- `system_messages`：被排除的系统消息数。
- `active_days`、`first_timestamp`、`last_timestamp`：非系统消息的本地时间跨度。
- `by_kind`：按 `local_type` 基础类型及打包子类型划分的细分类。
- `by_category`：适合图表的 `text/image/voice/video/file/sticker/other` 分类。
- `by_hour`：索引 0..23 的本地小时消息数数组。
- `by_date`：按本地日期聚合的消息数。
- `media_messages`、`by_media_kind`：图片、语音、视频、文件和表情统计。

类型统计不解析正文 XML。大整数 `local_type` 可以拆出 appmsg 子类型；普通 `local_type=49` 若没有打包子类型则记为 `appmsg`，不猜测其具体内容。

## 私聊方向

私聊与公众号返回 `direction`：

- 发送者 username 等于会话 username：接收；
- 其它已具名发送者：发送；
- 旧库发送者映射缺失时，`real_sender_id=2` 回退为发送，其余回退为接收；
- `basis` 明示当前判定规则，`unknown` 保留无法归类的数量。

这是对本地消息行的兼容性判定，不是服务器侧送达、已读或身份状态证明。

## 群聊成员

群聊返回：

- `participants`：在 `Name2Id` 中成功解析的发送者数量；
- `unknown_sender_messages`：无法映射发送者的非系统消息数；
- `members`：除消息数、媒体数、活跃天数、首次和最后消息时间外，还返回 `username`、微信 `nickname`、`remark`、`contact_display`、`group_nickname`、最终 `display`、`sender_identity` 和 `is_from_me`；按消息数降序排列。群昵称与联系人昵称分字段保留，不互相覆盖；`sender_identity=self` 依据本地消息状态列推断，不是服务器身份凭证。

`--top 20` 是默认展示范围；`--top 0` 返回全部已识别成员。排行只表示选定本地时间范围内的发言数量，不表示内容质量、贡献、影响力或关系强弱。

## 跨会话统计

省略 username 时扫描所有能够由联系人、会话或 `Name2Id` 与哈希表名稳定绑定的消息表，返回 `scope=all_chats`：

- `active_chats`、`source_tables`：选定范围内的活跃会话数与成功扫描表数；
- `chats`：按非系统消息数降序的会话排行，包含稳定 `chat`、显示名、会话类型、消息/系统/媒体数量、活跃天数及实际起止时间；
- `--top 20` 默认只展示前 20 个会话，`--top 0` 展示全部已识别活跃会话；
- `statistic_basis.complete=false` 时检查 `unknown_tables` 和 `failed_tables`，不得把结果描述成当前快照的全部会话。

`--date YYYY-MM-DD|today|yesterday` 选择一个本地自然日；`--last-24h` 选择截至执行时刻的滚动 24 小时。两者含义不同且不能与 `--start`、`--end`、`--all` 混用。精确边界、时区与秒数读取 `meta.time_window`。

## 覆盖边界

- 所有统计只覆盖当前已发布快照中仍留存、成功解密且当前结构适配器能读取的消息表。
- `--all` 取消日期限制；统计命令本身不设消息条数上限。
- 单会话 `source_databases` 是实际包含目标会话表并成功扫描的数据库数量；跨会话时是成功打开并检查的消息数据库数量。
- 比较不同会话时使用相同账号、日期范围、时区和系统消息排除规则。
