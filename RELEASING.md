# 发布流程

发布分为两个互不冒充的通道：

- `-dev.N` 是未签名 early-access，只能进入 GitHub prerelease 与 npm `next`。它使用
  `buildMode=candidate`，不要求 Authenticode、Developer ID 或 notarization，并把 Provider
  完整性明确报告为 `candidate_unverified`。
- 未来的 `-rc.N` 或稳定版本才进入 `.github/workflows/release.yml`。该通道继续要求
  平台签名、公证、最终签名件复验和 Trusted Publishing；无预发布后缀时才能进入
  npm `latest`。

## 未签名 early-access

1. 在 `main` 上确认 `Audit gates` 全绿，手动运行 `Release candidate`。
2. 需要创建可下载版本时，设置 `publish_unsigned_preview=true`，并把确认值精确填写为
   `PUBLISH_UNSIGNED_PREVIEW`。工作流只允许从 `main` 发布与 `npm/package.json` 完全一致的
   `-dev.N` 版本。
3. 发布作业会先通过 GitHub API 确认同一 `main` 提交存在成功的 `Audit gates`。工作流再以 `buildMode=candidate` 构建 Windows amd64、macOS amd64 和 macOS arm64 三个首发目标，生成校验和和 tgz，并为全部资产生成
   GitHub artifact attestation。发布作业会重新验证二进制摘要、tgz 内校验和与 attestation，
   随后创建明确标记为 unsigned early access 的 GitHub prerelease；已有标签或 Release
   一律拒绝覆盖。
4. Candidate CLI 会优先发现 Provider npm 安装器维护的当前用户固定目录，但不会把固定路径
   冒充平台签名；`provider status` 仍显示 `candidate_unverified`。显式开发 override 的原有
   授权边界不变。
5. 该通道不读取签名或 Apple 凭据，不执行 `npm publish`，也不会触发 `Signed release`。
   首次 npm 发布或后续人工 early-access 发布必须从这个确切 prerelease 下载 tgz、核对摘要，
   并在启用 2FA 的维护者终端发布到 `next`：

```sh
npm publish ./zanescope-v-local-cli-0.1.0-dev.1.tgz --access public --tag next
```

候选件虽然有内容摘要和 GitHub 来源证明，但没有平台签名信任；它只适合明确选择
early-access 的用户，不能提升为 `latest` 或替代正式签名件验收。

## 正式签名通道的一次性配置

仅在准备 signed release 时，才需要在 GitHub `release` environment 中配置并保护：

- `WINDOWS_SIGNING_CERTIFICATE_BASE64`、`WINDOWS_SIGNING_CERTIFICATE_PASSWORD`
- `MACOS_SIGNING_CERTIFICATE_BASE64`、`MACOS_SIGNING_CERTIFICATE_PASSWORD`
- `MACOS_CODESIGN_IDENTITY`
- `APPLE_NOTARY_APPLE_ID`、`APPLE_NOTARY_TEAM_ID`、`APPLE_NOTARY_APP_PASSWORD`

为该 environment 配置 required reviewer；批准前必须核对候选提交、三个首发目标摘要、
真机验收记录和当前能力声明。没有 ARM64 真机时保持 `darwin_arm64=build_only`，不得用
GitHub runner、签名成功或 notarization 成功代替真实微信数据验收。

该包按 npm 规则声明为 `dual-use`，`contentPolicy` 和包根目录的 `DISCLOSURE`
不得在后续版本删除。包完成首次 2FA bootstrap 后，可为正式工作流配置 Trusted Publisher：

```sh
npm trust github @zanescope/v-local-cli --file release.yml --repo zanescope/v-local-cli --env release --allow-stage-publish
```

正式流水线只提交 staged publish，不接受长期 npm token，也不直接绕过 2FA 发布。

## 正式签名发布步骤

1. 在 `main` 上确认 `Audit gates` 全绿并生成不可变候选，按支持平台清单完成真机验收；保存脱敏版本、候选摘要、generation 功能和签名检查结果，不保存账号、路径、正文或密钥。
2. 证书、公证和干净安装验收条件就绪后，将包版本提升到未来的 `-rc.N` 或稳定版本，再创建完全一致的标签。`-dev.N` 标签被明确排除在 Signed release 触发器之外。
3. `Signed release` 会调用完整 Audit gates，随后重新构建并启用发行门禁。Windows 注入 Authenticode 叶证书 SHA-256 后签名、时间戳并复核；macOS 注入固定 Developer ID Team，使用 Hardened Runtime、签名 DMG 与 notarization，并执行 staple/validate 和 Gatekeeper。工作流生成签名 manifest、notary log、`release-checksums.txt` 和正式来源证明，然后只创建 GitHub prerelease。
4. 下载这个确切 signed prerelease，从最终 tgz 在干净机器安装 CLI 和独立 Provider。确认固定路径、签名身份、helper sibling、daemon PID image、发行版 override 拒绝、协议失败边界、Keychain/Credential Manager、crash artifact 和卸载清理，再复验真实数据能力。只有签名 live evidence 通过后才人工提升 GitHub Release，并在核对同一 tgz 摘要后用 2FA 批准 staged npm publish。

证书资料尚未完成不会阻塞 unsigned early-access；但任何签名身份、公证、摘要、运行时信任、干净机器、真机验收或 Trusted Publishing 条件失败，仍必须阻塞 signed release 与 `latest`。签名与 CI 通过本身不改变 `build_only/unverified` 能力状态。
