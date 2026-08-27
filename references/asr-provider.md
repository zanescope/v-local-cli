# v-local-cli-asr/1 本地适配协议

此协议允许用户在不增加主 CLI 运行时依赖的前提下，显式接入 SenseVoice 等离线 ASR。适配器必须是用户自行选择的本地可执行文件；v-local-cli 不下载适配器、原生运行时或模型。

项目提供独立的 `v-local-cli-sensevoice` 仓库实现：它使用 sherpa-onnx 的 Go 绑定读取本地 SenseVoice int8 模型，不改变主仓依赖边界。仓库中的 Windows amd64 真实语音记录未绑定当前候选件摘要，只能作为历史验收线索；普通 `capabilities` 或适配器自报元数据不能把它升级为当前发布能力证据。

调用方式：v-local-cli 启动适配器，不传命令行秘密，并向标准输入写入一行 JSON：

```json
{"protocol":"v-local-cli-asr/1","action":"transcribe","audio_path":"<临时 WAV>","source_audio_sha256":"<原始语音摘要>","sample_rate":16000,"channels":1,"language":"zh","model_path":"<本地模型路径>"}
```

适配器向标准输出返回且只返回一个 JSON 对象：

```json
{"protocol":"v-local-cli-asr/1","transcript":"识别文字","engine":"sherpa-onnx-sensevoice","model":"sensevoice-int8@sha256:<完整64位十六进制摘要>","language":"zh","network_used":false}
```

约束：

- `source_audio_sha256` 必须是规范的 64 位小写十六进制 SHA-256。它来自原始语音负载（SILK 解码前），仅作溯源标记；它**不是**输入 WAV 的摘要，适配器只拿到解码后的 WAV，因此只能校验格式，不能声称独立复算了该值。
- 输入音频是权限收紧的 16 kHz 单声道 WAV，调用结束后由 v-local-cli 删除；适配器不得复制或长期保留。
- `network_used` 必须为 `false`；报告联网的结果会被拒绝。CLI 能校验声明和约束自身行为，但无法替代操作系统级网络隔离，审计要求高时应在沙箱或防火墙规则下运行适配器。
- 适配器可执行文件和模型路径在调用前会转换为已解析符号链接的绝对本地路径，适配器工作目录固定为账号私有临时目录；适配器仍以当前桌面用户权限运行。
- 输出上限 2 MiB，未知字段、额外 JSON、空文字或协议版本不符都会被拒绝。
- `engine`、`model`、`language` 和 `network_used` 是适配器自报元数据；CLI 会做枚举、长度和协议检查，但通用协议不能证明模型内容或操作系统级离线性。SenseVoice 参考实现把固定模型文件名、长度和内容做域分隔完整 SHA-256；其他适配器不得把任意显示名描述为已验真的模型身份。
- 模型目录、运行时安装和下载均属于独立可选组件，必须先取得用户同意并校验来源及摘要。
