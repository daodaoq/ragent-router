# RAgent Router

**Go 实现的 AI API 智能网关与容错引擎** — 在 Claude Code 与多个大模型之间构建透明代理，提供智能路由、语义缓存、多模型编排和韧性保护。

```
              POST /v1/messages (SSE Stream)
┌─────────┐        ┌──────────────────────────────────┐       ┌──────────┐
│ Claude  │───────▶│         RAgent Router (Go)        │──────▶│ DeepSeek │
│  Code   │        │                                  │       │  Claude  │
│         │◀───────│  Cache → Pipeline → Route        │◀──────│  MiniMax │
│ Dashboard│       │  CircuitBreaker → Retry → Timeout│       │  BaiLian │
└─────────┘        └──────────────────────────────────┘       └──────────┘
```

## ⚡ 一键 Demo（无需 API Key）

```bash
cd backend && MOCK_MODE=true go run ./cmd/server
```

打开 `http://localhost:15722/health` 确认服务启动，所有功能开箱即用：
- Mock AI 上游服务器自动启动（返回 SSE 流式响应）
- Dashboard 预填充 15 条模拟历史数据
- 语义缓存、多模型编排、中间件管线全部可用

## 核心功能

| 功能 | 说明 | Demo 可见性 |
|------|------|------------|
| **韧性五件套** | 限流→熔断→重试→舱壁→超时 | `/api/resilience/stats` |
| **三阶段路由** | 关键词(0ms) → Embedding(~300ms) → LLM分类(~500ms) → 兜底 | 日志 + 统计页 |
| **语义缓存** | Embedding 相似度匹配，不调 API 直接返回 | `cache_hit` SSE 事件 |
| **多模型编排** | DeepSeek 生成 → Claude 审查 | `phase_change` SSE 事件 |
| **协议适配** | Anthropic ↔ OpenAI/DeepSeek 自动翻译 | 透明切换 |
| **Token 实时计量** | SSE 流中解析 usage（兼容 Anthropic+OpenAI） | Dashboard 费用卡片 |
| **中间件管线** | Prompt 分析 → 注入元数据 | 请求日志有分析标签 |
| **Mock 模式** | 零配置一键启动全部功能 | `MOCK_MODE=true` |

## 架构

```
internal/
├── api/              ← HTTP handler + middleware（router/respond/middleware）
├── orchestrator/     ← 多模型编排引擎（review 模式）
├── mock/             ← Mock 上游服务器 + embedding + 种子数据
├── provider/         ← 共享类型（Config/CostRate/ResilienceConfig）
├── proxy/            ← 代理核心 + 管线 + 编排桥接
│   └── middleware/    ← PromptAnalyzer 等内置中间件
├── resilience/       ← 韧性五件套（自研，零依赖）
│   ├── circuitbreaker/  ← 三态熔断器（Closed→Open→HalfOpen）
│   ├── ratelimit/       ← 令牌桶（28ns/op, 2048 分片）
│   ├── retry/           ← 指数退避 + 3 种 Jitter
│   ├── bulkhead/        ← 信号量舱壁
│   └── timeout/         ← Context 级联超时
├── routing/          ← 三阶段混合路由引擎
├── semcache/         ← 语义缓存服务
└── store/            ← SQLite 持久化 + 分析查询
```

## 技术亮点（面试可聊）

### 熔断器 — 三态状态机 + 滑动窗口

```
Closed ──(失败率>50%)──→ Open ──(30s后)──→ HalfOpen
                                              │
                           ┌─(探测成功)────────┘
                           └─(探测失败)──→ Open
```

每个供应商独立熔断，`/api/resilience/stats` 可实时查看状态。

### 令牌桶 — 28ns/op

```
BenchmarkTokenBucket_Allow-32    42440167    28.32 ns/op
```

FNV-64a 哈希 + 2048 分片 + Double-Checked Locking + Lazy Refill。

### 语义缓存 — 不调 API 直接返回

```
请求 → Embedding → 与缓存库计算余弦相似度
  ├─ > 0.92 → SSE cache_hit 事件 → 直接返回（0 API 调用）
  └─ < 0.92 → 正常路由 → TeeReader 捕获响应 → 写缓存
```

### 多模型编排 — Review 模式

```
POST /v1/messages
Header: X-Ragent-Orchestrate: review

SSE 流:
event: phase_change  data: {"phase":"generation","provider":"DeepSeek"}
[生成阶段 SSE...]
event: phase_change  data: {"phase":"review","provider":"Claude"}  
[审查阶段 SSE...]
event: orchestration_summary  data: {"total_cost":0.052,...}
```

### 协议适配

`ProviderAdapter` 接口 + `AdapterFactory`，Anthropic → OpenAI 格式自动翻译（system prompt、认证头、content 块）。

### 测试

```bash
go test -v ./internal/proxy/...   # 10 测试 + 2 benchmark
go test -race ./...                # 全量竞态检测
```

## Quick Start

### Mock 模式（推荐，无需 API Key）

```bash
cd backend
MOCK_MODE=true go run ./cmd/server
# Dashboard: http://localhost:15722/health
```

### 生产模式

```bash
cd backend
DEEPSEEK_API_KEY=sk-xxx CLAUDE_API_KEY=sk-ant-xxx go run ./cmd/server
```

### 前端

```bash
cd frontend
npm install && npm run dev
# → http://localhost:5173
```

### Docker Compose

```bash
docker-compose up
# → 后端 :15722 + 前端 :5173
```

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/messages` | SSE 流式代理（Anthropic 兼容） |
| GET | `/api/dashboard/overview` | 费用概览 |
| GET | `/api/monitor/overview` | 聚合监控 |
| GET | `/api/analytics/model-performance` | 模型效果分析 |
| GET | `/api/cache/stats` | 语义缓存命中统计 |
| POST | `/api/cache/clear` | 清空缓存 |
| GET | `/api/resilience/stats` | 熔断器状态 |
| GET | `/api/intent/tree` | 意图树 |
| GET | `/health` `/healthz` `/readyz` | 健康检查 |

## 技术栈

| 层级 | 选型 |
|------|------|
| 语言 | Go 1.22+（纯标准库 + 1 个 SQLite 驱动） |
| 前端 | React 18 + TypeScript + Ant Design + Recharts |
| 数据库 | SQLite（modernc.org/sqlite, 零 CGO） |
| 桌面 | Electron + Vite |
