'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const root = path.join(__dirname, '..');
const source = fs.readFileSync(path.join(root, 'web', 'static', 'js', 'pages', 'sites.js'), 'utf8');
const style = fs.readFileSync(path.join(root, 'web', 'static', 'css', 'style.css'), 'utf8');

function loadHelpers() {
  const sandbox = { window: {}, URL, esc: value => String(value) };
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(path.join(root, 'web', 'static', 'js', 'form-validation.js'), 'utf8'), sandbox);
  vm.runInContext(source, sandbox, { filename: 'sites.js' });
  return sandbox;
}

test('web account renewal exposes preset and custom choices without a browser prompt', () => {
  assert.match(source, /data-renewal-days="30">一月/);
  assert.match(source, /data-renewal-days="90">一季度/);
  assert.match(source, /data-renewal-days="365">一年/);
  assert.match(source, /data-renewal-custom-toggle>自定义/);
  assert.match(source, /data-renewal-custom-input/);
  assert.match(source, /data-renewal-custom-submit>确认续期/);
  assert.doesNotMatch(source, /window\.prompt\(['"]续期天数/);
  assert.match(source, /guardAccountRenewal\(\)/);
  assert.match(source, /API\.renewSiteAccount\(site\.id, accountID, normalizedDays\)/);
});

test('web renewal day validation accepts only integer values from 1 through 3650', () => {
  const { normalizeAccountRenewalDays } = loadHelpers();
  for (const [input, expected] of [['30', 30], [90, 90], ['3650', 3650]]) {
    assert.equal(normalizeAccountRenewalDays(input), expected);
  }
  for (const input of ['', '0', '-1', '1.5', '1e3', '3651', 'abc', null]) {
    assert.equal(normalizeAccountRenewalDays(input), 0, String(input));
  }
});

test('web account expiry display keeps only the calendar date', () => {
  const { formatAccountExpiryDate } = loadHelpers();
  assert.equal(formatAccountExpiryDate('2026-11-09 22:08:59.342128'), '2026-11-09');
  assert.equal(formatAccountExpiryDate('2026-11-09T22:08:59.342128Z'), '2026-11-09');
  assert.equal(formatAccountExpiryDate('2026-11-09'), '2026-11-09');
  assert.equal(formatAccountExpiryDate(''), '');
  assert.equal(formatAccountExpiryDate('待修正'), '待修正');
});

test('web renewal picker has responsive controls and keeps pending edits guarded', () => {
  assert.match(style, /\.account-renewal-picker\s*\{[\s\S]*?display:\s*grid/);
  assert.match(style, /\.account-renewal-presets\s*\{[\s\S]*?flex-wrap:\s*wrap/);
  assert.match(style, /\.account-renewal-custom\s*\{[\s\S]*?align-items:\s*flex-end/);
  assert.match(source, /当前账号有未保存的修改，请先保存账号或放弃修改/);
  assert.match(source, /data-renewal-picker="\$\{account\.id\}/);
});
