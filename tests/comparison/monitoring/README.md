# CSV Export & Charts

After a test run, `results.csv` is written next to the binary (path configurable via `metrics.csv_path` in `test-config.yaml`).

## CSV Schema

```
timestamp,db,op,clients,stage_duration_s,requests,throughput_rps,mean_ms,p50_ms,p90_ms,p95_ms,p99_ms
```

| Column | Description |
|---|---|
| `timestamp` | RFC3339 UTC time of stage end |
| `db` | Database name (`redis`, `lume`, `memcached`) |
| `op` | Operation (`set` / `get`) |
| `clients` | Concurrent clients during this stage |
| `stage_duration_s` | Actual wall-clock stage duration (seconds) |
| `requests` | Requests completed **during this stage** (delta) |
| `throughput_rps` | `requests / stage_duration_s` |
| `mean_ms` | Mean latency (ms) |
| `p50_ms` | Median latency (ms) |
| `p90_ms` | 90th percentile latency (ms) |
| `p95_ms` | 95th percentile latency (ms) |
| `p99_ms` | 99th percentile latency (ms) |

Latency values are computed from histogram bucket deltas — each row reflects only the activity of that stage, not cumulative totals.

---

## Charts & Tables

### 1. Throughput vs Concurrency
**Chart type**: line chart  
**X**: `clients` | **Y**: `throughput_rps` | **Series**: one line per `db`  
**Filter**: `op = "set"`  
**What it shows**: at what concurrency each DB saturates.

### 2. Latency Percentiles vs Concurrency
**Chart type**: three line charts — p50, p95, p99  
**X**: `clients` | **Y**: latency (ms) | **Series**: one line per `db`  
**Filter**: `op = "set"`  
**What it shows**: how latency tail grows under load.

### 3. Peak-Load Comparison Table
Filter rows where `clients = max_clients`, group by `db`:

| DB | Throughput RPS | Mean ms | p50 ms | p95 ms | p99 ms |
|---|---|---|---|---|---|
| redis | … | … | … | … | … |
| lume | … | … | … | … | … |

### 4. Latency CDF (per DB at fixed concurrency)
**Chart type**: line chart, log-scale X  
**Points per DB**: `(p50, 0.50)`, `(p90, 0.90)`, `(p95, 0.95)`, `(p99, 0.99)`  
**What it shows**: shape of the latency distribution — heavy tail vs tight.

---

## Python Quickstart

```python
import pandas as pd
import matplotlib.pyplot as plt

df = pd.read_csv("results.csv")
sets = df[df["op"] == "set"]

# 1. Throughput vs Concurrency
fig, ax = plt.subplots()
for db, g in sets.groupby("db"):
    ax.plot(g["clients"], g["throughput_rps"], marker="o", label=db)
ax.set_xlabel("Clients"); ax.set_ylabel("RPS"); ax.legend()
ax.set_title("Throughput vs Concurrency")
fig.savefig("throughput.png", dpi=150)

# 2. p99 Latency vs Concurrency
fig, ax = plt.subplots()
for db, g in sets.groupby("db"):
    ax.plot(g["clients"], g["p99_ms"], marker="o", label=db)
ax.set_xlabel("Clients"); ax.set_ylabel("p99 latency (ms)"); ax.legend()
ax.set_title("p99 Latency vs Concurrency")
fig.savefig("p99_latency.png", dpi=150)

# 3. Peak-load comparison table
peak = sets[sets["clients"] == sets["clients"].max()]
table = peak[["db", "throughput_rps", "mean_ms", "p50_ms", "p95_ms", "p99_ms"]]
print(table.to_markdown(index=False))

# 4. Latency CDF at max concurrency
fig, ax = plt.subplots()
for db, g in peak.groupby("db"):
    row = g.iloc[0]
    xs = [row.p50_ms, row.p90_ms, row.p95_ms, row.p99_ms]
    ys = [0.50, 0.90, 0.95, 0.99]
    ax.plot(xs, ys, marker="o", label=db)
ax.set_xscale("log"); ax.set_xlabel("Latency (ms)"); ax.set_ylabel("Percentile")
ax.set_title(f"Latency CDF at {sets['clients'].max()} clients")
ax.legend()
fig.savefig("latency_cdf.png", dpi=150)
```

Requires: `pip install pandas matplotlib tabulate`