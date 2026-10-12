'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

function restartHarness(fetchResult) {
  let elapsed = 0;
  const requests = [], navigations = [], errors = [];
  const current = new URL('https://old.example.com:14430/');
  const sandbox = {
    URL, AbortSignal,
    Date: class extends Date { static now() { return elapsed; } },
    setTimeout(callback, delay) { elapsed += delay; queueMicrotask(callback); },
    window: { location: { href: current.href, origin: current.origin,
      replace(url) { navigations.push({url, elapsed}); } } },
    Toast: { info() {}, error(message) { errors.push(message); } },
    async fetch(url, options) {
      requests.push({url, options});
      return fetchResult(requests.length);
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/static/js/pages/panel-settings.js'), 'utf8'), sandbox);
  return {wait: sandbox.waitForPanelRestart, requests, navigations, errors};
}

for (const target of ['https://new.example.com:14430/', 'https://old.example.com:14431/', 'https://old.example.com:14430/']) {
  test(`restart waits for actual readiness at ${target}`, async () => {
    const h = restartHarness(attempt => {
      if (attempt === 1) throw new TypeError('Connection refused');
      // Cover an HTTP error, an unrelated HTML response, and the old process.
      if (attempt === 2) return {ok: false, status: 503};
      if (attempt === 3) return {ok: true, async json() { throw new SyntaxError('Not JSON'); }};
      return {ok: true, async json() { return {ready: attempt >= 6}; }};
    });
    await h.wait(target);
    assert.equal(h.requests.length, 6);
    assert.equal(h.navigations.length, 1);
    assert.equal(h.navigations[0].url, target);
    assert.ok(h.navigations[0].elapsed > 5000, 'slow restarts must not navigate early');
    assert.equal(h.requests[0].url, new URL('/api/panel-ready', target).href);
    for (const {options} of h.requests) {
      assert.equal(options.mode, 'cors');
      assert.equal(options.credentials, 'omit');
      assert.equal(options.redirect, 'error');
      assert.ok(options.signal instanceof AbortSignal);
    }
    assert.deepEqual(h.errors, []);
  });
}

test('an unavailable new address times out without navigating to an error page', async () => {
  const h = restartHarness(() => ({ok: false, status: 502}));
  await h.wait('https://unavailable.example.com:14430/');
  assert.equal(h.navigations.length, 0);
  assert.equal(h.errors.length, 1);
  assert.match(h.errors[0], /重启超时/);
  assert.match(h.errors[0], /https:\/\/unavailable.example.com:14430/);
  assert.ok(h.requests.length < 100, 'unsuccessful HTTP responses must back off');
});
