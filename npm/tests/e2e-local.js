#!/usr/bin/env node

'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const {spawnSync} = require('child_process');
const skillStager = require('../scripts/stage-skill.js');

async function main() {
  if (process.argv.length !== 3) throw new Error('用法：node e2e-local.js <v-local-cli-binary>');
  const packageRoot = path.resolve(__dirname, '..');
  const binary = path.resolve(process.argv[2]);
  const installationRoot = fs.mkdtempSync(path.join(require('os').tmpdir(), 'v-local-cli-e2e-'));
  assert.ok(fs.statSync(binary).isFile());
  skillStager.stageSkill();
  process.env.V_LOCAL_CLI_BINARY_PATH = binary;
  process.env.V_LOCAL_CLI_DEVELOPMENT = '1';
  process.env.V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY = '1';
  process.env.V_LOCAL_CLI_AGENT_SKILL_HOME = path.join(installationRoot, 'agents');
  process.env.V_LOCAL_CLI_SKILL_HOME = path.join(installationRoot, 'codex');
  const installer = require('../scripts/install.js');
  try {
    await installer.install();
    const result = spawnSync(process.execPath, [path.join(packageRoot, 'scripts', 'run.js'), '--version'], {
      encoding: 'utf8',
      env: process.env,
      windowsHide: true,
    });
    if (result.error) throw result.error;
    assert.strictEqual(result.status, 0, result.stderr);
    assert.match(result.stdout, /^0\.1\.0-dev\.1\s*$/);
    const installed = spawnSync(process.execPath, [path.join(packageRoot, 'scripts', 'run.js'), 'install'], {
      encoding: 'utf8',
      env: process.env,
      windowsHide: true,
    });
    if (installed.error) throw installed.error;
    assert.strictEqual(installed.status, 0, installed.stderr);
    assert.strictEqual(JSON.parse(installed.stdout).data.status, 'installed');
    assert.ok(fs.statSync(path.join(installationRoot, 'agents', 'v-local-cli', 'SKILL.md')).isFile());
    assert.ok(fs.statSync(path.join(installationRoot, 'codex', 'v-local-cli', 'SKILL.md')).isFile());
  } finally {
    fs.rmSync(path.join(packageRoot, 'bin'), {recursive: true, force: true});
    fs.rmSync(installationRoot, {recursive: true, force: true});
  }
}

main().catch(error => {
  process.stderr.write(`${error.stack || error.message}\n`);
  process.exitCode = 1;
});
