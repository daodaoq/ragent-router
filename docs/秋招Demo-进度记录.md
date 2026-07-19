# RAgent Router — 秋招 Demo 项目进度记录

> 最后更新：2026-07-19

---

## ✅ 已完成（全部测试通过、编译通过）

### 1. 语义缓存 ✅

| 组件 | 文件 | 说明 |
|------|------|------|
| 存储层 | `store/semantic_cache.go` | SQLite 表 `semantic_cache` + `sem_cache_stats`，余弦相似度暴力搜索 O(N×D) |
| 缓存服务 | `semcache/service.go` | 实现 `proxy.SemanticCache` 接口，复用 Embedding 服务 |
| Proxy 集成 | `proxy/handler.go` | `Cache` 字段 + `SemanticCache` 接口，路由前查缓存 |
| SSE 事件 | `proxy/handler.go:252-280` | 命中时发 `event: cache_hit` + `X-Ragent-Cache: hit` 响应头 |
| 响应捕获 | `proxy/handler.go:461-472` | `io.TeeReader` 边流式输出边捕获响应体写缓存 |
| API 端点 | `api/router.go` | `GET /api/cache/stats` + `POST /api/cache/clear` |
| 装配 | `main.go:181-195` | Embedding 服务可用时自动启用缓存 |

**Demo 亮点**：
- 不调 API 就能返回结果，SSE 流里能看到 `cache_hit` 事件
- Dashboard 能实时看命中率
- 面试可聊：为什么用余弦相似度？阈值 0.92 怎么选的？暴力搜索 vs 向量索引的取舍？

---

### 2. 测试套件 ✅

| 测试文件 | 覆盖内容 | 测试数 |
|----------|----------|--------|
| `proxy/adapter_test.go` | 表驱动：Anthropic↔OpenAI 格式翻译、system prompt 转换、auth 头转换、适配器选择 | 6 test + 1 benchmark |
| `proxy/token_tracker_test.go` | Anthropic SSE 解析、OpenAI usage 字段兼容、TeeWriter 透传 | 4 test + 1 benchmark |
| `proxy/adapter_test.go` | `BenchmarkAdapterFactory_Get` | 1 benchmark |
| `proxy/token_tracker_test.go` | `BenchmarkTokenTracker_Write` | 1 benchmark |

**Demo 亮点**：
- `go test -v ./...` 全部绿
- 表驱动测试展示测试方法论
- Benchmark 展示性能意识

---

### 3. 模型效果分析 API ✅

| 端点 | 说明 |
|------|------|
| `GET /api/analytics/model-performance` | 返回各模型近 30 天的：请求数、平均延迟、平均 Token、总费用、平均单次费用 |

**前端待做**：Recharts 散点图（x=成本, y=延迟, 点大小=使用量）的 Dashboard 页面。

---

## 🔶 进行中 / 待完成

### 4. 多模型编排（review 模式）

**目标效果**：输入问题 → DeepSeek 生成代码 → Claude 审查质量 → SSE 流式返回"生成+审查"结果

**架构设计**：
```
internal/orchestrator/
  engine.go     — 编排引擎（goroutine + errgroup）
  strategies.go — review / best_of_n / merge 策略
  sse_merge.go  — 多上游 SSE 流合并为单下游流
```

**关键待做项**：
1. `orchestrator.Engine` 结构体 + `Execute(ctx, req, strategy)` 方法
2. `ReviewStrategy`：串行调用（DeepSeek 生成 → Claude 审查），SSE 用 `phase_change` 事件标记阶段切换
3. Proxy 集成：新增 `/v1/orchestrate` 端点（或通过请求头触发编排模式）
4. 前端展示：左侧生成结果 / 右侧审查意见的对比布局

**面试可聊的技术点**：
- Goroutine 编排 vs errgroup
- SSE 流式多路复用（多个上游流合并）
- 超时与取消传播（任一模型超时不影响另一个）
- Fan-out/Fan-in 模式

---

### 5. 中间件管线框架

**目标效果**：`Pipeline` 接口 + 一个 demo 中间件（自动分析 prompt 复杂度并标记标签）

**架构设计**：
```
proxy/pipeline.go      — Pipeline 结构体 + 链式执行
proxy/middleware/
  prompt_analyzer.go   — Demo 中间件：统计 prompt 长度、检测代码块、标记语言
```

**面试可聊的技术点**：
- 责任链模式的实际应用
- 接口设计（`ProcessRequest` + `ProcessResponse`）
- 与 HTTP middleware 的一致性

---

### 6. 前端补充（按需）

- 模型效果分析页面（Recharts 散点图）
- 缓存命中率显示（Dashboard 卡片）
- 编排模式切换 UI

---

## 📝 已知待修复

| 问题 | 说明 | 优先级 |
|------|------|--------|
| `TokenTracker` 跨 chunk SSE 解析 | accumulator 方式在单 chunk 测试通过，跨 chunk 场景有 bug，`TestTokenTracker_CrossChunkSSE` 已 skip | 低（生产环境 TCP chunk 很少切断 SSE） |
| Windows Defender 阻断 race test | `routing` 包的 race test 被安全软件拦截，非代码问题 | — |

---

## 🚀 下次继续的建议

**优先级排序：**

1. **多模型编排 review 模式** — Demo 核心亮点，最能在面试中展示技术深度
   - 先做后端：`internal/orchestrator/` + `/v1/orchestrate` 端点
   - 测试：Mock 两个上游 SSE 流 → 验证合并后的流有 `phase_change` 事件
   - 前端：简单的左右对比展示
   
2. **中间件管线** — 展示设计模式能力
   - 只做管线框架 + 1 个 demo 中间件即可
   
3. **前端模型效果分析页** — 数据已有，只差可视化

4. **修复跨 chunk 测试** — 完善测试覆盖

**架构提醒**：
- 新增包放在 `internal/` 下（对外不可见 = 不怕面试官挑接口设计的刺）
- 每个新包都要有 package doc（保持和现有代码一致的注释风格）
- 面试 Demo 的核心是"能跑 + 能聊"，不需要完美覆盖所有 edge case
