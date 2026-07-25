# RAgent Router v0.3.0

**Go 实现的 AI API 智能网关与容错引擎** — 在客户端与多个大模型之间构建透明代理，提供智能路由、语义缓存、多模型编排、韧性保护、用户认证和计费管理。

```
              POST /v1/messages (SSE Stream)
┌─────────┐        ┌──────────────────────────────────┐       ┌──────────┐
│ Claude  │───────▶│         RAgent Router (Go)        │──────▶│ DeepSeek │
│  Code   │        │                                  │       │  Claude  │
│  Client │◀───────│  Auth → Cache → Route → Proxy    │◀──────│  MiniMax │
│         │        │  CircuitBreaker → Retry → Timeout │       │  20+...  │
└─────────┘        └──────────────────────────────────┘       └──────────┘
                         │
                    ┌────┴────┐
                    │ Dashboard│ ← 用户管理 / 渠道管理 / API Key / 费用分析
                    └─────────┘
```

## ⚡ 一键 Demo（无需 API Key）

```bash
cd backend && MOCK_MODE=true go run ./cmd/server
# 默认用户: root / 123456
# Dashboard: http://localhost:15722
```

## 核心功能

### 🔐 用户认证与权限系统

| 功能 | 说明 |
|------|------|
| **JWT 认证** | 登录/注册/会话管理，7 天有效期 |
| **RBAC 权限** | Root(100) > Admin(10) > User(1)，中间件级鉴权 |
| **API Key 管理** | 创建/删除/掩码显示/配额限制/过期时间/IP 白名单 |
| **多来源认证** | Bearer Token / x-api-key / x-goog-api-key / Query 参数 |

### 🌐 多提供商网关

| 功能 | 说明 |
|------|------|
| **20+ 提供商** | OpenAI, Claude, DeepSeek, Gemini, 通义, 文心, GLM, Kimi, MiniMax, 混元, 星火, 豆包, Mistral, Cohere, Perplexity, xAI, Ollama, OpenRouter, SiliconFlow, Bedrock, Vertex |
| **协议适配** | Anthropic ↔ OpenAI 格式自动翻译 |
| **渠道管理** | CRUD / 权重 / 优先级 / 模型映射 / 自动禁用 |
| **智能路由** | 加权随机选择 + 模型匹配 |

### 🛡️ 韧性保护五件套（自研）

| 组件 | 实现 | 亮点 |
|------|------|------|
| **令牌桶限流** | FNV-64a + 2048 分片 | 28ns/op, Double-Checked Locking |
| **信号量舱壁** | Buffered Channel | 快速失败，防止 goroutine 堆积 |
| **三态熔断器** | 滑动时间窗口 | Closed→Open→HalfOpen，每供应商独立 |
| **指数退避重试** | 3 种 Jitter 策略 | Full/Equal/Decorrelated，防惊群 |
| **级联超时** | Context 传播 | 子截止时间不超父，防止超时逃逸 |

### 🧠 智能路由引擎

```
请求 → 关键词规则(0ms) → Embedding语义匹配(~300ms) → LLM分类器(~500ms) → 兜底
         │                    │                          │
         └─ 命中直接返回       └─ 相似度>0.75 直接返回     └─ 解析意图名返回
```

三级渐进式路由，每层可独立禁用，优雅降级。

### 💾 语义缓存

```
请求 → Embedding → 余弦相似度匹配
  ├─ > 0.92 → SSE cache_hit → 直接返回（0 API 调用）
  └─ < 0.92 → 正常路由 → TeeReader 捕获 → 写缓存
```

### 🔄 多模型编排

```bash
POST /v1/messages
Header: X-Ragent-Orchestrate: review

# DeepSeek 生成 → Claude 审查 → 合并结果
```

### 💰 计费与配额

| 功能 | 说明 |
|------|------|
| **Token 配额** | 用户级 + API Key 级双重配额控制 |
| **预扣结算** | 请求前预扣，完成后结算差额 |
| **费用追踪** | 实时解析 SSE 中的 token 用量，估算成本 |
| **成本分析** | 今日/本月/总计费用，节省金额估算 |

### 📊 管理后台 API

| 端点 | 说明 |
|------|------|
| `POST /api/auth/register` | 用户注册 |
| `POST /api/auth/login` | 用户登录（返回 JWT） |
| `GET /api/user/self` | 当前用户信息 |
| `GET/POST/PUT/DELETE /api/tokens` | API Key CRUD |
| `GET/POST/PUT/DELETE /api/channels` | 渠道管理 |
| `GET /api/users` | 用户列表（管理员） |
| `GET /api/dashboard/*` | 仪表盘数据 |
| `GET /api/monitor/*` | 实时监控 |
| `GET /api/channel-types` | 支持的渠道类型 |

## 架构

```
backend/
├── cmd/server/           ← 入口（Gin + GORM + Redis + JWT）
├── common/               ← 工具库（Redis, Crypto, JSON, Env）
├── constant/             ← 常量（渠道类型, 角色, 状态）
├── model/                ← GORM 模型（User, Token, Channel, Log）
├── middleware/            ← Gin 中间件（Auth, CORS, Recovery, Distributor）
├── controller/           ← HTTP 控制器（User, Token, Channel, Dashboard）
├── service/              ← 业务逻辑（配额管理）
├── router/               ← 路由注册
├── internal/             ← 核心引擎（保留自研实现）
│   ├── resilience/       ← 韧性五件套（自研，零依赖）
│   │   ├── circuitbreaker/  ← 三态熔断器
│   │   ├── ratelimit/       ← 令牌桶（28ns/op）
│   │   ├── retry/           ← 指数退避 + Jitter
│   │   ├── bulkhead/        ← 信号量舱壁
│   │   └── timeout/         ← Context 级联超时
│   ├── routing/          ← 三阶段混合路由引擎
│   ├── semcache/         ← 语义缓存
│   ├── orchestrator/     ← 多模型编排
│   ├── proxy/            ← 代理核心
│   ├── mock/             ← Mock 模式
│   └── store/            ← 旧存储层（兼容）
└── frontend/             ← React 18 + Ant Design + Electron
```

