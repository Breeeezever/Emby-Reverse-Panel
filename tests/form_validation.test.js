'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const sandbox = { URL, window: {} };
vm.createContext(sandbox);
for (const file of ['form-validation.js', 'pages/sites.js', 'pages/global-settings.js', 'pages/panel-settings.js']) {
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/static/js', file), 'utf8'), sandbox);
}

test('invalid ports cannot be silently discarded by the URL port setter', () => {
  for (const port of ['0', '-1', '65536', '888888', '80.5', '80abc', '1e3', '+80', 'Infinity']) {
    assert.equal(sandbox.joinUpstreamTargetAddress('https', 'origin.example:8443/emby', port), '', port);
    assert.equal(sandbox.buildIngressPayload('port', port, '').listen_port, 0, port);
  }
  for (const port of ['1', '65535']) {
    assert.equal(sandbox.joinUpstreamTargetAddress('http', '[::1]/emby', port), `http://[::1]:${port}/emby`);
    assert.equal(sandbox.buildIngressPayload('port', port, '').listen_port, Number(port));
  }
  assert.equal(sandbox.joinUpstreamTargetAddress('http', 'origin.example', ''), 'http://origin.example:8096');
  assert.equal(sandbox.joinUpstreamTargetAddress('https', 'origin.example', ''), 'https://origin.example');
});

test('panel listener port stays distinct from the external access port', () => {
  const proxy = {listen_port:19093, active_listen_port:19093, restart_required:false};
  assert.equal(sandbox.panelEndpointFormPort(proxy, {protocol:'https:', port:'8443'}), 19093);
  assert.equal(sandbox.panelEndpointFormPort(proxy, {protocol:'https:', port:''}), 19093);
  assert.equal(sandbox.panelEndpointFormPort(proxy, {protocol:'http:', port:''}), 19093);
  assert.equal(sandbox.panelEndpointFormPort(proxy, {protocol:'http:', port:'9090'}), 19093);
  assert.equal(sandbox.panelEndpointFormPort({...proxy,listen_port:9443,restart_required:true}, {protocol:'https:', port:'8443'}), 9443);
  assert.equal(sandbox.panelEndpointFormPort(proxy, null), 19093);
});

function numberField(value, overrides = {}) {
  return { type: 'number', value, min: '1', max: '65535', required: true, disabled: false,
    validity: { badInput: false }, closest: () => null, ...overrides };
}

test('numeric form validation covers empty, malformed, fractional and boundary values', () => {
  for (const value of ['', '0', '-1', '888888', '1.5', '1e3', '12oops']) {
    assert.match(sandbox.fieldValidationMessage(numberField(value)), /1–65535/, value);
  }
  for (const value of ['1', '65535', '0080']) {
    assert.equal(sandbox.fieldValidationMessage(numberField(value)), '', value);
  }
  assert.equal(sandbox.fieldValidationMessage(numberField('', { required: false })), '');
  assert.match(sandbox.fieldValidationMessage(numberField('', { required: false, validity: { badInput: true } })), /整数/);
  assert.equal(sandbox.fieldValidationMessage(numberField('0', { min: '0', max: '1000000' })), '');
  assert.match(sandbox.fieldValidationMessage(numberField('1000001', { min: '0', max: '1000000' })), /0–1000000/);
});

test('hidden conditional controls are skipped, but inactive tabs are validated', () => {
  assert.equal(sandbox.fieldValidationMessage(numberField('888888', { disabled: true })), '');
  assert.equal(sandbox.fieldValidationMessage(numberField('888888', { closest: () => ({ hidden: true }) })), '');
  assert.match(sandbox.fieldValidationMessage(numberField('888888')), /65535/);
});

test('numeric parsing rejects unsafe values and does not truncate fractional settings', () => {
  for (const value of ['', '12.7', '24hours', '1e6', '9007199254740992']) {
    assert.equal(sandbox.parseBoundedInteger(value), null, value);
  }
  assert.equal(sandbox.parseBoundedInteger('0', 0, 1000000), 0);
  assert.equal(sandbox.parseBoundedInteger('720', 1, 720), 720);
  assert.equal(sandbox.parseBoundedInteger('721', 1, 720), null);
});
