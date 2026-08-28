'use strict';

const assert = require('assert');
const crypto = require('crypto');
const fs = require('fs');
const os = require('os');
const path = require('path');
const test = require('node:test');
const {EventEmitter} = require('events');
const {PassThrough} = require('stream');

const installer = require('../scripts/install.js');
const skillStager = require('../scripts/stage-skill.js');

test('npm dual-use 声明随发布包持久存在', () => {
  const metadata = require('../package.json');
  assert.strictEqual(metadata.contentPolicy.class, 'dual-use');
  assert.ok(metadata.files.includes('DISCLOSURE'));
  assert.match(fs.readFileSync(path.resolve(__dirname, '..', 'DISCLOSURE'), 'utf8'), /explicitly authorized/i);
});

test('首发平台和架构只包含三个目标', () => {
  const metadata = require('../package.json');
  assert.deepStrictEqual(metadata.os, ['darwin', 'win32']);
  assert.deepStrictEqual(installer.target('win32', 'x64'), {
    platform: 'windows', arch: 'amd64',
    asset: 'v-local-cli-windows-amd64.exe', binary: 'v-local-cli.exe',
  });
  assert.deepStrictEqual(installer.target('darwin', 'arm64'), {
    platform: 'darwin', arch: 'arm64',
    asset: 'v-local-cli-darwin-arm64', binary: 'v-local-cli',
  });
  assert.deepStrictEqual(installer.target('darwin', 'x64'), {
    platform: 'darwin', arch: 'amd64',
    asset: 'v-local-cli-darwin-amd64', binary: 'v-local-cli',
  });
  assert.throws(() => installer.target('win32', 'arm64'), /不支持的平台/);
  assert.throws(() => installer.target('linux', 'x64'), /不支持的平台/);
  assert.throws(() => installer.target('linux', 'arm64'), /不支持的平台/);
});

test('校验和解析拒绝模糊格式', () => {
  const digest = 'a'.repeat(64);
  const values = installer.parseChecksums(`${digest}  v-local-cli-windows-amd64.exe\n`);
  assert.strictEqual(values.get('v-local-cli-windows-amd64.exe'), digest);
  assert.throws(() => installer.parseChecksums('sha256 file'));
  assert.throws(() => installer.parseChecksums(`${digest}  ../v-local-cli\n`));
  assert.throws(() => installer.parseChecksums(`${digest}  ..\\v-local-cli\n`));
  assert.throws(() => installer.parseChecksums(
    `${digest}  v-local-cli\n${'b'.repeat(64)}  v-local-cli\n`,
  ));
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

test('本地未验证 CLI 必须由隔离开发态三重显式授权', () => {
  assert.strictEqual(installer.allowUnverifiedLocalBinary({}), false);
  assert.strictEqual(installer.allowUnverifiedLocalBinary({
    V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY: '1',
  }), false);
  assert.strictEqual(installer.allowUnverifiedLocalBinary({
    V_LOCAL_CLI_BINARY_PATH: 'cli-under-test',
    V_LOCAL_CLI_DEVELOPMENT: '1',
    V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY: '1',
  }), true);
});

test('CLI 下载重定向保留已独占打开的目标描述符', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-npm-'));
  const destination = path.join(directory, 'cli.tmp');
  const descriptor = fs.openSync(destination, 'wx', 0o700);
  const requests = [];
  const requester = (url, _options, callback) => {
    requests.push(url.toString());
    const request = new EventEmitter();
    request.setTimeout = () => request;
    request.destroy = error => request.emit('error', error);
    process.nextTick(() => {
      const response = new PassThrough();
      if (requests.length === 1) {
        response.statusCode = 302;
        response.headers = {location: 'https://objects.githubusercontent.com/cli'};
        callback(response);
        response.end();
        return;
      }
      response.statusCode = 200;
      response.headers = {'content-length': '3'};
      callback(response);
      response.end('cli');
    });
    return request;
  };
  try {
    await installer.download(
      'https://github.com/zanescope/v-local-cli/releases/download/v1/cli',
      destination, 0, descriptor, requester,
    );
    assert.strictEqual(fs.readFileSync(destination, 'utf8'), 'cli');
    assert.strictEqual(requests.length, 2);
  } finally {
    fs.closeSync(descriptor);
    fs.rmSync(directory, {recursive: true, force: true});
  }
});

test('预发布 npm 版本下载同版本 GitHub Release', () => {
  assert.strictEqual(installer.releaseTag('0.1.0-dev.1'), 'v0.1.0-dev.1');
  assert.strictEqual(
    installer.releaseUrl('0.1.0-dev.1', 'v-local-cli-windows-amd64.exe'),
    'https://github.com/zanescope/v-local-cli/releases/download/v0.1.0-dev.1/v-local-cli-windows-amd64.exe',
  );
  assert.throws(() => installer.releaseTag('latest'));
});

test('包内二进制目录拒绝预置符号链接或目录联接', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-install-root-'));
  const external = fs.mkdtempSync(path.join(os.tmpdir(), 'v-local-cli-install-external-'));
  const selected = installer.target('win32', 'x64');
  try {
    const direct = installer.preparePackageInstallDirectory(root, selected);
    assert.strictEqual(direct, path.join(root, 'bin', 'windows-amd64'));
    fs.rmSync(path.join(root, 'bin'), {recursive: true, force: true});
    fs.symlinkSync(external, path.join(root, 'bin'), process.platform === 'win32' ? 'junction' : 'dir');
    assert.throws(() => installer.preparePackageInstallDirectory(root, selected), /符号链接|目录联接/);
  } finally {
    fs.rmSync(root, {recursive: true, force: true});
    fs.rmSync(external, {recursive: true, force: true});
  }
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
