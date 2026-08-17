#!/usr/bin/env node

'use strict';

const fs = require('fs');
const path = require('path');
const {spawnSync} = require('child_process');
const installer = require('./install.js');

async function main() {
  const selected = installer.target();
  const packageRoot = path.resolve(__dirname, '..');
  const binary = path.join(
    packageRoot, 'bin', `${selected.platform}-${selected.arch}`, selected.binary,
  );
  if (!fs.existsSync(binary)) await installer.install();
  if (process.env.V_LOCAL_CLI_ALLOW_UNVERIFIED_LOCAL_BINARY !== '1') {
    installer.verifyHash(binary, installer.expectedChecksum(selected));
  }
  const result = spawnSync(binary, process.argv.slice(2), {
    stdio: 'inherit',
    windowsHide: true,
    env: {...process.env, V_LOCAL_CLI_SKILL_DIR: path.join(packageRoot, 'skill')},
  });
  if (result.error) throw result.error;
  process.exitCode = result.status === null ? 1 : result.status;
}

main().catch(error => {
  process.stderr.write(`v-local-cli 启动失败：${error.message}\n`);
  process.exitCode = 1;
});
