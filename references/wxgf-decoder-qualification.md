# WXGF 本地解码资格验证

当前公开 CLI 行为没有改变：聊天图片中的 WXGF 强候选继续返回
`decoder_unavailable`，不会伪装成 JPEG/PNG，也不会触发 CDN 请求或要求用户重复
打开同一张原图。

仓库中的 `internal/wxgfqual` 只用于回答一个更窄的问题：一个以 `wxgf` 开头的
本地明文中，是否存在值得交给隔离解码器试验的、边界受限的 HEVC Annex-B 单图
候选。检查器要求：

- 输入不超过 64 MiB，首个 Annex-B 起始码位于前 1 MiB；若更早出现伪造或非法
  起始码则失败，不跳过它寻找后续“看起来能解”的片段；
- NAL 头必须是 layer 0、合法 temporal id，且只接受已定义的 HEVC 类型；
- 首张图片之前必须依次存在可观察到的 VPS、SPS、PPS；
- 首个 VCL 必须是独立随机访问图片，按 `first_slice_segment_in_pic_flag` 只能识别
  到一张图片；同图多 slice 可以保留，第二张图片直接拒绝；
- NAL 数量有上限，空、截断、保留类型、参数集后置和终止后图片均拒绝。

这仍不是 WXGF 容器规范验证器，也不解析完整 SPS、PPS 或 slice 语法。成功结果的
方法名固定为
`wxgf_magic+bounded_prefix+annex_b_nal_headers+single_irap_picture`，只能进入解码
试验，不能直接进入图片导出。

## 外部线索的证据等级

