'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const sandbox = {Date, esc: value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;')};
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/static/js/pages/panel-settings.js'), 'utf8'), sandbox);

test('TLS status distinguishes the served certificate and saved certificate, and escapes maintenance failures', () => {
  const html = sandbox.renderPanelCertificateStatus({
    available: true, configured: true, restart_required: true, tls_enabled: true,
    certificate_dns_names: ['*.next.example'], certificate_current: true, certificate_valid: true,
    certificate_trusted: false, certificate_trust_message: '内部 CA 需单独信任',
    active_certificate: {dns_names: ['*.current.example'], valid: true, trusted: true, trust_message: '服务器信任链校验通过'},
    auto_renew_enabled: true, certificate_source: 'upload',
    renewal: {source: 'acme', domain: 'panel.current.example', pending: true,
      last_checked_at: '2026-10-12T09:00:00Z', last_success_at: '2026-10-11T09:00:00Z',
      last_error: '<img src=x onerror=alert(1)>', next_attempt_at: '2026-10-12T10:00:00Z'},
  });
  assert.match(html, /当前提供的证书/);
  assert.match(html, /\*\.current\.example/);
  assert.match(html, /已保存的证书/);
  assert.match(html, /\*\.next\.example/);
  assert.match(html, /panel\.current\.example；新配置等待应用/);
  assert.match(html, /已启用，到期前 30 天续签/);
  assert.match(html, /下次重试/);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /<img/);
});

test('expired certificate retains HTTPS and clearly reports recovery requirements', () => {
  const html = sandbox.renderPanelCertificateStatus({available: true, configured: true,
    active_certificate: {dns_names: ['panel.example.com'], valid: false, trusted: false},
    certificate_source: 'path', tls_enabled: true, renewal: {source: 'path'},
  });
  assert.match(html, /服务保留 HTTPS/);
  assert.match(html, /每分钟检查服务器文件/);
  assert.match(html, /证书已失效/);
  assert.doesNotMatch(html, /Invalid Date/);
});
