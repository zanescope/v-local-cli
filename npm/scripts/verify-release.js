#!/usr/bin/env node

'use strict';

const fs = require('fs');
const path = require('path');
const installer = require('./install.js');

const packageRoot = path.resolve(__dirname, '..');
const metadata = JSON.parse(fs.readFileSync(path.join(packageRoot, 'package.json'), 'utf8'));
const expectedAssets = [
  'v-local-cli-windows-amd64.exe',
  'v-local-cli-windows-arm64.exe',
  'v-local-cli-darwin-amd64',
  'v-local-cli-darwin-arm64',
  'v-local-cli-linux-amd64',
  'v-local-cli-linux-arm64',
];

const requestedVersion = process.env.RELEASE_VERSION || metadata.version;
if (requestedVersion !== metadata.version) {
  throw new Error(`发布版本 ${requestedVersion} 与 npm 版本 ${metadata.version} 不一致`);
}
const expectedTag = installer.releaseTag(metadata.version);
if (process.env.GITHUB_REF_TYPE === 'tag' && process.env.GITHUB_REF_NAME !== expectedTag) {
  throw new Error(`Git 标签 ${process.env.GITHUB_REF_NAME} 与 npm 版本要求的 ${expectedTag} 不一致`);
}

const checksums = installer.parseChecksums(
  fs.readFileSync(path.join(packageRoot, 'checksums.txt'), 'utf8'),
);
if (checksums.size !== expectedAssets.length) {
  throw new Error(`校验和清单应只包含 ${expectedAssets.length} 个发布资产，实际为 ${checksums.size} 个`);
}
for (const asset of expectedAssets) {
  if (!checksums.has(asset)) throw new Error(`校验和清单缺少 ${asset}`);
}

process.stdout.write(`release ${expectedTag}: ${expectedAssets.length} assets verified\n`);
