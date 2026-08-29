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
`v-local-cli-image-decoder/2`；本阶段没有公开 CLI flag，也不会自动发现或下载适配器。

宿主不再直接执行操作者给出的 provider 文件。provider 必须与 `ffmpeg.exe` 位于同一
普通目录，并存在 `<provider>.manifest.json`。邻接清单协议为
`v-local-cli/wxgf-provider-identity-manifest/v1`，固定记录两个文件名、两个 SHA-256，及
`provider_source_status=unverified`、`decoder_source_status=unverified`、
`decoder_distribution_license_status=not_qualified`。Windows 可用以下脚本在一个全新的
资格目录中独占创建清单；脚本拒绝覆盖、链接/reparse point、非邻接文件和非
`ffmpeg.exe` 解码器，也拒绝 `\\server`、`//server` 与设备路径，避免路径解析触发
网络访问：

```powershell
pwsh -NoProfile -File .\scripts\new-windows-wxgf-provider-identity-manifest.ps1 `
  -Provider '<absolute-qualification-provider.exe>' `
  -Decoder '<same-directory\ffmpeg.exe>'
```

清单是操作者选择的“预期身份”，不是签名或来源信任根。宿主严格解析清单并拒绝重复或
未知字段；Windows 逐级拒绝 symlink/junction/reparse point，其他测试平台先把系统路径
别名解析为无链接规范路径；随后边读边计算 provider 与 FFmpeg
摘要；只有摘要完全一致才把确切字节复制进私有 staging。实际启动的是 staging 中的
provider，FFmpeg 也来自同一 staging；请求前和进程退出后再次计算清单、provider、
FFmpeg 三个摘要。源目录随后变化不会改变已经复制的本次 staging 字节；宿主前后检查能
观察到的 staging 变化会阻断结果。但同一用户权限下的恶意 provider 仍可能篡改后恢复，
也可能根本不调用邻接 FFmpeg，因此这不是执行路径证明。清单也未签名，仍不能证明来源
可信或许可合规，`provider_binary_trust_status=unverified` 保持不变。

请求示意：

```json
{
  "protocol": "v-local-cli-image-decoder/2",
  "request_id": "<一次性随机值>",
  "action": "decode_still",
  "input_path": "<私有临时目录/input.hevc>",
  "input_format": "hevc_annex_b",
  "input_sha256": "<完整 SHA-256>",
  "output_path": "<私有临时目录/output.png>",
  "output_format": "png",
  "maximum_frames": 1,
  "maximum_pixels": 40000000,
  "network_allowed": false,
  "provider_identity_manifest_sha256": "<宿主计算的清单 SHA-256>",
  "provider_sha256": "<宿主计算的 staging provider SHA-256>",
  "decoder_name": "ffmpeg",
  "decoder_sha256": "<宿主计算的 staging FFmpeg SHA-256>",
  "decoder_identity_basis": "host_staged_manifest_bound_provider_and_decoder_sha256"
}
```

成功响应示意：

```json
{
  "protocol": "v-local-cli-image-decoder/2",
  "request_id": "<原样回显>",
  "status": "decoded",
  "input_sha256": "<原样回显>",
  "output_sha256": "<PNG 完整 SHA-256>",
  "output_format": "png",
  "frame_count": 1,
  "network_used": false,
  "decoder": "ffmpeg",
  "decoder_version": "sha256:<宿主计算的 staging FFmpeg SHA-256>"
}
```

CLI 侧实验壳会限制请求/诊断输出长度和运行时间，在私有目录中只写入提取后的 HEVC，
拒绝符号链接/reparse point、非普通文件、超过 64 MiB 的输出、未知 JSON 字段、多余 JSON、摘要不
匹配、联网声明和多帧声明。适配器输出必须是 PNG，并再次经过 Go 的完整 PNG 分块、
尾部、解码和 4000 万总像素上限验证。所有临时明文无论成功、失败或超时都必须清理。

Windows 资格验证进程另有一层可查询的 Job Object 约束：provider 先以挂起状态创建，
成功加入 Job 后才恢复主线程，避免进程在资源限制生效前执行；Job 最多同时容纳 2 个
进程（provider 与一个顺序执行的 FFmpeg），单进程提交内存上限为 512 MiB，Job 总提交
内存上限为 768 MiB，且关闭最后一个 Job handle 时终止整个进程树。没有设置 breakaway
许可；自动测试会查询实际 limit flags，并让 provider 派生一个延迟写文件的子进程，
确认 provider 结束后该子进程不能存活。微软文档也明确说明，Job 默认包含
[CreateProcess 子进程](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)，
但进程加入 Job 前已经发生的内存操作不受追溯检查，因此这里必须采用
[挂起创建后再分配](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-assignprocesstojobobject)。

