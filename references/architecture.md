# 架构

```text
Agent → SKILL.md → v-local-cli CLI
                       ├─账号发现与 setup 授权
                       ├─候选格式检查和独立验证
                       ├─SQLCipher 主库/WAL 快照
                       ├─generation 结构化全文索引
                       ├─原子增量 consumer 游标
                       ├─loopback immutable 查询 daemon
                       ├─系统凭据库
                       ├─SQLite 查询、导出、DAT/SILK 解码
                       └─账号私有语音转写暂存
                                  │
                    明确授权后    │ stdin/stdout JSON
                                  ▼
                         外部密钥组件
                         └─只生成密钥候选
```

外部密钥组件是可选且单独安装的程序。主仓库不包含外部密钥组件源码或二进制，也不会在 npm 安装阶段静默安装它。

朋友圈图片和普通视频的可选远端路径不经过外部密钥组件：`export-moment-media` 先复用本地媒体验证；只有命令本次显式带 `--allow-network` 且本地未命中时，才把该媒体 XML 内的临时令牌发往按类型限制的腾讯 CDN。图片解密完整响应；普通视频使用同一条 XML 的外层 key，只解密前 131072 字节。普通查询、`--resolve-media`、公众号卡片和聊天导出都不会进入这条路径。

语音通过消息 `server_id` 精确关联快照 `media_*.db/VoiceInfo`，SILK 解码在 Go 二进制内完成。可选 whisper.cpp 由用户安装并显式配置，CLI 只把权限受限临时 WAV 交给本地子进程，成功或失败后删除；清理失败会使本次转写失败，成功文本才写入账号私有转写库。CLI 不自动下载模型，也不提供在线 ASR 回退。用户选择的外部密钥组件、whisper.cpp 和 ASR 适配器仍以当前桌面用户权限运行，结构化协议和自报离线状态不是操作系统沙箱。

Windows 实验 OCR 只从 Known Folder API 返回的 Program Files 根发现安装，拒绝环境变量重定向，并对 `WeChatOcr.bin` 解包执行路径、大小和 CRC 校验。为复用微信私有 Mojo 启动契约，OCR 子进程当前接收 `no-sandbox` 开关；因此它属于每张图片单独授权的本地执行边界，而不是受 CLI 隔离的不可信解析器。

公众号正文使用另一条逐篇授权的远端路径：`official-article` 从本地 `publication:` 证据重新解析卡片，只把清理后的公开标识发送到 `mp.weixin.qq.com`。它不读取会话 Cookie、不跟随重定向，不与本地卡片搜索或朋友圈 CDN 授权合并。

## setup 事务

1. 主 CLI 发现账号及 `db_storage`，再取得该账号的非阻塞操作系统文件锁；同账号已有 setup/refresh 时立即返回 `snapshot_busy`。
2. `--keys` 读取用户已有候选；`--allow-key-access` 才启动外部密钥组件。两者互斥。
3. CLI 绑定一次性 `request_id`，限制外部密钥组件响应大小并检查候选格式。
4. 图片候选使用真实 DAT 样本验证；setup 默认把它作为硬条件，只有显式 `--database-only` 才允许 database-only 流程。
5. 每个数据库和 WAL 先流式复制为大小受限的稳定私有副本，再用 SQLCipher 首页验证并逐页解密；不会把完整大库一次性读入内存。
6. CLI 校验 WAL 头、salt、帧校验和与最后一次提交，只回放完整提交。
7. 完整快照先写入私有暂存目录，生成包含版本标识、创建版本、源文件大小/修改时间、明文 SHA-256 和覆盖汇总的 manifest，再原子改名为不可变 `snapshots/<generation_id>`。源大小和修改时间只用于记录稳定复制时的本地状态，不是密码学来源签名。
8. CLI 在账号状态提交前为候选 generation 构建账号私有结构化消息索引；索引 manifest 绑定账号、generation、snapshot manifest 摘要及 parser/schema 版本。索引只从已发布候选目录读取，先写 staging 再原子发布；失败不会把半成品标记为 ready。
9. 账号状态最后提交当前版本指针；失败时删除新版本、对应派生索引并恢复先前凭据。成功后保留当前版本与一个回滚版本。
10. `keychain` 只保存数据库及图片密钥；切换为 `snapshot-only` 时删除旧凭据。setup 和 refresh 默认都阻止数据库覆盖率倒退。

