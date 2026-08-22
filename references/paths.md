# 路径

CLI 只把包含 `db_storage` 目录的父目录识别为微信账号目录。默认候选根：

| 平台 | 候选根 |
|---|---|
| Windows | `%USERPROFILE%\Documents\xwechat_files`、`WeChat Files`，以及 `%APPDATA%\Tencent` 下的微信目录 |
| macOS | `~/Library/Containers/com.tencent.xinWeChat/Data` 下的 Documents 和 Application Support 微信候选目录；当前仍需按[真机验收清单](macos-acceptance.md)确认实际微信版本布局 |
| Linux | `~/.xwechat`，主要用于显式测试数据 |

环境变量：

- `V_LOCAL_CLI_DATA_ROOT`：覆盖账号搜索根。
- `V_LOCAL_CLI_ACCOUNT_DIR`：直接指定一个账号目录；其中必须存在 `db_storage`。
- `V_LOCAL_CLI_HOME`：覆盖 v-local-cli 状态、快照、锁和私有临时文件根。
- `V_LOCAL_CLI_KEY_PROVIDER`：指定独立 Provider 可执行文件。
- `V_LOCAL_CLI_WHISPER_BIN`：指定用户已安装的本地 `whisper-cli`；也可每次传 `--engine`。
- `V_LOCAL_CLI_WHISPER_MODEL`：指定用户已下载的 whisper.cpp 多语言模型；也可每次传 `--model`。
- `V_LOCAL_CLI_ASR_PROVIDER`：指定用户选择的 `v-local-cli-asr/1` 本地适配器；也可每次传 `--asr-provider`。
- `V_LOCAL_CLI_ASR_MODEL`：指定本地 ASR 适配器的模型目录；也可每次传 `--model`。
- `V_LOCAL_CLI_SKILL_DIR`：仅供发布包指向已带摘要清单的 Skill bundle；不要指向不可信目录。
- `V_LOCAL_CLI_AGENT_SKILL_HOME`、`V_LOCAL_CLI_SKILL_HOME`：分别覆盖通用 Agent 与 Codex 的 Skill 安装根，主要用于隔离测试或受管环境。

npm 安装层还保留三个仅供本仓库构建、测试或受管镜像使用的变量：`V_LOCAL_CLI_SKIP_BINARY_INSTALL` 跳过 postinstall 二进制安装；`V_LOCAL_CLI_BINARY_PATH` 指向本地测试二进制，但只有同时设置 `V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY=1` 才生效。最后两个变量会绕过发布摘要链，只能用于明确隔离的开发环境，不得写入普通用户配置、生产镜像或发布说明中的推荐流程。

默认状态位于系统用户缓存目录的 `v-local-cli/`：

```text
accounts/
  <account_id>/
    state.json
    voice-transcripts.db
    ocr-texts.db
    inbox/
      <consumer>.json
    derived/
      <generation_id>/
        index-manifest.json
        message-index.sqlite
    snapshots/
      <generation_id>/
        manifest.json
        contact/contact.db
        message/message_*.db
        ...
    tmp/
daemon/
  endpoint.json
locks/
  <account_id>.lock
```

`state.json` 只指向一个已发布的不可变版本，并保留其 manifest 摘要；不包含密钥。`derived/<generation_id>` 是从对应 snapshot 原子构建的结构化消息索引，manifest 同时绑定账号、generation、snapshot 摘要和 parser/schema 版本；它不是微信源数据，也不能跨 generation 复用。`inbox` 保存每个 consumer 的原子增量位置和未确认批次；`gc` 会保留仍被游标引用的派生 generation。`daemon/endpoint.json` 保存 loopback endpoint、PID 和随机认证令牌，只对当前用户开放，`forget` 单个账号不会删除 daemon 控制状态。

`voice-transcripts.db` 保存用户选择生成的语音转写、音频摘要和本地引擎/模型名称，不保存原始语音；`ocr-texts.db` 保存 OCR 文字、图片摘要和证据元数据，不保存原始图片。`keychain` 模式把最小候选集合保存到当前桌面用户的系统凭据库。`tmp` 用于受限导出、视频验证、OCR 明文图片和本地 ASR 的临时 WAV/文本；成功或失败都会尽快删除临时文件，清理失败时命令会显式报错。

Windows 上会将 v-local-cli 私有目录的 DACL 限制为当前用户与 SYSTEM；其他平台使用 `0700`。加载、导出或删除前会拒绝私有路径层级中的符号链接、junction 或其他重解析点。

`accounts`、`status`、`doctor`、`provider status` 默认只返回账号标识和脱敏元数据。只有用户需要本机路径排错时才增加 `--show-paths`。
