# RAgent Router

**AI API Gateway with Resilience Engine — built in Go.**

A transparent proxy layer between AI coding assistants (Claude Code, Cursor, etc.) and multiple LLM providers, featuring circuit breaking, rate limiting, retry with jitter, token tracking, intelligent routing, and cost analytics.

## Why This Exists

Using Claude Code daily, I ran into three problems:

1. **No fault tolerance** — when an upstream API returns 5xx or gets throttled, all requests fail. No retry, no fallback, no circuit breaking.
2. **No cost visibility** — token consumption and API spend are completely opaque.
3. **No intelligent routing** — simple "explain this" queries and complex architecture designs all hit the same expensive model.

RAgent Router sits between the AI tool and the providers, solving all three without changing the client workflow.

## Architecture

```
Claude Code / AI Client
        │
        │  POST /v1/messages (Anthropic-compatible, SSE streaming)
        │
   ┌────▼──────────────────────────────────────────┐
   │              RAgent Router (Go)                │
   │                                                │
   │  ┌──────────────────────────────────────────┐ │
   │  │         Resilience Pipeline               │ │
   │  │                                          │ │
   │  │  Rate Limiter → Circuit Breaker → Retry  │ │
   │  │       → Bulkhead → Timeout Cascade       │ │
   │  └──────────────────────────────────────────┘ │
   │                     │                          │
   │  ┌──────────────────────────────────────────┐ │
   │  │         Routing Engine                    │ │
   │  │   Rule-based + keyword matching → Model  │ │
   │  └──────────────────────────────────────────┘ │
   │                     │                          │
   │  ┌──────────────────────────────────────────┐ │
   │  │         Observability                     │ │
   │  │   Token tracking, cost estimation,       │ │
   │  │   SQLite persistence, Dashboard API      │ │
   │  └──────────────────────────────────────────┘ │
   └────┬──────────────────────────────────────────┘
        │
   ┌────▼────┐  ┌────▼────┐  ┌────▼────┐
   │  GPT-4  │  │ Claude  │  │DeepSeek │
   └─────────┘  └─────────┘  └─────────┘
```

## Project Structure

```
ragent-router/
├── backend/                          # Go backend
│   ├── cmd/server/main.go            # HTTP server entry point
│   ├── internal/
│   │   ├── resilience/               # ★ Core: resilience engine
│   │   │   ├── circuitbreaker/       #   3-state breaker + sliding window
│   │   │   ├── ratelimit/            #   Token bucket + sharded store
│   │   │   ├── retry/                #   Exponential backoff + 3 jitter strategies
│   │   │   ├── bulkhead/             #   Semaphore concurrency limiter
│   │   │   └── timeout/              #   Context cascading deadlines
│   │   ├── proxy/                    # Anthropic SSE streaming proxy
│   │   ├── routing/                  # Keyword-based rule engine
│   │   └── store/                    # SQLite persistence + analytics
│   └── demo/demo_test.go             # 8 integration test scenarios
│
├── frontend/                         # React dashboard (Electron)
│   ├── src/
│   │   ├── api/index.ts              # API client
│   │   ├── pages/Dashboard.tsx       # Dashboard page
│   │   ├── pages/TrafficMonitor.tsx  # Traffic monitoring
│   │   └── components/               # Charts, tables, settings
│   └── package.json
│
└── README.md
```

## Technical Highlights

### 1. Circuit Breaker — Three-State + Sliding Window

```
Closed ──(failure rate > threshold)──→ Open
Open   ──(timeout expires)───────────→ HalfOpen
HalfOpen ──(probe succeeds)──────────→ Closed
HalfOpen ──(probe fails)─────────────→ Open
```

- Failure rate computed over a sliding window of configurable time buckets
- Per-provider circuit breakers isolate failures independently
- Half-open state only permits a configurable number of probe requests

### 2. Token Bucket — Lazy Refill + Sharded Lock

The common path (tokens available) costs **one mutex lock + one integer decrement**. Time-based refill is deferred until the bucket empties, avoiding `time.Now()` syscall overhead on every request.

```
BenchmarkTokenBucket_Allow-32    42440167    28.32 ns/op    0 B/op    0 allocs/op
```

Under the hood, the rate limiter store uses **FNV-64a hashing** to distribute keys across **2048 shards**, each protected by its own `sync.RWMutex`. The `Load` path uses **double-checked locking** to avoid write-lock contention on cache hits:

