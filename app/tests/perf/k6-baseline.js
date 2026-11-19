// Base Hub load harness (SRS §4.1, PERF-01).
//
// Exercises the four HTTP rows of the performance table against a running,
// seeded server and encodes the provisional thresholds; any breach makes k6
// exit non-zero with the measured percentiles in the summary.
//
//   k6 run k6-baseline.js
//   BASE_URL=http://localhost:8180 PERF_LOGIN=admin PERF_PASSWORD=... k6 run k6-baseline.js
//
// Rows and thresholds (SRS §4.1, provisional until real baselines exist):
//   search_read  p95 < 300 ms   (≤ 80 records, warm process, seeded dataset)
//   create/write p95 < 400 ms   (single record incl. ACL + record rules)
//   webhook      p95 < 1 s      (≤ 5 sync steps, no outbound HTTP in path)
//   login        p95 < 500 ms   (bcrypt cost ≥ 10 dominates — do not lower it)
//   mixed        ≥ 100 req/s sustained with the row p95s still holding

import http from 'k6/http';
import encoding from 'k6/encoding';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8181';
const LOGIN = __ENV.PERF_LOGIN || 'admin';
const PASSWORD = __ENV.PERF_PASSWORD || 'admin';
const DB_NAME = __ENV.PERF_DB || ''; // optional X-Database-Name
// search target: a table expected to be populated on a seeded DB.
const SEARCH_MODEL = __ENV.PERF_SEARCH_MODEL || 'sys.config_param';
// write target: rows are created, written, then unlinked so repeated runs do
// not grow the database.
const WRITE_MODEL = __ENV.PERF_WRITE_MODEL || 'tbl.partner';
// webhook topic: the /wh/<topic> row only runs when a seeded topic is given,
// because firing an unknown topic measures the 404 path, not the pipeline.
const WH_TOPIC = __ENV.PERF_WH_TOPIC || '';
const DURATION = __ENV.PERF_DURATION || '1m';
const VUS = Number(__ENV.PERF_VUS || 10);
const MIXED_RPS = Number(__ENV.PERF_MIXED_RPS || 100);

const AUTH_HEADER = 'Basic ' + encoding.b64encode(`${LOGIN}:${PASSWORD}`);

function rpcHeaders(extraTags) {
  const headers = {
    'Content-Type': 'application/json',
    Authorization: AUTH_HEADER,
  };
  if (DB_NAME) headers['X-Database-Name'] = DB_NAME;
  return { headers, tags: extraTags };
}

const scenarios = {
  search_read: {
    executor: 'constant-vus',
    vus: VUS,
    duration: DURATION,
    exec: 'searchRead',
  },
  create_write: {
    executor: 'constant-vus',
    vus: Math.max(1, Math.floor(VUS / 2)),
    duration: DURATION,
    exec: 'createWrite',
  },
  login: {
    executor: 'constant-vus',
    vus: Math.max(1, Math.floor(VUS / 2)),
    duration: DURATION,
    exec: 'login',
  },
  // Sustained mixed RPC throughput (SRS row 5): constant arrival rate keeps
  // pressure at the target even when individual requests slow down.
  mixed: {
    executor: 'constant-arrival-rate',
    rate: MIXED_RPS,
    timeUnit: '1s',
    duration: DURATION,
    preAllocatedVUs: MIXED_RPS,
    maxVUs: MIXED_RPS * 2,
    exec: 'mixed',
  },
};
if (WH_TOPIC) {
  scenarios.webhook = {
    executor: 'constant-vus',
    vus: Math.max(1, Math.floor(VUS / 2)),
    duration: DURATION,
    exec: 'webhook',
  };
}

export const options = {
  scenarios,
  thresholds: {
    // Row thresholds apply across every scenario (including mixed) via tags,
    // so throughput pressure cannot quietly relax the latency contract.
    'http_req_duration{row:search_read}': ['p(95)<300'],
    'http_req_duration{row:write}': ['p(95)<400'],
    'http_req_duration{row:login}': ['p(95)<500'],
    ...(WH_TOPIC ? { 'http_req_duration{row:webhook}': ['p(95)<1000'] } : {}),
    'http_req_failed{scenario:mixed}': ['rate<0.01'],
  },
};

export function searchRead() {
  const res = http.post(
    `${BASE_URL}/rpc/${SEARCH_MODEL}/search_read`,
    JSON.stringify({ args: [], kwargs: { fields: ['id', 'name'], limit: 80 } }),
    rpcHeaders({ row: 'search_read' })
  );
  check(res, { 'search_read 200': (r) => r.status === 200 });
}

export function createWrite() {
  const created = http.post(
    `${BASE_URL}/rpc/${WRITE_MODEL}/create`,
    JSON.stringify({ args: [], kwargs: { values: { name: `perf-01 probe ${__VU}-${__ITER}` } } }),
    rpcHeaders({ row: 'write' })
  );
  const ok = check(created, { 'create 200': (r) => r.status === 200 });
  if (!ok) return;
  const id = created.json('result.id');
  if (!id) return;
  http.post(
    `${BASE_URL}/rpc/${WRITE_MODEL}/write/${id}`,
    JSON.stringify({ args: [], kwargs: { values: { name: `perf-01 probe ${__VU}-${__ITER} v2` } } }),
    rpcHeaders({ row: 'write' })
  );
  // Cleanup is untagged so unlink latency never contaminates the write row.
  http.post(
    `${BASE_URL}/rpc/${WRITE_MODEL}/unlink/${id}`,
    JSON.stringify({ args: [], kwargs: {} }),
    rpcHeaders({ row: 'cleanup' })
  );
}

export function login() {
  const res = http.post(
    `${BASE_URL}/oauth/token`,
    JSON.stringify({ params: { db: DB_NAME, login: LOGIN, password: PASSWORD, context: {} } }),
    { headers: { 'Content-Type': 'application/json' }, tags: { row: 'login' } }
  );
  check(res, { 'login 200': (r) => r.status === 200 });
  // The login row measures bcrypt-dominated latency, not the SEC-03 limiter;
  // pace requests so one VU never trips the per-login+IP sliding window.
  sleep(1);
}

export function webhook() {
  const res = http.post(
    `${BASE_URL}/wh/${WH_TOPIC}`,
    JSON.stringify({ perf: true, vu: __VU, iter: __ITER }),
    { headers: { 'Content-Type': 'application/json' }, tags: { row: 'webhook' } }
  );
  check(res, { 'webhook 2xx': (r) => r.status >= 200 && r.status < 300 });
}

export function mixed() {
  // Mixed RPC pressure: mostly reads with a write slice, mirroring the SRS
  // "mixed RPC" throughput condition.
  if (__ITER % 5 === 4) {
    createWrite();
  } else {
    searchRead();
  }
}
