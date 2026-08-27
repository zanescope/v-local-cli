# DAT 图片解密

图片候选包含 16 字节 ASCII AES 值和 0..255 的 XOR 值。候选可以来自用户文件或独立 Provider，但最终验证和解密都在主 CLI 完成。

- v1 魔数：`07 08 56 31 08 07`，使用固定 AES key。
- v2 魔数：`07 08 56 32 08 07`，使用账号 AES 候选。
- v3：没有上述魔数，使用逐字节 XOR。

v1/v2 头部包含小端序 AES 区长度和 XOR 尾长度。AES 区使用 AES-128-ECB，随后拼接原始区和 XOR 尾。输出必须再次匹配 JPEG、PNG、GIF、WebP 或 wxgf 容器魔数，否则视为失败。

## 聊天消息图片

从 `history` 返回的 `kind=image` 项取得 `evidence_id` 后，优先使用：

```text
v-local-cli export-chat-image --account <account> --output <file> <image_evidence_id>
```

CLI 会从当前不可变 generation 重新定位消息，以 `message_resource.db` 中的资源 stem 与 `hardlink.db` 映射共同证明候选归属，再对明文或 DAT 解密结果做完整容器解码。成功结果返回像素宽高、明文 SHA-256、字节数和 `verified_by=message_resource_stem+hardlink_map+full_decode`；不返回微信源路径且不联网。不同强候选产生不同明文时拒绝导出，不能按时间或邻近目录猜测“原图”。验收“高清图”必须为已知夹具设置最低宽高，并核对返回尺寸，不能把缩略图仅凭文件名当作原图。

## 独立 DAT 图片

`export-media` 仅用于用户已经明确提供的独立 DAT 路径；它不能单独证明文件属于某条历史消息：

```text
v-local-cli export-media --account <account> [--force] --output <file> <input.dat>
```

当前版本不调用 ffmpeg，也不转码 wxgf。若结果是 wxgf，`data.format` 会明确返回 `wxgf`，输出文件保留原容器。

## 朋友圈 CDN 图片

朋友圈 CDN 媒体不是 DAT 容器，也不使用聊天图片的 AES/XOR 候选。`export-moment-media` 对图片从媒体节点取得数字 seed，为完整响应生成 ISAAC-64 大端序密钥流并逐字节 XOR，再以严格完整图片解码验证。普通视频从同一条 XML 的外层 `<enc key>` 取得 seed，只 XOR 前 `min(131072, 文件长度)` 字节，随后验证 ISO BMFF 顶层盒边界及 `ftyp`、`moov`/`moof`、`mdat`。XML 中可用的长度和 MD5 只作描述符辅助依据，精确命中密文或明文时才提升验证等级。图片路径可由现存 [CipherTalk 图片实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1693-L1728) 与 [ISAAC64 实现](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/isaac64.ts) 交叉核对；视频的外层 key 与前 128 KiB 规则见其 [视频路径](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1378-L1399) 和 [解密代码](https://github.com/ILoveBingLu/CipherTalk/blob/e252f18de78450bb8976e1435711873b05d5f124/electron/services/snsService.ts#L1595-L1623)。

视频 CDN 兼容两代地址：旧的 `snsvideodownload*.tc.qq.com`，以及微信当前使用的 `*.video.qq.com/.../snsvideodownload`。后一类不会按 `*.video.qq.com` 整体放行：首级标签必须包含 `wxsns`，路径末段也必须精确匹配下载端点。该模式同时由本机真实描述符和公开的[朋友圈视频上传响应示例](https://doc.geweapi.com/api-139908345)佐证。

```text
v-local-cli export-moment-media --account <account> --output <file> <media_evidence_id>
v-local-cli export-moment-media --account <account> --allow-network --output <file> <media_evidence_id>
```

第一条命令只尝试本地缓存；第二条命令才授权本次受限 CDN 请求。解密路径不能互换：不要把朋友圈数字 seed 当作 DAT XOR 字节，不要把账号 DAT 密钥用于 CDN 响应，也不要把图片的全文规则套到视频。当前支持图片和普通视频，不展开实况照片的独立视频描述符。
