# generation 索引、增量游标与查询 daemon

这三个能力建立在同一条边界上：微信数据库先发布为不可变 snapshot generation，结构化索引只从该 generation 构建，游标只比较两个已发布 generation，daemon 只查询已发布且摘要匹配的派生索引。

## generation 级结构化全文索引

`index build` 扫描当前 snapshot 中所有可识别消息表，把解析后的正文、发送者身份、提及、引用、语音转写和文本型 detail 写入账号私有 SQLite 索引。令牌、密钥、secret 一类字段不进入全文文本。每个文档保留稳定 `evidence_id`、时间、会话、消息类型、来源数据库、内容摘要与完整结构化消息。

索引 manifest 绑定：

- `account_id`；
- `generation_id`；
- snapshot manifest SHA-256；
- 索引 schema 版本和消息 parser 版本；
- 消息扫描覆盖率。

构建先写随机 staging 目录，再原子发布到 `derived/<generation_id>`。查询发现任一绑定不匹配时拒绝复用。有效索引发布后不可重写；`--force` 只替换绑定或版本已经无效的旧索引。FTS 优先使用 trigram tokenizer；不能保证子串语义时使用大小写不敏感 LIKE，manifest 和 coverage 明确说明实际后端。

`setup` 和 `refresh` 在提交新 state 指针前尝试构建该 generation 的索引。索引失败不会把不完整索引伪装成 ready；`new-messages` 要求目标 generation 覆盖完整，否则返回 `index_required`。普通 `search` 可在索引不可用时回退到原有只读扫描，并在 coverage 中说明原因。

## 原子增量 consumer

每个 `--consumer` 对应账号私有目录中的一个原子 JSON 状态。名称只允许受限字符，不得使用路径。状态同时保存 base/target generation 及各自的 snapshot manifest SHA-256、已确认位置、待确认批次与创建/更新时间；generation 与摘要任一缺失或不匹配都会拒绝推进。

首次读取时：

- `--start now` 把当前 generation 作为基线，只等待后续成功发布的 generation；
- `--start beginning` 从空基线读取当前索引中的全部消息。

当 state 指向的新 generation 出现时，consumer 固定该 generation 为 target，并通过稳定排序键 `(timestamp, sort_seq, evidence_id)` 比较 base 与 target。相同 evidence 的内容摘要变化返回 `updated`；新 evidence 返回 `inserted`。删除不作为“新消息”返回。

poll 的事务顺序是：计算一批 → 原子写入 pending batch → 输出。进程在输出前后崩溃都不会推进确认位置；下一次 poll 重放同一个 `batch_id` 和项目。`--ack` 只接受当前 pending batch，确认后才推进位置。达到 target 末尾后，base 才原子前移到 target，并可继续观察下一个 generation。`--limit` 只切分批次，不跳过尾部。

不要自动 ack 尚未完整处理的批次。需要重新开始时必须由用户明确执行 `--delete --yes`；换 consumer 名称会建立独立消费进度。`gc` 会保留当前、回滚版本以及仍被 consumer 引用的 derived generation。

## immutable 查询 daemon

daemon 与 CLI 使用同一二进制。`serve` 是前台单实例服务，在 IPv4 loopback 的随机端口监听，并把 endpoint、PID 和随机 bearer token 写入当前用户私有状态目录；另一个 `serve` 会被操作系统锁拒绝。请求有大小、响应大小、并发数和 deadline 上限；endpoint 不是 loopback、token 不匹配或协议版本不符时拒绝。

daemon 白名单只包含不刷新、不联网、不导出、不改变派生状态的 snapshot 查询。它拒绝 `--fresh`、`--resolve-media`、setup、refresh、index build、new-messages、导出、联网正文/媒体请求等命令。history/search 在 daemon 中只合并 snapshot 内微信已有的语音文字，不读取可变的私有 ASR cache。缓存键包含账号、generation、snapshot manifest 摘要、派生索引身份、完整参数、本地日期和二进制版本，只有成功响应进入有界 LRU；generation、摘要或索引可用性改变后不会命中旧结果。

客户端全局 `--daemon` 的语义是“要求经 daemon 查询”，daemon 不可用时返回明确错误，不静默回退到本地执行。YAML/table 转换发生在客户端，daemon 内部协议仍是 JSON。

## 输出与证据

默认 JSON 是 Agent 和 daemon 的稳定协议。`--output yaml` 与 `--output table` 只用于人工阅读；table 会压缩长字段，不能当无损导出。所有查询仍应保留 `meta.generation_id`、snapshot manifest 摘要、coverage 与消息 `evidence_id`，跨 generation 比较时不得只按显示时间或正文猜测同一性。
