#!/usr/bin/env node

'use strict';

const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const packageRoot = path.resolve(__dirname, '..');
const repositoryRoot = path.resolve(packageRoot, '..');
const destinationRoot = path.join(packageRoot, 'skill');
const maxSkillFileBytes = 8 * 1024 * 1024;

function sha256(payload) {
  return crypto.createHash('sha256').update(payload).digest('hex');
}

function copyRegularFile(relative, manifest) {
  const source = path.join(repositoryRoot, relative);
  const stat = fs.lstatSync(source);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > maxSkillFileBytes) {
    throw new Error(`拒绝打包异常 Skill 资源：${relative}`);
  }
  const payload = fs.readFileSync(source);
  const target = path.join(destinationRoot, relative);
  fs.mkdirSync(path.dirname(target), {recursive: true, mode: 0o700});
  fs.writeFileSync(target, payload, {flag: 'wx', mode: 0o600});
  manifest.files[relative.split(path.sep).join('/')] = sha256(payload);
}

function stageSkill() {
  fs.rmSync(destinationRoot, {recursive: true, force: true});
  fs.mkdirSync(destinationRoot, {recursive: true, mode: 0o700});
  const manifest = {schema_version: 1, files: {}};
  copyRegularFile('SKILL.md', manifest);
  copyRegularFile(path.join('agents', 'openai.yaml'), manifest);
  const references = fs.readdirSync(path.join(repositoryRoot, 'references'), {withFileTypes: true})
    .filter(entry => entry.isFile() && path.extname(entry.name).toLowerCase() === '.md')
    .map(entry => entry.name)
    .sort();
  for (const name of references) copyRegularFile(path.join('references', name), manifest);
  fs.writeFileSync(
    path.join(destinationRoot, 'skill-manifest.json'),
    `${JSON.stringify(manifest, null, 2)}\n`,
    {flag: 'wx', mode: 0o600},
  );
  return manifest;
}

if (require.main === module) stageSkill();

module.exports = {stageSkill};
