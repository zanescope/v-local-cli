#!/usr/bin/env node

'use strict';

const crypto = require('crypto');
const fs = require('fs');
const https = require('https');
const path = require('path');

const packageRoot = path.resolve(__dirname, '..');
const allowedHosts = new Set([
  'github.com',
  'objects.githubusercontent.com',
  'release-assets.githubusercontent.com',
]);
const maxBinaryBytes = 128 * 1024 * 1024;

function target(platform = process.platform, arch = process.arch) {
  const supported = {
    'win32/x64': {platform: 'windows', arch: 'amd64'},
    'darwin/x64': {platform: 'darwin', arch: 'amd64'},
    'darwin/arm64': {platform: 'darwin', arch: 'arm64'},
  };
  const selected = supported[`${platform}/${arch}`];
  if (!selected) {
    throw new Error(`不支持的平台：${platform}/${arch}`);
  }
  const extension = platform === 'win32' ? '.exe' : '';
  return {
    platform: selected.platform,
    arch: selected.arch,
    asset: `v-local-cli-${selected.platform}-${selected.arch}${extension}`,
    binary: `v-local-cli${extension}`,
  };
}

function parseChecksums(text) {
  const values = new Map();
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const match = line.match(/^([0-9a-fA-F]{64})\s+\*?([^\s]+)$/);
    if (!match) throw new Error(`无效的校验和记录：${line}`);
    const name = match[2];
    if (path.basename(name) !== name || name.includes('/') || name.includes('\\') || name === '.' || name === '..') {
      throw new Error(`校验和文件名不能包含路径：${name}`);
    }
    if (values.has(name)) throw new Error(`校验和包含重复资产：${name}`);
    values.set(name, match[1].toLowerCase());
  }
  return values;
}

function releaseTag(version) {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
    throw new Error(`npm 包版本无法映射到 GitHub Release：${version}`);
  }
  return `v${version}`;
}

function releaseUrl(version, asset) {
  return `https://github.com/zanescope/v-local-cli/releases/download/${releaseTag(version)}/${asset}`;
}

function sha256(file) {
  const hash = crypto.createHash('sha256');
  const descriptor = fs.openSync(file, 'r');
  const buffer = Buffer.allocUnsafe(1024 * 1024);
  try {
    for (;;) {
      const bytes = fs.readSync(descriptor, buffer, 0, buffer.length, null);
      if (bytes === 0) break;
      hash.update(buffer.subarray(0, bytes));
    }
  } finally {
    fs.closeSync(descriptor);
  }
  return hash.digest('hex');
}

function verifyHash(file, expected) {
  const actual = sha256(file);
  if (actual !== expected.toLowerCase()) {
    throw new Error(`二进制 SHA-256 不匹配：期望 ${expected}，实际 ${actual}`);
  }
}

function assertDownloadUrl(value) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || !allowedHosts.has(url.hostname) ||
      url.username !== '' || url.password !== '' || url.port !== '') {
    throw new Error(`拒绝从未授权地址下载：${url.origin}`);
  }
  return url;
}

function reserveSiblingFile(destination, suffix, mode) {
  for (let attempt = 0; attempt < 64; attempt += 1) {
    const nonce = crypto.randomBytes(16).toString('hex');
    const candidate = path.join(path.dirname(destination), `.v-local-cli-${nonce}.${suffix}`);
    try {
      const descriptor = fs.openSync(candidate, 'wx', mode);
      return {path: candidate, descriptor};
    } catch (error) {
      if (error && error.code === 'EEXIST') continue;
      throw error;
    }
  }
  throw new Error('无法创建随机独占的安装临时文件');
}

function reserveSibling(destination, suffix, mode) {
  const reservation = reserveSiblingFile(destination, suffix, mode);
  fs.closeSync(reservation.descriptor);
  return reservation.path;
}

function download(value, destination, redirects = 0, descriptor = undefined, requester = https.get) {
  if (redirects > 5) return Promise.reject(new Error('下载重定向次数过多'));
  const url = assertDownloadUrl(value);
  return new Promise((resolve, reject) => {
    const request = requester(url, {headers: {'user-agent': '@zanescope/v-local-cli'}}, response => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        const next = new URL(response.headers.location, url).toString();
        download(next, destination, redirects + 1, descriptor, requester).then(resolve, reject);
        return;
      }
      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`下载失败：HTTP ${response.statusCode}`));
        return;
      }
      const declaredLength = Number(response.headers['content-length']);
      if (Number.isFinite(declaredLength) && declaredLength > maxBinaryBytes) {
        response.resume();
        reject(new Error('下载响应超过二进制大小上限'));
        return;
      }
      const stream = descriptor === undefined ?
        fs.createWriteStream(destination, {flags: 'wx', mode: 0o700}) :
        fs.createWriteStream(destination, {fd: descriptor, autoClose: false});
      let received = 0;
      response.on('data', chunk => {
        received += chunk.length;
        if (received > maxBinaryBytes) response.destroy(new Error('下载响应超过二进制大小上限'));
      });
      response.pipe(stream);
      stream.on('finish', () => {
        if (received <= 0 || received > maxBinaryBytes) {
          reject(new Error('下载响应大小无效'));
        } else {
          resolve();
        }
      });
      stream.on('error', reject);
      response.on('error', reject);
    });
    request.setTimeout(30_000, () => request.destroy(new Error('下载超时')));
    request.on('error', reject);
  });
}

