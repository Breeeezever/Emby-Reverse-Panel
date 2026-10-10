'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const ROOT = path.join(__dirname, '..');
const indexSource = fs.readFileSync(path.join(ROOT, 'web/static/index.html'), 'utf8');
const appSource = fs.readFileSync(path.join(ROOT, 'web/static/js/app.js'), 'utf8');
const dashboardSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/dashboard.js'), 'utf8');
const cssSource = fs.readFileSync(path.join(ROOT, 'web/static/css/style.css'), 'utf8');

test('GitHub project link points to the official repository', () => {
  assert.match(indexSource, /class="github-project-link" href="https:\/\/github\.com\/snnabb\/Meridian"/);
  assert.doesNotMatch(indexSource, /github\.com\/chanhui800\/Meridian/);
});

test('site modal resets overlay and content scroll on every open', () => {
  assert.match(appSource, /function resetModalScroll\(\)/);
  assert.match(appSource, /overlay\.scrollTop = 0/);
  assert.match(appSource, /body\.scrollTop = 0/);
  assert.match(appSource, /requestAnimationFrame\(resetModalScroll\)/);
});

test('site modal applies and removes its responsive modal class', () => {
  assert.match(appSource, /activeModalClass/);
  assert.match(appSource, /modal\.classList\.add\(activeModalClass\)/);
  assert.match(appSource, /modal\.classList\.remove\(activeModalClass\)/);
});