外部密钥组件的退出状态和自报诊断都不能代替主 CLI 验证。

## refresh 事务

1. CLI 从已初始化状态选择账号，并要求当前发现的账号目录与原始账号路径及派生账号标识完全一致。
2. 在读取系统凭据前取得账号级非阻塞操作系统文件锁；锁冲突时返回 `snapshot_busy`，进程退出或异常终止后由操作系统释放锁状态。
3. 只从当前桌面用户的系统凭据库读取 setup 已保存的验证密钥，不启动外部密钥组件、不访问微信进程、不联网。
4. 使用与 setup 相同的主库/WAL 稳定读取和 SQLCipher 验证，在私有暂存目录构造候选快照。
5. 按规范化相对路径比较当前快照与候选中的数据库文件集合。候选缺少任一旧数据库时返回 `snapshot_coverage_regression`，删除候选暂存目录且不切换快照、不更新状态。
6. 集合未减少时发布不可变目录，为新 generation 构建绑定摘要的派生消息索引，再原子提交状态中的 `generation_id` 和 manifest 摘要。不改写系统凭据；仅新增分片没有已保存密钥时标记为 `skipped`，整体状态为 `partial`，需要完整覆盖时重新 setup。

覆盖保护只防止整个数据库文件从候选快照消失，不根据文件大小推断历史完整性，也不证明同名数据库内部的行集合单调增加。

## 查询边界

查询只连接已发布的明文快照，SQLite 连接同时使用 `mode=ro`、`immutable=1` 和 `query_only`。联系人按字段探测读取；消息表按 `Msg_<md5(username)>` 定位；zstd 压缩正文在内存中受限解码。

聊天查询按本地时区解析共享时间窗：个人或公众号默认当前自然月，群聊和跨会话搜索默认当前自然日；显式日期或 `--all` 可以覆盖默认值，实际范围回显在 `meta.time_window`，证据版本回显在 `meta.generation_id` 与 manifest 摘要。

`stats` 直接扫描选定时间窗内的类型、发送者编号和时间字段，不加载正文；系统消息从发言统计中排除并单独计数。

朋友圈查询使用 `SnsTimeLine` 表列确认作者和记录 ID，再解析同一行 `TimelineObject` 的正文、时间、位置、链接与逻辑媒体，并从根节点 `LocalExtraInfo` 解析本地可见的点赞、评论、回复引用和评论图片。`--resolve-media` 对原帖与评论媒体都只接受对应 XML 节点派生的 MD5/资源键或 hardlink 映射，随后验证容器或解密结果；不以时间、目录或文件修改时间猜测归属。

每个朋友圈媒体生成不含令牌和密钥的 `evidence_id`。独立导出命令用该标识重新绑定快照记录，身份冲突、多义记录或证据缺失都会终止。远端图片访问固定 HTTPS 域名白名单、无代理/无 Cookie/无重定向，并在连接前验证 DNS 公网地址；响应限时限量，ISAAC-64/XOR 解密后还必须通过容器、可用长度与 MD5 验证，最终文件才会原子发布。

公众号卡片查询只扫描独立 `biz_message` 分片，解析本机留存的 `mmreader/category/item` 多图文卡片。它不打开卡片 URL，也不把卡片元数据声明为文章正文或完整发布历史。独立正文命令必须用同一快照证据重新定位文章，并经过逐篇网络授权、目标解析、响应限制和正文节点验证。

结构化搜索优先使用当前 generation 的派生消息索引。索引逐表探测所有可识别消息表，保存稳定证据标识、内容摘要、发送者、提及、引用、语音转写和文本型详情；token、secret、key 一类字段排除在全文文本之外。FTS tokenizer 不可用时按 manifest 明确降级。索引缺失或绑定无效时，普通 `search` 可回退到原有已发现联系人扫描，并在 `search_backend_status` 说明其不完整范围；绝不把旧 generation 索引冒充当前结果。