function expectedChecksum(selected = target()) {
  const checksums = parseChecksums(fs.readFileSync(path.join(packageRoot, 'checksums.txt'), 'utf8'));
  const expected = checksums.get(selected.asset);
  if (!expected) throw new Error(`发布包缺少 ${selected.asset} 的 SHA-256`);
  return expected;
}

function allowUnverifiedLocalBinary(environment = process.env) {
  return Boolean(String(environment.V_LOCAL_CLI_BINARY_PATH || '').trim()) &&
    environment.V_LOCAL_CLI_DEVELOPMENT === '1' &&
    environment.V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY === '1';
}

function preparePackageInstallDirectory(root, selected = target(), filesystem = fs) {
  let current = path.resolve(root);
  for (const segment of ['bin', `${selected.platform}-${selected.arch}`]) {
    current = path.join(current, segment);
    try {
      filesystem.mkdirSync(current, {mode: 0o700});
    } catch (error) {
      if (!error || error.code !== 'EEXIST') throw error;
    }
    const info = filesystem.lstatSync(current);
    if (!info.isDirectory() || info.isSymbolicLink()) {
      throw new Error(`npm 安装目录不能是符号链接或目录联接：${current}`);
    }
  }
  return current;
}

function replaceFile(temporary, destination) {
  let backup = '';
  let movedOld = false;
  try {
    if (fs.existsSync(destination)) {
      backup = reserveSibling(destination, 'old', 0o600);
      fs.rmSync(backup);
      fs.renameSync(destination, backup);
      movedOld = true;
    }
    fs.renameSync(temporary, destination);
    if (movedOld) fs.rmSync(backup, {force: true});
  } catch (error) {
    if (movedOld && !fs.existsSync(destination) && fs.existsSync(backup)) {
      fs.renameSync(backup, destination);
    }
    throw error;
  }
}

async function install() {
  if (process.env.V_LOCAL_CLI_SKIP_BINARY_INSTALL === '1') return;
  const selected = target();
  const destinationDir = preparePackageInstallDirectory(packageRoot, selected);
  const destination = path.join(destinationDir, selected.binary);

  const localBinary = process.env.V_LOCAL_CLI_BINARY_PATH;
  if (localBinary) {
    if (!allowUnverifiedLocalBinary()) {
      throw new Error('V_LOCAL_CLI_BINARY_PATH 仅允许在同时设置 V_LOCAL_CLI_DEVELOPMENT=1 和 V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY=1 的隔离开发环境使用');
    }
    const temporary = reserveSibling(destination, 'tmp', 0o700);
    try {
      fs.copyFileSync(path.resolve(localBinary), temporary);
      fs.chmodSync(temporary, 0o700);
      replaceFile(temporary, destination);
    } finally {
      if (fs.existsSync(temporary)) fs.rmSync(temporary, {force: true});
    }
    return destination;
  }

  const packageInfo = JSON.parse(fs.readFileSync(path.join(packageRoot, 'package.json'), 'utf8'));
  const expected = expectedChecksum(selected);
  if (fs.existsSync(destination)) {
    try {
      verifyHash(destination, expected);
      return destination;
    } catch (_) {
      // 已存在但摘要不匹配时下载干净副本并进行原子替换。
    }
  }
  const url = releaseUrl(packageInfo.version, selected.asset);
  const reservation = reserveSiblingFile(destination, 'tmp', 0o700);
  const temporary = reservation.path;
  try {
    await download(url, temporary, 0, reservation.descriptor);
    fs.closeSync(reservation.descriptor);
    reservation.descriptor = undefined;
    verifyHash(temporary, expected);
    fs.chmodSync(temporary, 0o700);
    replaceFile(temporary, destination);
  } finally {
    if (reservation.descriptor !== undefined) fs.closeSync(reservation.descriptor);
    if (fs.existsSync(temporary)) fs.rmSync(temporary, {force: true});
  }
  return destination;
}

if (require.main === module) {
  install().catch(error => {
    process.stderr.write(`v-local-cli 安装失败：${error.message}\n`);
    process.exitCode = 1;
  });
}

module.exports = {
  allowUnverifiedLocalBinary, assertDownloadUrl, download, expectedChecksum, install, parseChecksums,
  preparePackageInstallDirectory, releaseTag, releaseUrl, replaceFile, reserveSibling, target, verifyHash,
};