原踩坑记录对 WXGF 的结论仍适合作为“不得把私有容器伪装成普通图片”的失败边界，
但不能单独证明当前 Windows 客户端的 WXGF 永远不可解。截至 2026-08-29，仍在维护的
[`wechatauto-replica` v1.1.3 记录](https://github.com/fanyuantaier/wechatauto-replica/blob/main/README_pypi.md)
称其于 2026-08-17 加入了 WXGF/HEVC 转码；对应
[`media.py` 实现](https://github.com/fanyuantaier/wechatauto-replica/blob/main/wechatauto/media.py)
只是从首个四字节起始码截取剩余数据、让 PATH 或 `imageio-ffmpeg` 中的 FFmpeg 输出
一张 JPEG，再检查 JPEG 文件头。它能提供“HEVC 路线值得实测”的近期线索，却没有
容器/NAL 边界、完整输出、单帧、二进制信任、网络隔离或许可验收，不能直接复用为
v-local-cli 的生产实现。这里的检查和真实样本试验均独立建立，不把该项目的成功声明
当作本项目证据。

## 实验适配协议

`RunProviderTrial` 通过标准输入向一个本地进程发送单行 JSON。协议版本为
`v-local-cli-image-decoder/1`；本阶段没有公开 CLI flag，也不会自动发现或下载适配器。

请求示意：

```json
{
  "protocol": "v-local-cli-image-decoder/1",
  "request_id": "<一次性随机值>",
  "action": "decode_still",
  "input_path": "<私有临时目录/input.hevc>",
  "input_format": "hevc_annex_b",
  "input_sha256": "<完整 SHA-256>",
  "output_path": "<私有临时目录/output.png>",
  "output_format": "png",
  "maximum_frames": 1,
  "maximum_pixels": 40000000,
  "network_allowed": false
}
```

成功响应示意：

```json
{
  "protocol": "v-local-cli-image-decoder/1",
  "request_id": "<原样回显>",
  "status": "decoded",
  "input_sha256": "<原样回显>",
  "output_sha256": "<PNG 完整 SHA-256>",
  "output_format": "png",
  "frame_count": 1,
  "network_used": false,
  "decoder": "ffmpeg",
  "decoder_version": "<固定版本>"
}
```

CLI 侧实验壳会限制请求/诊断输出长度和运行时间，在私有目录中只写入提取后的 HEVC，
拒绝符号链接、非普通文件、超过 64 MiB 的输出、未知 JSON 字段、多余 JSON、摘要不
匹配、联网声明和多帧声明。适配器输出必须是 PNG，并再次经过 Go 的完整 PNG 分块、
尾部、解码和 4000 万总像素上限验证。所有临时明文无论成功、失败或超时都必须清理。

如果实验适配器使用 FFmpeg，至少应显式使用原始 HEVC demuxer、本地文件协议白名单、
`-nostdin`、单视频流、单帧输出、无损 PNG 和错误即失败；不得依赖扩展名自动探测，
不得输出二次有损 JPEG。具体 argv 属于适配器实现与验收范围，不由主 CLI 猜测。
对应行为应以 FFmpeg 的
[格式文档](https://ffmpeg.org/ffmpeg-formats.html)、
[命令行文档](https://ffmpeg.org/ffmpeg.html)和
[协议白名单文档](https://ffmpeg.org/ffmpeg-protocols.html)为准。

## 尚未关闭的发布门禁

即使真实样本解码成功，实验结果仍固定为 `production_ready=false`，并保留以下门禁：

1. WXGF 容器布局没有稳定公开规范，当前只能保守识别 HEVC 候选；
2. 适配器二进制的签名、版本、来源与摘要尚未纳入发行信任链；
3. `network_used=false` 目前是协议声明，尚无操作系统级网络隔离证明；
4. 有超时和输出上限，但尚无进程级内存/子进程约束；
5. 真实夹具矩阵仍不足，尚未覆盖不同微信版本、更多静态图和多帧 WXGF；
6. 完整解码和颜色多样性只能排除明显的空白/单色故障，尚未与微信界面显示做视觉等价确认；
7. 解码器的可分发构建、来源、许可组合和对应源码提供方式尚未验收。

只有这些门禁全部关闭，才能讨论把适配器接入 `export-chat-image`。接入后仍必须保持
`quality_tier` 只描述微信缓存层级；解码像素宽高不证明发送前源图质量。

开发者可设置仅供测试使用的 `V_LOCAL_TEST_WXGF_FIXTURE`（已解密、去标识化的 WXGF
文件）与 `V_LOCAL_TEST_WXGF_PROVIDER`（实验适配器普通文件路径），运行：

```text
go test ./internal/wxgfqual -run TestRealWXGFFixtureQualification -v
```

上述独立文件测试未同时设置两个变量时必须跳过。若要复用当前不可变快照中的消息资源
与 hardlink 双重绑定，并保持 WXGF 只在内存中解密，可另外设置账号选择器：

```text
V_LOCAL_TEST_WXGF_ACCOUNT=<account> \
V_LOCAL_TEST_WXGF_PROVIDER=<qualification-provider> \
go test ./internal/store -run TestRealWXGFQualificationFromSnapshot -v -count=1
```

该测试找到第一个通过保守结构门禁的真实候选后即停止，不声称扫描了完整历史矩阵。
没有显式设置变量时必须跳过；普通 CI 不读取用户微信数据，也不下载 FFmpeg。

## 2026-08-29 单机资格记录

在当前 Windows/Weixin 4.1.12.55 验收快照中，测试通过消息资源 stem 与 hardlink
映射找到一个真实强关联 WXGF，整个过程没有输出联系人、消息正文、路径、evidence ID
或内容摘要：

- WXGF 明文 56,161 字节，首个 HEVC Annex-B 起始码位于偏移 38；
- 保守检查识别到 4 个 NAL、完整 VPS/SPS/PPS 和 1 张独立图片；
- 两遍 FFmpeg 流程先完整解码并用 framehash 确认总帧数恰好为 1，再生成 PNG；
- Go 侧完整 PNG 验证得到 1280×2774；这是解码输出观察值，不是源图质量声明；
- 4,160 个网格采样点包含 213 个粗粒度颜色桶，RGB 三通道都观察到完整跨度，
  因而不是纯白、纯黑或单色绿屏；这不能证明画面与微信显示语义等价。

本次只为资格验证临时下载 PyPI
[`imageio-ffmpeg 0.6.0`](https://pypi.org/project/imageio-ffmpeg/) Windows wheel。wheel 的
SHA-256 与 PyPI 发布页给出的
`02fa47c83703c37df6bfe4896aab339013f62bf02c5ebf2dce6da56af04ffc0a`
一致；提取出的 FFmpeg 7.1 二进制 SHA-256 为
`2ce797a0f88d7f067180338fb227f7b1928ea727bd9a4d7a1d022f7c52af71a3`。
该二进制声明 `--enable-gpl --enable-version3 --enable-static`；FFmpeg 的
[官方许可说明](https://ffmpeg.org/legal.html)明确指出启用 GPL 部分后整个 FFmpeg
适用 GPL，并列出发行时的构建与对应源码义务。因此它只用于本地一次性试验，不能按
当前形态直接作为 v-local-cli 的可分发解码组件。测试后必须删除 wheel、FFmpeg、
临时 provider 和所有解码输出。

这条记录把“WXGF 中的 HEVC 在当前真实样本上可解码”从外部项目线索提升为本机证据，
但样本量仍为 1，所有 `production_ready` 阻断项继续保留，公开 CLI 仍返回
`decoder_unavailable`。
