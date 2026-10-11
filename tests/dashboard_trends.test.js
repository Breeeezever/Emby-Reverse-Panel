'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const dashboardSource = fs.readFileSync(
  path.join(__dirname, '..', 'web', 'static', 'js', 'pages', 'dashboard.js'),
  'utf8',
);

test('dashboard trend controls expose a minute-precision custom range', () => {
  assert.match(dashboardSource, /<option value="month">本月<\/option>/);
  assert.match(dashboardSource, /<option value="custom">自定义<\/option>/);
  assert.match(dashboardSource, /type="datetime-local"[^>]*id="dashboard-trend-start"[^>]*step="60"/);
  assert.match(dashboardSource, /type="datetime-local"[^>]*id="dashboard-trend-end"[^>]*step="60"/);
  assert.match(dashboardSource, /id="dashboard-trend-apply"/);
});

test('dashboard custom range validates ordering before loading', () => {
  assert.match(dashboardSource, /结束时间必须晚于开始时间/);
  assert.match(dashboardSource, /dashboardTrendState\.customStart = current\.start/);
  assert.match(dashboardSource, /dashboardTrendState\.customEnd = current\.end/);
});

test('dashboard trend tooltip lists all site names or only the selected site', () => {
  assert.match(dashboardSource, /site_series/);
  assert.match(dashboardSource, /dashboardTrendState\.siteId === 'all'/);
  assert.match(dashboardSource, /dashboardTrendMetricLine/);
  assert.match(dashboardSource, /series\.site_name/);
  assert.match(dashboardSource, /selectedOption\?\.textContent/);
});

test('dashboard realtime trend keeps historical points after a page refresh', () => {
  assert.match(dashboardSource, /function dashboardTrendRealtimeOffset\(\)/);
  assert.match(dashboardSource, /historicalPoints\.slice\(0, offset\)\.concat\(realtimePoints\)/);
  assert.match(dashboardSource, /if \(!realtimePoints\.length \|\| !historicalPoints\.length\) return realtimePoints\.length \? realtimePoints : historicalPoints/);
});

test('dashboard trend rendering handles invalid values and live samples', () => {
  assert.match(dashboardSource, /function dashboardSafeNonNegative\(value\)/);
  assert.match(dashboardSource, /Number\.isFinite\(numeric\)/);
  assert.match(dashboardSource, /if \(liveMap\.size > 0\) appendRealtimeTrendSample/);
  assert.match(dashboardSource, /const unit = units\[i\] \|\| units\[0\]/);
  assert.match(dashboardSource, /bucketSeconds > 0 && bucketSeconds < 3600/);
});

test('dashboard binds trend controls after SSE cleanup and removes pointer handlers', () => {
  assert.ok(
    dashboardSource.indexOf('startDashSSE();') < dashboardSource.indexOf('setupDashboardTrendControls();'),
    'SSE cleanup must happen before chart controls are registered',
  );
  assert.match(dashboardSource, /let dashboardTrendControlsCleanup = null;/);
  assert.match(dashboardSource, /canvas\.removeEventListener\('pointermove'/);
  assert.match(dashboardSource, /function stopDashSSE\(\) \{[\s\S]*dashboardTrendControlsCleanup/);
});

test('dashboard renders a numeric zero over the initial placeholder', () => {
  const element = { style: {}, textContent: '—' };
  const sandbox = {
    document: {
      getElementById(id) {
        return id === 's-total' ? element : null;
      },
    },
    setTimeout(callback) { callback(); },
  };
  vm.runInNewContext(dashboardSource, sandbox, { filename: 'dashboard.js' });

  sandbox.animateValue('s-total', 0);
  assert.equal(element.textContent, '0');
});

function traceCurve(points) {
  const sandbox = {};
  vm.runInNewContext(dashboardSource, sandbox);
  const segments = [];
  let start;
  const context = {
    moveTo(x, y) { start = { x, y }; },
    lineTo(x, y) { segments.push({ start, end: { x, y } }); start = { x, y }; },
    bezierCurveTo(x1, y1, x2, y2, x, y) {
      segments.push({ start, first: { x: x1, y: y1 }, second: { x: x2, y: y2 }, end: { x, y } });
      start = { x, y };
    },
  };
  sandbox.dashboardTraceSmoothLine(context, points);
  return segments;
}

function cubic(a, b, c, d, t) {
  return (1 - t) ** 3 * a + 3 * (1 - t) ** 2 * t * b + 3 * (1 - t) * t ** 2 * c + t ** 3 * d;
}

test('smooth trends pass through samples without inventing peaks or negative values', () => {
  const sequences = [[0, 0, 100, 0, 0], [0, 1, 1000, 1001, 10, 0], [5, 5, 5, 5], [100, 25, 5, 0]];
  for (const values of sequences) {
    // Uneven x spacing also exercises monotonicity at narrow intervals.
    const points = values.map((y, i) => ({ x: i * i + i, y }));
    const segments = traceCurve(points);
    assert.equal(segments.length, points.length - 1);
    segments.forEach((segment, i) => {
      assert.deepEqual(segment.start, points[i]);
      assert.deepEqual(segment.end, points[i + 1]);
      const lo = Math.min(segment.start.y, segment.end.y), hi = Math.max(segment.start.y, segment.end.y);
      let previousX = segment.start.x;
      for (let step = 0; step <= 100; step++) {
        const t = step / 100;
        const y = cubic(segment.start.y, segment.first.y, segment.second.y, segment.end.y, t);
        const x = cubic(segment.start.x, segment.first.x, segment.second.x, segment.end.x, t);
        assert.ok(y >= lo - 1e-9 && y <= hi + 1e-9, `overshoot at segment ${i}: ${y}`);
        assert.ok(x >= previousX - 1e-9, 'the curve must not double back in time');
        previousX = x;
      }
    });
  }
});

test('neighboring curves share a tangent and flatten smoothly at peaks', () => {
  const segments = traceCurve([0, 2, 10, 8, 8, 3, 1].map((y, i) => ({ x: i * 20, y })));
  for (let i = 0; i < segments.length - 1; i++) {
    const left = segments[i], right = segments[i + 1];
    const incoming = (left.end.y - left.second.y) / (left.end.x - left.second.x);
    const outgoing = (right.first.y - right.start.y) / (right.first.x - right.start.x);
    assert.ok(Math.abs(incoming - outgoing) < 1e-10, 'no sharp join between segments');
  }
  assert.equal(segments[1].second.y, 10);
  assert.equal(segments[2].first.y, 10);
  assert.notEqual(segments[0].second.y, 4 / 3, 'a changing trend should curve away from the straight chord');
});

test('empty, single-point and duplicate-time trends remain drawable', () => {
  assert.deepEqual(traceCurve([]), []);
  assert.deepEqual(traceCurve([{ x: 10, y: 4 }]), []);
  const points = [{ x: 10, y: 4 }, { x: 10, y: 8 }, { x: 20, y: 0 }];
  assert.deepEqual(traceCurve(points).map(segment => segment.end), points.slice(1));
});