`new-messages` 通过稳定排序键比较两个不可变 generation，返回新增或内容摘要变化的证据。每个 consumer 固定 base/target generation 及对应 snapshot manifest 摘要，poll 先原子持久化 pending batch 再输出，只有相同 `batch_id` 被 ack 后才推进位置，因此是 at-least-once，不是实时监听。批次上限只切分结果，不会跳过尾部；绑定不符或覆盖不完整的 generation 不允许推进。

可选查询 daemon 与 CLI 使用同一二进制，只绑定 IPv4 loopback，并用当前用户私有随机令牌认证。它只接受不刷新、不联网、不导出、不解析账号源媒体、不读取可变私有 ASR cache、不改变游标或索引的 immutable generation 查询。成功响应的有界缓存键包含账号、generation、snapshot manifest 摘要、派生索引身份、参数、本地日期和二进制版本；状态或索引可用性切换后旧缓存不会命中。客户端 YAML/table 只改变展示，daemon 协议仍为 JSON。

`--all` 且没有显式 `--limit` 时不设置结果条数上限；结果仍只代表当前 generation、`meta.database_coverage_status` 和命令回显的领域限定状态。全量 `export` 通过账号私有临时 SQLite 完成跨分片排序，再流式写最终 JSON/JSONL，避免把全部消息装入内存，结束后立即删除暂存库。

用户可见输出默认采用仅在目标不存在时发布的语义；`export`、`export-media`、`export-moment-media` 和诊断包只有显式 `--force` 才覆盖。输出目标是符号链接、Windows 重解析点或特殊文件时始终拒绝；硬链接覆盖通过发布新的普通文件打断链接关系，不修改其他名字指向的内容。写入与覆盖备份都使用目标目录内随机独占的兄弟临时文件，避免固定 PID、`.tmp` 或 `.old` 名称造成抢占和误删。

## 平台验证

构建成功、CI 测试和真实设备验证是三个不同层级，而且按架构、微信版本和候选件摘要分别判断。仓库内的历史说明不具备发布签名，也不能作为当前构建的 live evidence；普通 `capabilities` 因此统一返回 `validation_evidence.status=not_embedded`，不自报任何 `real_device_verified` 结论。macOS arm64 的 CI 只验证普通执行、Unix 权限、锁、路径和发布语义；候选文件仍可进入独立验证的只读快照流程。任何架构的自动 Provider、索引布局或 OCR 声明都必须由与当前二进制摘要绑定的签名发布证据升级，流程见[macOS 真机验收清单](macos-acceptance.md)。动态 Hook 在 arm64 与 x86_64 上读取的寄存器不同，结论不能跨架构外推。

## 安装层

核心发行物是预编译 Go 二进制。`@zanescope/v-local-cli` 只使用 Node 标准库：识别平台、限制 GitHub Release 域名和 128 MiB 响应上限、验证 SHA-256、原子安装二进制并转发命令及退出码。每次启动重新验证摘要。

Agent Skill 与 references 随 npm 包进入 `skill/`，其逐文件 SHA-256 写入 `skill-manifest.json`。`v-local-cli install` 只从该本地清单验证的 bundle 原子安装，不执行额外的 `npx skills` 或其他未固定版本安装器。

## 私有状态与删除

当前状态格式为 v2，只接受同版本且账号标识匹配的状态文件，不执行隐式旧版本迁移。提交先把上一版改名为 `.old`，再发布随机临时文件；若第二步失败会回滚，若进程在两步之间终止，后续加载会从同版本 `.old` 只读恢复并忽略未提交临时文件。账号目录必须位于 v-local-cli 私有根目录内；Unix 拒绝符号链接，Windows 使用当前用户与 SYSTEM 专属 ACL，并拒绝重解析点。

`gc` 只删除未完成暂存目录和超出策略的不可变版本，保留当前、一个回滚版本以及仍被增量 consumer 引用的派生 generation；语音转写、OCR 暂存和有效 consumer 不是旧版本，不会被 `gc` 删除。`forget` 先要求 dry-run，再用 `--yes` 删除精确账号目录（包括派生索引、consumer、转写和 OCR 暂存）与系统凭据；锁文件位于独立私有锁目录，因此删除账号数据时仍保持互斥。