test('dashboard trends trace smooth curves instead of only straight segments', () => {
  assert.match(dashboardSource, /function dashboardTraceSmoothLine\(ctx, points\)/);
  assert.match(dashboardSource, /ctx\.bezierCurveTo\(/);
  assert.match(dashboardSource, /dashboardTraceSmoothLine\(ctx, pointsOnCanvas\)/);
});

test('mobile navigation keeps the header and drawer available', () => {
  assert.match(cssSource, /@media \(max-width: 768px\)[\s\S]*?\.app-header \{[\s\S]*?display: flex;[\s\S]*?\.sidebar \{[\s\S]*?display: flex;/);
  assert.match(cssSource, /#app-shell\.sidebar-expanded \.sidebar\s*\{\s*transform: translateX\(0\);\s*visibility: visible;\s*\}/);
});
test('upstream header rows reset fieldsets and retain an explicit action column', () => {
  assert.match(cssSource, /\.form-list-row\.upstream-header-row\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:[^;]*58px;[^}]*border:\s*0;/);
  assert.match(cssSource, /\.upstream-header-row \.form-row-action\s*\{[^}]*white-space:\s*nowrap;/);
});

test('dashboard assets use a cache-busting revision after chart and mobile fixes', () => {
  assert.match(indexSource, /\?v=1\.13\.0/);
});

test('desktop sidebar width and content offset share the expanded state', () => {
  assert.match(cssSource, /#app-shell\s*\{[^}]*--sidebar-w:\s*76px;/);
  assert.match(cssSource, /#app-shell\.sidebar-expanded\s*\{[^}]*--sidebar-w:\s*224px;/);
  assert.match(cssSource, /\.sidebar\s*\{[^}]*width:\s*var\(--sidebar-w\);/);
  assert.match(cssSource, /\.main\s*\{[^}]*margin-left:\s*var\(--sidebar-w\);/);
  assert.match(cssSource, /\.app-header\s*\{[^}]*margin-left:\s*var\(--sidebar-w\);/);
});

test('mobile upstream rows keep address full-width and pair protocol with port', () => {
  assert.match(cssSource, /grid-template-areas:\s*"enabled actions" "name name" "scheme port" "address address" "latency latency"/);
  assert.match(cssSource, /\.upstream-line-address-field\s*\{[^}]*grid-area:\s*address;/);
  assert.match(cssSource, /\.upstream-line-port-field\s*\{[^}]*grid-area:\s*port;/);
});

test('shared selectors provide a dropdown arrow without replacing their background color', () => {
  assert.match(cssSource, /--select-arrow:\s*url\(/);
  assert.match(cssSource, /\.form-select,\s*select\.form-input\s*\{[^}]*padding-right:\s*40px;[^}]*background-image:\s*var\(--select-arrow\);[^}]*background-position:\s*right 13px center;/);
  assert.match(cssSource, /\.dashboard-trend-controls \.form-select\s*\{[^}]*background-color:\s*var\(--panel\);/);
});

test('document scrolling stays on the root for iOS status-bar tap-to-top', () => {
  assert.match(cssSource, /html\s*\{[^}]*overflow-y:\s*auto;[^}]*-webkit-overflow-scrolling:\s*touch;/);
  assert.match(cssSource, /body\s*\{[^}]*overflow-y:\s*visible;/);
});

test('trend selectors and upstream actions retain comfortable control heights', () => {
  assert.match(cssSource, /\.dashboard-trend-controls \.form-select\s*\{[^}]*min-height:\s*42px;/);
  assert.match(cssSource, /\.upstream-lines-buttons > button\s*\{[^}]*height:\s*36px;/);
  assert.match(cssSource, /@media \(max-width: 768px\)[\s\S]*\.upstream-lines-buttons > button\s*\{[^}]*min-height:\s*38px;/);
});

test('site editor latency colors survive the modal value override', () => {
  assert.match(cssSource, /\.site-config-modal \.upstream-line-latency\.good \{\s*color: var\(--green\);\s*\}/);
  assert.match(cssSource, /\.site-config-modal \.upstream-line-latency\.warn \{\s*color: var\(--orange\);\s*\}/);
  assert.match(cssSource, /\.site-config-modal \.upstream-line-latency\.bad \{\s*color: var\(--red\);\s*\}/);
});

test('mobile drawer backdrop and header responsive structure are active', () => {
  assert.match(indexSource, /class="sidebar-backdrop" id="sidebar-backdrop"/);
  assert.match(cssSource, /\.sidebar-backdrop \{[\s\S]*?position: fixed;/);
});

test('site modal introduces tabbed panels to avoid infinite vertical scrolling', () => {
  const sitesSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/sites.js'), 'utf8');
  assert.match(sitesSource, /class="site-modal-tabs"/);
  assert.match(sitesSource, /id="site-panel-basic"/);
  assert.match(sitesSource, /id="site-panel-lines"/);
  assert.doesNotMatch(sitesSource, /id="site-panel-discovery"/);
  assert.match(sitesSource, /id="site-panel-advanced"/);
  assert.match(cssSource, /\.site-modal-tabs \{/);
  assert.match(cssSource, /\.site-modal-tab\.active \{/);
});

test('backend logout only clears the current device session cookie', () => {
  const httpSource = fs.readFileSync(path.join(ROOT, 'cmd/meridian/app_http.go'), 'utf8');
  assert.match(httpSource, /func \(a \*App\) handleLogout/);
  assert.doesNotMatch(httpSource, /func \(a \*App\) handleLogout[\s\S]*InvalidateAllSessions/);
  assert.match(httpSource, /a\.clearSessionCookie\(w, r\)/);
});

test('site modal supports multiple playback origin nodes and dynamic stream hosts', () => {
  const sitesSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/sites.js'), 'utf8');
  assert.match(sitesSource, /id="m-playback-target"/);
  assert.match(sitesSource, /id="m-add-stream-host"/);
  assert.match(sitesSource, /id="m-stream-hosts-list"/);
  assert.match(sitesSource, /configuredStreamHosts/);
  assert.match(sitesSource, /stream_hosts: streamHostValues/);
});

test('mobile settings constrain grids and preserve readable controls', () => {
  const mobile = cssSource.slice(cssSource.indexOf('@media (max-width: 768px)'));
  assert.match(mobile, /\.settings-layout\s*\{[^}]*display:\s*block;[^}]*width:\s*100%;/);
  assert.match(mobile, /\.settings-grid,\s*\.settings-two-column,\s*\.settings-check-grid\s*\{[^}]*grid-template-columns:\s*1fr;/);
  assert.match(cssSource, /\.settings-panel\s*\{[^}]*overflow-wrap:\s*anywhere;/);
  assert.match(mobile, /\.form-input,\s*\.form-select\s*\{[^}]*font-size:\s*16px;/);
});

test('zero-flicker page switching avoids blank screen opacity drops and preserves skeletons', () => {
  const routerSource = fs.readFileSync(path.join(ROOT, 'web/static/js/router.js'), 'utf8');
  const dashboardSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/dashboard.js'), 'utf8');
  const sitesSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/sites.js'), 'utf8');
  assert.doesNotMatch(routerSource, /target\.classList\.add\('active', 'page-entering'\)/);
  assert.match(dashboardSource, /if \(!page\.querySelector\('#dash-stats'\)\)/);
  assert.match(sitesSource, /if \(!page\.querySelector\('#sites-grid'\)\)/);
});

test('account session card accurately describes current-device logout', () => {
  const accountSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/account.js'), 'utf8');
  assert.match(accountSource, /仅退出当前设备，其他设备不受影响/);
  assert.doesNotMatch(accountSource, /作废所有设备的登录会话/);
});

test('major cards honor the configurable UI radius', () => {
  assert.match(cssSource, /\.glass-card,[^{]+\{[^}]*border-radius:\s*var\(--ui-radius\);/);
});

test('request log detail avoids raw data-copy attribute in innerHTML and pauses auto-refresh when expanded', () => {
  const reqLogSource = fs.readFileSync(path.join(ROOT, 'web/static/js/pages/request-logs.js'), 'utf8');
  assert.doesNotMatch(reqLogSource, /data-copy="\$\{esc\(entry\.path/);
  assert.match(reqLogSource, /!document\.querySelector\('\.log-detail-row'\)/);
});
