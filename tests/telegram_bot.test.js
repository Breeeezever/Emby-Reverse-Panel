'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

function load() {
  const inputs = Object.fromEntries([
    ['telegram-bot-token', ''], ['telegram-chat-id', ''], ['telegram-report-enabled', false],
    ['telegram-bot-enabled', true], ['telegram-admin-ids', '123456789'],
    ['telegram-public-url', 'https://panel.example:8443/'], ['telegram-frequency', 'daily'],
    ['telegram-weekday', '1'], ['telegram-schedule-time', '20:00'],
  ].map(([id, value]) => [id, { value: typeof value === 'string' ? value : '', checked: value === true, focus() { this.focused = true; } }]));
  const context = { URL, window: { location: { origin: 'https://panel.example:8443' } },
    document: { getElementById: id => inputs[id], querySelectorAll: () => Object.values(inputs) },
    setFieldError(field, message) { field.error = message; },
  };
  vm.createContext(context);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/static/js/pages/telegram-report.js'), 'utf8'), context);
  vm.runInContext('telegramTokenConfigured = true', context);
  return { context, inputs };
}

test('Telegram admin IDs reject group IDs, usernames, overflow and partial numbers', () => {
  const { context } = load();
  for (const value of ['-100123456', '@admin', '12x', '1.5', '1e3', '0', '9007199254740993', '4503599627370496', Array.from({ length: 11 }, (_, i) => i + 1).join(',')]) assert.equal(context.parseTelegramAdminIDs(value), null, value);
  assert.deepEqual(Array.from(context.parseTelegramAdminIDs('123, 456，123\n789')), [123, 456, 789]);
  assert.deepEqual(Array.from(context.parseTelegramAdminIDs('')), []);
});

test('bot administration can be enabled independently of scheduled reporting', () => {
  const { context, inputs } = load();
  const payload = context.telegramReportPayload('');
  assert.equal(payload.enabled, false);
  assert.equal(payload.chat_id, '');
  assert.equal(payload.bot.enabled, true);
  assert.equal(payload.bot.public_url, 'https://panel.example:8443');
  assert.equal(context.validateTelegramForm(payload, false), true);
  inputs['telegram-report-enabled'].checked = true;
  assert.equal(context.validateTelegramForm(context.telegramReportPayload(''), false), false);
  assert.ok(inputs['telegram-chat-id'].focused);
});

test('manual report sends only its destination and optional credential without modifying bot settings', () => {
  const { context, inputs } = load();
  inputs['telegram-chat-id'].value = '-100123';
  inputs['telegram-admin-ids'].value = 'unfinished';
  const payload = context.telegramReportPayload('test');
  assert.deepEqual(Object.keys(payload).sort(), ['action', 'bot_token', 'chat_id']);
  assert.equal(payload.action, 'test');
  assert.equal(context.validateTelegramForm(payload, true), true);
});

test('Telegram form rejects invalid public origins before saving', () => {
  const { context, inputs } = load();
  for (const value of ['', 'ftp://panel.example', 'https://panel.example:888888', 'https://panel.example/path', 'https://user:pass@panel.example', 'https://panel.example?token=secret']) {
    inputs['telegram-public-url'].value = value;
    assert.equal(context.validateTelegramForm(context.telegramReportPayload(''), false), false, value);
    assert.ok(inputs['telegram-public-url'].focused);
  }
  for (const value of ['https://panel.example:8443', 'http://[::1]:9090', 'https://panel.example']) {
    inputs['telegram-public-url'].value = value;
    assert.equal(context.validateTelegramForm(context.telegramReportPayload(''), false), true, value);
  }
});