这只能称为“`CreateProcess` 子树与 Job 成员内存约束”，不能称为完整沙箱。微软文档
特别注明 `Win32_Process.Create` 创建的进程不会自动加入 Job；借助系统服务或其他代理
触发的进程同样不应被本项目推断为 Job 成员。Job Object 也不会默认隔离网络、文件
系统或用户凭据；这些能力属于
[AppContainer 隔离](https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation)
等安全边界。Windows 11 新增的
[Create Process in Sandbox API](https://learn.microsoft.com/en-us/windows/win32/secauthz/createprocessinsandbox)
可以组合 AppContainer、网络和文件授权，但截至本记录仍标为 experimental、接口可能
变化，因此本阶段不把它接入生产路径，也不借此提前关闭对应门禁。

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
2. provider/解码器摘要已绑定单次 staging 身份，但签名、发布者与来源尚未纳入发行信任链；
3. `network_used=false` 目前是协议声明，尚无操作系统级网络隔离证明；
4. 尚无操作系统级文件系统与用户凭据隔离证明；
5. 非 Windows 平台、或 Windows Job Object 建立失败时，`CreateProcess` 子树与 Job 成员内存约束仍未建立；
6. 真实夹具矩阵仍不足，尚未覆盖不同机器、微信版本、缓存档位和真实多帧 WXGF；
7. 完整解码、颜色采样和感知哈希只能排除一部分明显故障，尚未与微信界面显示做视觉等价确认；
8. 解码器的可分发构建、来源、许可组合和对应源码提供方式尚未验收。

当前 Windows 实现只有第 5 项在 Job Object 成功建立后从结果中移除，其余 7 项继续
返回。任一 Job 创建、配置、分配或恢复步骤失败都会 fail closed，不会退化为无约束
执行。

只有这些门禁全部关闭，才能讨论把适配器接入 `export-chat-image`。接入后仍必须保持
`quality_tier` 只描述微信缓存层级；解码像素宽高不证明发送前源图质量。

开发者可设置仅供测试使用的 `V_LOCAL_TEST_WXGF_FIXTURE`（已解密、去标识化的 WXGF
文件）与 `V_LOCAL_TEST_WXGF_PROVIDER`（实验适配器普通文件路径），运行：

```text
go test ./internal/wxgfqual -run TestRealWXGFFixtureQualification -v
```

`V_LOCAL_TEST_WXGF_PROVIDER` 指向的文件必须带有上述邻接清单和同目录 `ffmpeg.exe`；
缺少清单、文件或摘要不符时在 provider 启动前 fail closed。上述独立文件测试未同时
设置两个变量时必须跳过。若要复用当前不可变快照中的消息资源
与 hardlink 双重绑定，并保持 WXGF 只在内存中解密，可另外设置账号选择器：

```text
V_LOCAL_TEST_WXGF_ACCOUNT=<account> \
V_LOCAL_TEST_WXGF_PROVIDER=<qualification-provider> \
V_LOCAL_TEST_WXGF_SAMPLE_TARGET=3 \
go test ./internal/store -run TestRealWXGFQualificationFromSnapshot -v -count=1
```

`V_LOCAL_TEST_WXGF_SAMPLE_TARGET` 默认为 1，只接受 1..5。测试会对明文内容做仅存于
内存的 SHA-256 去重，并要求计入矩阵的解码结果具有不同的 64 位感知指纹；指纹本身
不会输出。零距离只是“指纹相同”，不能解释为像素完全相同；非零距离也不证明原图
质量或视觉等价。测试达到目标后即停止，不声称扫描了完整历史矩阵。
没有显式设置变量时必须跳过；普通 CI 不读取用户微信数据，也不下载 FFmpeg。

## 人工视觉等价复审

解码成功不能单独关闭 `decoded_visual_equivalence_not_confirmed`。仅供资格验证的人工
流程使用以下三部分，均不进入公开 CLI 或发行包：

- `scripts/new-windows-wxgf-visual-review-session.ps1` 创建一个既有、空、禁止继承且
  仅当前用户、SYSTEM、Administrators 可访问的短期目录；
- 快照测试显式设置 `V_LOCAL_TEST_WXGF_REVIEW_ROOT` 后，把 `decoded-NN.png` 与私有
  `capture.json` 写入该目录。该变量必须与 `V_LOCAL_TEST_WXGF_PROVIDER` 同时设置；
- `internal/wxgfqual/cmd/visual-review` 生成无脚本、只含 `data:` 图片的离线 HTML，
  `scripts/accept-windows-wxgf-visual-equivalence.ps1` 再要求操作者逐样本确认内容、方向、
  完整裁剪和颜色/解码伪影四项。任一项不确定即不能记录通过。

参考图必须是从 `capture.json` 绑定的同一条微信消息原图界面人工取得的完整图片内容，
以 `reference-NN.png` 保存。页面按各自像素显示并允许滚动；宽高只用于验证 PNG 结构
与帮助查看，不参与质量判定。浏览器显示也不是色度学证明。成功或拒绝后，脚本删除
`capture.json`、解码 PNG、参考 PNG 和 HTML；但是删除磁盘文件不能证明浏览器历史、
缓存或进程内存已经擦除，因此报告固定保留 `browser_cache_erasure_proven=false`。
若用户拒绝或没有准备参考图，可直接使用 `-ReviewMode Skip`：helper 只校验 capture 与
解码图绑定，随后清理，不要求 CLI、账号、参考图或浏览器，也不会生成复审记录。

部分沙箱化浏览器不能读取仅当前用户可访问的复审目录。只有操作者明确接受短时扩大
本机读取面时，才可额外传入既有的本地固定磁盘 `-BrowserDisplayRoot`。脚本不会修改
私有复审目录 ACL，而是在所选位置创建受保护的随机子目录。当前用户、SYSTEM、
Administrators 是唯一可写主体；展示根 ACL 中实际具有读取权限的机器/域特定
`S-1-5-21-*` 主体会被复制为只读，Windows 应用包也仅获读取/执行权限。
`Authenticated Users`、`BUILTIN\Users` 等通用本地用户组不会被复制。根目录存在适用的
deny 或 Everyone/匿名/Guests 读取、位于非固定磁盘、与私有目录重叠、没有合格读取主体
或包含 reparse point 时直接拒绝。这能兼容 Codex 等宿主专用读取组，同时不会继承它们
的写权限。

每次只创建一份不覆盖既有文件、与私有 HTML 摘要一致的展示副本；收到输入后先复核
副本未变并立即删除，最后删除随机子目录。内容变化或任何清理失败都会阻止通过。普通
脱敏报告只记录 `browser_display_copy_used`、
`browser_display_access_basis=explicit_local_root_readers_downgraded_to_read_only` 与
`temporary_browser_display_artifacts_removed`，固定 `browser_display_path_included=false`。
展示窗口内所选根的合格读取主体仍可能读取该份明文页面，所以这不是默认行为，也不能
证明浏览器缓存已擦除；应使用专用、本地、非同步目录，先用无微信数据自检验证，确认
结束后检查其为空。

私有复审记录会保留 evidence ID、快照 generation/manifest、WXGF/解码/参考图摘要和
解码图 64 位感知指纹；这些值不得上传。可分享矩阵只保留计数与 blocker，不含 evidence
ID 或图片内容摘要。感知指纹只用于保守去重：矩阵同时要求 `high/medium` 复审中至少
4 个不同 WXGF 摘要和 4 个不同解码感知指纹；`thumbnail` 只保留诊断计数，不得填充
多样性门禁，也不能把相同画面的不同编码凑成 4 个样本。感知指纹仍不是质量分数。

矩阵还要求至少两个“复审时安装的微信版本”，且每个版本都有人工作证实的 `high` 与
`medium` 缓存档位。这里必须同时保留下列限制：

- 版本依据固定为 `installed_package_at_review_not_source_provenance`；它不证明缓存文件
  由该版本生成，源文件生产版本仍为 `unknown`；
- 档位依据固定为 `hardlink_cache_filename_variant_not_source_quality`；`high` 只是微信
  缓存命名层级，不证明发送前源图精度或质量；
- v2 记录同时按宿主计算的清单、provider、FFmpeg SHA-256 分组，不把不同 provider 或
  解码器构建拼成一组；provider 的响应只能精确回显宿主给定身份，不能选择矩阵身份；
- v1 记录只含 provider 自报的相邻解码器摘要，评估时计入
  `legacy_records_excluded`，不得静默补字段或升级为 v2 证据；
- 清单仍未签名且来源/许可未验收，所以 `provider_binary_trust_status=unverified`、
  `provider_source_status=unverified`、`decoder_source_status=unverified`、
  `decoder_distribution_license_status=not_qualified`；
- 矩阵即使为 `pass`，范围也只是 `human_visual_equivalence_only`，并固定
  `production_ready=false`、`fixed_dimension_quality_gate=false`。

示意流程如下，路径与账号都属于本机私有输入：

```powershell
$qualificationProvider = '<absolute-qualification-provider.exe>'
$qualificationFFmpeg = '<same-directory\ffmpeg.exe>'
pwsh -NoProfile -File .\scripts\new-windows-wxgf-provider-identity-manifest.ps1 `
  -Provider $qualificationProvider -Decoder $qualificationFFmpeg
$session = pwsh -NoProfile -File .\scripts\new-windows-wxgf-visual-review-session.ps1 -ShowPaths | ConvertFrom-Json
$env:V_LOCAL_TEST_WXGF_ACCOUNT = '<account>'
$env:V_LOCAL_TEST_WXGF_PROVIDER = $qualificationProvider
$env:V_LOCAL_TEST_WXGF_SAMPLE_TARGET = '2'
$env:V_LOCAL_TEST_WXGF_REVIEW_ROOT = $session.review_root
go test .\internal\store -run '^TestRealWXGFQualificationFromSnapshot$' -v -count=1

# 人工保存 reference-01.png、reference-02.png 后：
pwsh -NoProfile -File .\scripts\accept-windows-wxgf-visual-equivalence.ps1 `
  -Helper '<visual-review-helper>' -Cli '<current-v-local-cli>' `
  -Account '<account>' -ReviewRoot $session.review_root -ReviewMode Prompt

# 仅在默认浏览器读不到严格 ACL 目录且操作者明确接受上述短时本机读取面时：
# 若当前仓库位于同步盘，必须改用另一个已通过下方自检的本地非同步目录。
$browserDisplayRoot = Join-Path $PWD '.codex-temp\wxgf-browser-display'
New-Item -ItemType Directory -Path $browserDisplayRoot -Force | Out-Null
# 先用不含微信数据的页面确认浏览器确实能读取，并按提示输入一次性 challenge：
pwsh -NoProfile -File .\scripts\accept-windows-wxgf-visual-equivalence.ps1 `
  -SelfTest -BrowserDisplayRoot $browserDisplayRoot
pwsh -NoProfile -File .\scripts\accept-windows-wxgf-visual-equivalence.ps1 `
  -Helper '<visual-review-helper>' -Cli '<current-v-local-cli>' `
  -Account '<account>' -ReviewRoot $session.review_root -ReviewMode Prompt `
  -BrowserDisplayRoot $browserDisplayRoot

# 或者拒绝复审并立即清理；这一分支不需要 -Cli、-Account 或参考图：
pwsh -NoProfile -File .\scripts\accept-windows-wxgf-visual-equivalence.ps1 `
  -Helper '<visual-review-helper>' -ReviewRoot $session.review_root -ReviewMode Skip
```

本流程不操作微信 UI、不请求 CDN，也不把用户同意解释为联网授权。即使描述符刚被
观察到，它也可能在询问、打开原图、refresh 或首次请求前失效；在没有已验真的真实
协议请求前只能继续报告 `present_expiry_unknown`。

## 2026-08-29 单机资格记录

在当前 Windows/Weixin 4.1.12.55 验收快照中，测试通过消息资源 stem 与 hardlink
映射找到 3 个明文内容不同的强关联 WXGF。为避免对 18,017 个附件文件按消息反复遍历，
资格测试先建立一次不超过生产扫描上限的文件名索引；单元测试确认该索引返回的既存
候选集合与生产用的有界补扫一致。整个过程没有输出联系人、消息正文、路径、
evidence ID、明文摘要或感知指纹：

| 样本 | 缓存档位 | WXGF 字节 | HEVC 偏移 | NAL / 图片 | PNG 尺寸 | 采样点 / 粗色桶 |
| --- | --- | ---: | ---: | --- | --- | --- |
| 1 | medium | 56,161 | 38 | 4 / 1 | 1280×2774 | 4,160 / 213 |
| 2 | medium | 144,654 | 42 | 4 / 1 | 1280×1706 | 4,224 / 106 |
| 3 | medium | 4,828 | 38 | 4 / 1 | 684×188 | 6,486 / 92 |

三项都由两遍 FFmpeg 流程先完整解码并用 framehash 确认总帧数恰好为 1，再生成
无损 PNG，随后由 Go 再做完整结构与像素上限验证。三个感知指纹没有零距离，最小
Hamming 距离为 19，只能说明本次矩阵不是同一低成本指纹的重复结果。尺寸、颜色跨度、
缓存档位和指纹距离都不能表示发送前源图质量，也不能证明画面与微信显示语义等价。
本次 3 个样本都属于 `medium`，且来自同一机器和客户端版本，因此仍不足以关闭真实
夹具矩阵门禁。

同日，在操作者先后于微信中打开两张原图并使用已保存凭据执行一次无进程访问、无网络
的 `refresh --require-media` 后，又按不同 evidence ID 重采样 2 个 WXGF：分别为
60,705 字节、1080×2338 和 56,161 字节、1280×2774，感知指纹最小 Hamming 距离为
23。两者仍都只命中 `medium`，没有观察到 `high` 本地缓存。这不能证明 CDN 已过期，
但直接否定了“用户打开原图后必然落盘 high”的实现假设；Agent 仍只能做一次绑定同一
evidence 的 refresh/重试，然后停止并报告可能过期或不可用。

操作者查看了解码输出并认为画面正确，但选择跳过保存独立参考 PNG。按上述门禁，本次
记录为 `inconclusive/skipped`，临时图片与 capture 已清理，确认样本数为 0，矩阵未
评估；主观观察没有被升级为视觉等价证据。

随后操作者补齐独立参考图并对新的私有复审会话重跑。两张参考图均从微信“原图”另行
转存并通过严格 PNG 结构校验；“原图”标签仍不能证明发送前源图精度，故
`source_original_quality_status=unknown` 保持不变。本轮两个不同 WXGF 仍都属于
`medium`：45,525 字节、解码为 1180×686，以及 136,403 字节、解码为
1180×2556；两个解码感知指纹的 Hamming 距离为 30。字节数、尺寸、档位和指纹只用于
样本区分，不作为图片质量门槛。

操作者在每个一次性离线并排页上分别用随机 challenge 精确确认了内容、方向、裁剪及
颜色/解码伪影。脱敏报告因此记录 `sample_review.status=confirmed`、
`samples_confirmed=2`，四项确认均为 true，且私有 capture、参考图、解码图和复审页均
已清理；`browser_cache_erasure_proven=false` 仍明确保留。公开报告不含账号、
evidence ID、图片内容摘要或源路径，未上传私有记录，也未执行网络访问或微信 UI 自动化。

正式矩阵仍为 `insufficient`：`distinct_wxgf_samples=2`、
`distinct_decoded_visual_fingerprints=2`、复审时安装版本数为 1，且观察档位为
`high=0`、`medium=2`，没有任何版本同时覆盖 `high+medium`。因此四样本、两版本及
每版本双档位门槛均未满足，`production_ready=false`。这次确认只支持这两个样本在当前
解码器构建下的人工视觉等价，不证明其他 WXGF、源图质量、未来缓存可用性或 CDN 描述符
仍有效；描述符时效继续按 `present_expiry_unknown` 处理。

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

这条记录把“WXGF 中的 HEVC 在当前真实样本上可解码”从单样本提升为同版本多样本
证据，并新增了两个具有独立微信参考图的正式人工视觉确认；两者仍不能关闭完整矩阵。
记录同时关闭了 Windows 资格进程的“无 `CreateProcess` 子树/Job 成员内存约束”单项
缺口。由于其余 7 个
`production_ready` 阻断项仍存在，公开 CLI 继续返回 `decoder_unavailable`；本地解码
结论也不会改变 CDN 描述符的 `present_expiry_unknown` 状态或启用任何远端请求。

随后身份协议升级为 provider v2、capture/record/matrix/helper 及脱敏 evidence report
v2。上述两条正式人工确认是在升级前生成的 v1 历史证据：它们仍能说明当时两个样本的
人工观察，但只绑定了未受信任 provider 自报的 FFmpeg 摘要。新 helper 会识别并计数这类记录为
`legacy_records_excluded`，不会把它们拼入宿主计算的清单 + provider + FFmpeg 身份矩阵，
也不会原地重写私有记录。因此当前 v2 矩阵尚未用真实样本评估；若以后确有必要继续资格
验证，必须从新的宿主 staging capture 开始并重新取得独立参考图与一次性人工确认。
这次协议升级不改变历史 v1 的 `high=0`、`medium=2` 观察，也不把它升级为生产证据。