```go
// Fast path: optimistic read (no contention)
sh.mu.RLock()
v, ok := sh.data[key]
sh.mu.RUnlock()
if ok { return v }

// Slow path: write lock + double-check (only on miss)
sh.mu.Lock()
v, ok = sh.data[key]  // ← re-check to prevent TOCTOU race
if ok { return v }
sh.data[key] = newVal
sh.mu.Unlock()
```

### 3. Retry — Jitter Strategies

| Strategy | Formula | Best For |
|---|---|---|
| Full Jitter | `random(0, cap)` | Avoiding thundering herd |
| Equal Jitter | `cap/2 + random(0, cap/2)` | Balanced timing + spread |
| Decorrelated Jitter | `min(cap, random(base, cap×3))` | Independent retry timing across nodes |

### 4. Context Cascading Timeout

Child deadlines are constrained by their parent. A retry loop with 10s per-attempt won't accidentally run for 50s if the parent has a 30s budget.

### 5. Provider Adapter Pattern

Abstracts protocol differences between AI providers. The proxy speaks Anthropic Messages API natively; adapters translate to provider-specific formats:

```go
type ProviderAdapter interface {
    BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (
        url string, reqHeaders map[string]string, reqBody []byte, err error)
}
```

New providers only need to implement this interface — no changes to the proxy core.

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 18+

### Backend

```bash
cd backend

# Install dependencies
GOPROXY=https://goproxy.cn,direct go mod download

# Run (with default providers from env vars)
DEEPSEEK_API_KEY=sk-your-key \
CLAUDE_API_KEY=sk-ant-your-key \
go run ./cmd/server

# Or configure via JSON
PROVIDERS='[{"id":"1","name":"DeepSeek","base_url":"https://api.deepseek.com","api_key":"sk-...","model":"deepseek-chat","enabled":true}]' \
go run ./cmd/server
```

The server starts on `http://localhost:15722`.

### Frontend

```bash
cd frontend
npm install
npm start
```

### Test

```bash
cd backend
go test -v ./demo/          # 8 integration scenarios
go test -bench=. ./demo/    # Benchmarks
```

## API Endpoints

### Proxy (Anthropic-compatible)

| Method | Path | Description |
|---|---|---|
| POST | `/v1/messages` | SSE streaming proxy (Claude Code calls this) |
| GET | `/v1/messages` | Health check |

### Dashboard

| Method | Path | Description |
|---|---|---|
| GET | `/api/dashboard/overview` | Today/month cost, total requests |
| GET | `/api/dashboard/model-distribution` | Requests per model with percentages |
| GET | `/api/dashboard/recent-routes` | Recent request log (configurable limit) |
| GET | `/api/dashboard/cost-trend` | Daily cost + request count timeline |

### Monitoring

| Method | Path | Description |
|---|---|---|
| GET | `/api/monitor/overview` | Aggregate stats (tokens, cost, latency, errors) |
| GET | `/api/monitor/recent` | Raw request log entries |
| GET | `/api/monitor/by-model` | Per-model latency + cost breakdown |

### Provider Management

| Method | Path | Description |
|---|---|---|
| GET | `/api/proxy/current` | Currently active provider |
| POST | `/api/proxy/activate/{id}` | Switch active provider |
| GET | `/api/proxy/health` | System health + warnings |
| GET | `/api/ccswitch/providers` | List all registered providers |

### Resilience

| Method | Path | Description |
|---|---|---|
| GET | `/api/resilience/stats` | Per-provider circuit breaker states |

### Health

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Service health check |

## Routing Rules

Default rules (configurable via code):

| Rule | Keywords | Target | Priority |
|---|---|---|---|
| Architecture & Design | architecture, design, refactor, 架构, 设计 | Claude | 100 |
| Bug Fix & Debugging | bug, fix, debug, error, 修复, 调试 | Claude | 90 |
| Code Generation | generate, create, implement, 生成, 创建 | Claude | 80 |
| Complex Analysis | analyze, review, explain (with >300 token prompt) | Claude | 70 |
| Simple Questions | explain, what is, how to, 解释, 什么是 | DeepSeek | 50 |
| Documentation | document, readme, doc, 文档 | DeepSeek | 40 |

Unmatched requests default to DeepSeek (most cost-efficient).

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.22+ |
| HTTP Server | `net/http` (standard library) |
| Database | SQLite (`modernc.org/sqlite`, pure Go, no CGO) |
| Frontend | React + TypeScript + Ant Design + Recharts |
| Desktop | Electron + Vite |
| State | Zustand |

## License

MIT
