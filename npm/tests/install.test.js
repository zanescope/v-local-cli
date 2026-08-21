'use strict';

const assert = require('assert');
const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const test = require('node:test');

const installer = require('../scripts/install.js');
const skillStager = require('../scripts/stage-skill.js');

test('npm dual-use 声明随发布包持久存在', () => {
  const metadata = require('../package.json');
  assert.strictEqual(metadata.contentPolicy.class, 'dual-use');
  assert.ok(metadata.files.includes('DISCLOSURE'));
  assert.match(fs.readFileSync(path.resolve(__dirname, '..', 'DISCLOSURE'), 'utf8'), /explicitly authorized/i);
});

test('平台和架构映射稳定', () => {
  assert.deepStrictEqual(installer.target('win32', 'x64'), {
    platform: 'windows', arch: 'amd64',
    asset: 'v-local-cli-windows-amd64.exe', binary: 'v-local-cli.exe',
  });
  assert.deepStrictEqual(installer.target('darwin', 'arm64'), {
    platform: 'darwin', arch: 'arm64',
    asset: 'v-local-cli-darwin-arm64', binary: 'v-local-cli',
  });
});

test('校验和解析拒绝模糊格式', () => {
  const digest = 'a'.repeat(64);
  const values = installer.parseChecksums(`${digest}  v-local-cli-windows-amd64.exe\n`);
  assert.strictEqual(values.get('v-local-cli-windows-amd64.exe'), digest);
  assert.throws(() => installer.parseChecksums('sha256 file'));
});

test('文件校验使用 SHA-256', () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-npm-'));
  const file = path.join(directory, 'binary');
  try {
    fs.writeFileSync(file, 'v-local-cli');
    const digest = crypto.createHash('sha256').update('v-local-cli').digest('hex');
    installer.verifyHash(file, digest);
    assert.throws(() => installer.verifyHash(file, '0'.repeat(64)));
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('下载地址只允许 GitHub 发布域名', () => {
  assert.doesNotThrow(() => installer.assertDownloadUrl(
    'https://github.com/zanescope/v-local-cli/releases/download/v1/a',
  ));
  assert.throws(() => installer.assertDownloadUrl('https://example.com/a'));
  assert.throws(() => installer.assertDownloadUrl('http://github.com/a'));
  assert.throws(() => installer.assertDownloadUrl('https://user@github.com/a'));
  assert.throws(() => installer.assertDownloadUrl('https://github.com:8443/a'));
});

test('预发布 npm 版本下载同版本 GitHub Release', () => {
  assert.strictEqual(installer.releaseTag('0.1.0-dev.1'), 'v0.1.0-dev.1');
  assert.strictEqual(
    installer.releaseUrl('0.1.0-dev.1', 'v-local-cli-windows-amd64.exe'),
    'https://github.com/zanescope/v-local-cli/releases/download/v0.1.0-dev.1/v-local-cli-windows-amd64.exe',
  );
  assert.throws(() => installer.releaseTag('latest'));
});

test('原子替换失败前保留旧文件', () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-npm-'));
  const destination = path.join(directory, 'v-local-cli');
  const temporary = path.join(directory, 'v-local-cli.tmp');
  try {
    fs.writeFileSync(destination, 'old');
    fs.writeFileSync(temporary, 'new');
    const predictableBackup = `${destination}.${process.pid}.old`;
    fs.writeFileSync(predictableBackup, 'user-owned');
    installer.replaceFile(temporary, destination);
    assert.strictEqual(fs.readFileSync(destination, 'utf8'), 'new');
    assert.strictEqual(fs.existsSync(temporary), false);
    assert.strictEqual(fs.readFileSync(predictableBackup, 'utf8'), 'user-owned');
    assert.deepStrictEqual(fs.readdirSync(directory).filter(name => name.startsWith('.v-local-cli-')), []);
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('安装临时文件使用随机独占兄弟路径', () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-npm-'));
  const destination = path.join(directory, 'v-local-cli');
  const predictable = `${destination}.${process.pid}.tmp`;
  try {
    fs.writeFileSync(predictable, 'user-owned');
    const first = installer.reserveSibling(destination, 'tmp', 0o700);
    const second = installer.reserveSibling(destination, 'tmp', 0o700);
    assert.notStrictEqual(first, second);
    assert.notStrictEqual(first, predictable);
    assert.strictEqual(fs.readFileSync(predictable, 'utf8'), 'user-owned');
    fs.rmSync(first);
    fs.rmSync(second);
  } finally {
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('npm 包中的 Skill 清单绑定全部资源摘要', () => {
  const manifest = skillStager.stageSkill();
  const skillRoot = path.resolve(__dirname, '..', 'skill');
  assert.strictEqual(manifest.schema_version, 1);
  assert.ok(manifest.files['SKILL.md']);
  assert.ok(manifest.files['agents/openai.yaml']);
  for (const [relative, expected] of Object.entries(manifest.files)) {
    const payload = fs.readFileSync(path.join(skillRoot, relative));
    const actual = crypto.createHash('sha256').update(payload).digest('hex');
    assert.strictEqual(actual, expected);
  }
});
