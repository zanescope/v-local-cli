# 发布流程

正式发布只从版本标签触发 `.github/workflows/release.yml`。标签必须与
`npm/package.json`、运行时 `--version` 和 README 声明完全一致。当前预发布版本
使用 `v0.1.0-dev.1` 并进入 npm `next`；没有预发布后缀的版本才进入 `latest`。

## 一次性配置

在 GitHub `release` environment 中配置并保护：

- `WINDOWS_SIGNING_CERTIFICATE_BASE64`、`WINDOWS_SIGNING_CERTIFICATE_PASSWORD`
- `MACOS_SIGNING_CERTIFICATE_BASE64`、`MACOS_SIGNING_CERTIFICATE_PASSWORD`
- `MACOS_CODESIGN_IDENTITY`
- `APPLE_NOTARY_APPLE_ID`、`APPLE_NOTARY_TEAM_ID`、`APPLE_NOTARY_APP_PASSWORD`

为该 environment 配置 required reviewer；批准前必须核对候选提交、六个平台摘要、
Apple Silicon 真机验收记录和当前能力声明。没有 ARM64 真机时保持
`darwin_arm64=build_only`，不得用 GitHub arm64 runner、签名成功或 notarization 成功
代替真实微信数据验收。

该包按 npm 规则声明为 `dual-use`，`contentPolicy` 和包根目录的 `DISCLOSURE`
不得在后续版本删除。OIDC 只用于 `npm stage publish`；每个 staged package 仍须维护者
用 2FA 审核批准。

npm 要求包先存在才能配置 Trusted Publisher，而当前包尚未创建。第一个签名 Release
生成后，维护者必须下载并检查其中的 tgz，再在已登录且启用 2FA 的终端完成一次 bootstrap：

```sh
npm publish ./zanescope-v-local-cli-0.1.0-dev.1.tgz --access public --tag next
npm trust github @zanescope/v-local-cli --file release.yml --repo zanescope/v-local-cli --env release --allow-stage-publish
```

bootstrap 后，正式流水线只提交 staged publish，不接受长期 npm token，也不直接绕过 2FA 发布。

## 发布步骤

1. 在 `main` 上确认 `Audit gates` 全绿并手动运行 `Release candidate`。
2. 下载候选件，按 `references/macos-acceptance.md` 和支持平台清单完成真机验收；保存脱敏的机器/微信/CLI 版本、候选件 SHA-256、generation 功能和签名检查结果，不保存账号、路径、正文或密钥。`Release candidate` 明确使用 `buildMode=candidate`，不是签名发行证据。
3. 从已验收提交创建并推送与包版本一致的标签，例如 `v0.1.0-dev.1`。
4. `Signed release` 会重新构建并启用发行门禁。Windows 把 Authenticode 叶证书 SHA-256 注入 CLI 后再签名/时间戳并复核；macOS 使用固定 identifier、Developer ID 和 Hardened Runtime，将二进制装入签名 DMG 后 notarize，拒绝日志 warning/error，并执行 staple/validate 与 Gatekeeper。每架构同时发布 `signature-manifest-*.json`，macOS 发布 notary log/DMG，全集生成 `release-checksums.txt` 和来源证明，然后只创建 GitHub prerelease。
5. 下载这个确切 prerelease，从最终 tgz 在干净机器安装 CLI 和独立 Provider。确认 Provider 固定路径、签名身份、helper sibling、daemon PID image、发行版 override 拒绝、协议版本不匹配失败、Keychain/Credential Manager、WER/core-dump 和卸载清理，再按支持平台复验真实数据路径和能力边界。把 tgz、DMG、manifest、notary log 和二进制摘要写入签名 live evidence。只有这份证据通过后才人工提升 GitHub Release；后续 npm staged publish 也只能在核对同一 tgz 摘要后用 2FA 批准。

任何签名身份、公证/staple、版本、摘要、运行时信任、干净机器、真机验收或 Trusted Publishing 条件未满足时，不得发布 `latest`。签名与 CI 通过本身不改变 `build_only/unverified` 能力状态。
