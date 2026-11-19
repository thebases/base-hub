# Load harness (SRS §4.1, PERF-01)

k6 script exercising the four HTTP rows of the SRS performance table against a
**running, seeded** Base Hub server. Thresholds are encoded from §4.1; any
breach fails the run (k6 exits non-zero) and the summary reports the measured
percentiles. Tool choice (k6) is the recorded owner decision of 2026-07-06.

## Running

```bash
# from app (server must already be running)
make perf
# or directly, with overrides:
BASE_URL=http://localhost:8180 PERF_LOGIN=admin PERF_PASSWORD=... \
  k6 run tests/perf/k6-baseline.js
```

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `BASE_URL` | `http://localhost:8180` | server under test |
| `PERF_LOGIN` / `PERF_PASSWORD` | `admin` / `admin` | RPC + login credentials |
| `PERF_DB` | (empty) | optional `X-Database-Name` binding |
| `PERF_SEARCH_MODEL` | `sys.config_param` | `search_read` target model |
| `PERF_WRITE_MODEL` | `tbl.partner` | create/write target (rows are unlinked after each iteration) |
| `PERF_WH_TOPIC` | (empty) | seeded webhook topic; the `/wh/*` row is skipped when unset |
| `PERF_DURATION` | `1m` | per-scenario duration |
| `PERF_VUS` | `10` | virtual users for the latency scenarios |
| `PERF_MIXED_RPS` | `100` | arrival rate of the sustained-throughput scenario |

## Thresholds (provisional, from SRS §4.1)

| Row | Threshold | Notes |
|---|---|---|
| `search_read` | p95 < 300 ms | measure against a dataset ≥ 100k rows in the target table for the official number |
| `create`/`write` | p95 < 400 ms | single record incl. ACL/record-rule checks |
| webhook `/wh/*` | p95 < 1 s | requires `PERF_WH_TOPIC` pointing at a seeded flow with ≤ 5 sync steps and no outbound HTTP |
| login | p95 < 500 ms | bcrypt-dominated; do **not** "fix" a breach by lowering bcrypt cost (SEC-01) |
| mixed throughput | ≥ 100 req/s | constant arrival rate; error rate < 1% and the row p95s above still hold |

## Caveats

- Login iterations pace at ≥ 1 s per VU so the SEC-03 rate limiter's per-IP
  sliding window (30/min) is not what gets measured.
- Numbers are only comparable when produced under the §4.1 measurement
  conditions (warm process, seeded dataset, 4-core host for the throughput
  row). Treat anything else as smoke, not baseline.