## 技术亮点（面试可聊）

### 1. 韧性保护五件套 — 零外部依赖自研

```go
// 执行顺序精心设计
ServeHTTP → 全局限流 → 舱壁 → 熔断 → 重试 → 超时 → HTTP 转发
```

- 令牌桶：FNV-64a 哈希 + 2048 分片，热路径 28ns/op
- 熔断器：滑动时间窗口（环形缓冲区），避免固定窗口边界效应
- 重试：三种 Jitter 策略防止惊群效应
- 超时：Context 级联传播，子截止时间自动不超父

### 2. 三级渐进式路由 — 三种匹配策略

```go
type HybridRouter struct {
    keywordRules   RuleEngine          // Stage 1: 正则匹配, 0ms
    embeddingSvc   EmbeddingService    // Stage 2: 语义相似度, ~300ms
    classifier     IntentClassifier    // Stage 3: LLM 分类, ~500ms
}
```

每层可独立禁用，优雅降级。Embedding 缓存避免重复调用。

### 3. 双阶段上游请求设计

```go
// 阶段 1（可重试）：建立连接 + 检查状态码
// 阶段 2（不可重试）：SSE 流式传输（WriteHeader 后不能重试）
var ErrStreamStarted = errors.New("stream already started")
```

区分"连接阶段"和"流式阶段"，避免 SSE 流中断后错误重试。

### 4. GORM + Redis + JWT — 生产级基础设施

- GORM：多数据库支持（SQLite/MySQL/PostgreSQL），自动迁移
- Redis：分布式缓存和限流（可选，降级到内存）
- JWT：无状态认证，RBAC 权限控制

### 5. 协议适配器模式

```go
type ProviderAdapter interface {
    BuildRequest(baseURL string, headers map[string]string, body map[string]interface{}) (url string, h map[string]string, b []byte, err error)
}
```

添加新提供商只需实现一个接口，代理核心永不修改。

## Quick Start

### Mock 模式（推荐）

```bash
cd backend
MOCK_MODE=true go run ./cmd/server
# 默认用户: root / 123456
# API: http://localhost:15722
```

### 生产模式

```bash
cd backend
DEEPSEEK_API_KEY=sk-xxx CLAUDE_API_KEY=sk-ant-xxx go run ./cmd/server
```

### Docker Compose

```bash
docker-compose up
```

## 技术栈

| 层级 | 选型 |
|------|------|
| 语言 | Go 1.22+ |
| Web 框架 | Gin |
| ORM | GORM v2（SQLite/MySQL/PostgreSQL） |
| 缓存 | Redis（分布式锁/Lua限流/PubSub/Streams） |
| 消息队列 | Redis Streams（生产者/消费者/死信队列） |
| 搜索引擎 | Elasticsearch（全文检索/聚合分析） |
| 认证 | JWT + bcrypt + RBAC |
| 实时通信 | WebSocket（Hub 模式/心跳/广播） |
| 链路追踪 | TraceID 透传（OpenTelemetry 规范） |
| 指标采集 | Prometheus（/metrics 端点） |
| RPC | gRPC（Protobuf/拦截器/流式RPC） |
| 前端 | React 18 + TypeScript + Ant Design + Recharts |
| 桌面 | Electron + Vite |

### Redis 深度使用

| 功能 | 实现 | 面试考点 |
|------|------|---------|
| 分布式锁 | SET NX EX + Lua 原子释放 + Watchdog 续约 | Redlock、误删防护、死锁预防 |
| 滑动窗口限流 | Sorted Set + Lua 原子操作 | 固定窗口边界突发、限流算法对比 |
| 令牌桶限流 | Hash + Lua 原子操作 | 突发流量、令牌补充策略 |
| 多维度限流 | 全局/用户/IP/模型 四维限流 | 限流维度设计、降级策略 |
| Pub/Sub | 事件总线 + 通配符订阅 + 自动重连 | 消息可靠性、与 Streams 区别 |
| Streams | 生产者/消费者组/ACK/死信队列 | 消息持久化、消费幂等、死信处理 |

### 消息队列（Redis Streams）

```
生产者 → XADD → Stream → XREADGROUP → 消费者组
                                    ↓
                              处理成功 → XACK
                              处理失败 → 死信队列
```

### Elasticsearch 集成

- 请求日志自动写入 ES（异步）
- 全文检索 prompt 内容
- 按模型/提供商/时间聚合分析
- 慢查询排查

### Prometheus 监控

```
GET /metrics

# 指标列表：
ragent_requests_total          (Counter)   按 method/status/provider/model
ragent_request_duration_seconds (Histogram) 延迟分布
ragent_active_requests         (Gauge)     活跃请求数
ragent_tokens_total            (Counter)   Token 用量
ragent_cost_usd_total          (Counter)   累计费用
ragent_cache_hits_total        (Counter)   缓存命中
ragent_circuit_breaker_state   (Gauge)     熔断器状态
```

## 测试

```bash
go test -v ./internal/proxy/...   # 代理测试 + benchmark
go test -race ./...                # 全量竞态检测
```
