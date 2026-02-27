# LAPP Architecture

Log Auto Pattern Pipeline

## Modules

```
┌─────────────────────────────────────────┐
│               CLI / API                 │
└─────────┬───────────────────┬───────────┘
          │                   │
   ┌──────▼──────┐     ┌─────▼─────┐
   │  Ingestor   │     │  Querier  │
   └──────┬──────┘     └─────▲─────┘
          │                  │
   ┌──────▼──────┐           │
   │   Parser    │           │
   │ ┌────────┐  │           │
   │ │ Drain  │  │           │
   │ └───┬────┘  │           │
   │     │miss   │           │
   │ ┌───▼────┐  │           │
   │ │LLM Ext │  │           │
   │ └────────┘  │           │
   └──────┬──────┘           │
          │                  │
   ┌──────▼──────────────────┘
   │     Store
   │  ┌────────┐ ┌────────┐
   │  │ DuckDB │ │ SQLite │
   │  │ (data) │ │ (ctrl) │
   │  └────────┘ └────────┘
   └──────────────────────────┘
```

### Ingestor
接收原始日志流，输出单条 LogEntry（timestamp + level + raw message）。

- 当前只支持**日志文件上传**，未来可扩展 Elasticsearch、GCP Logging 等上游
- **多行合并**（Java stacktrace / Rust panic / Python traceback）：归属待定，可能在 Ingestor 也可能在 Parser，取决于实现时哪层更自然

### Parser
多策略 pipeline，按 log 格式分流：

- **JSON log** → 直接结构化提取，不需要 template 匹配
- **Grok patterns** → 预定义正则模板匹配（nginx、syslog 等已知格式）
- **Drain** → 通用非结构化日志的 template 自动发现
- **LLM Extractor** → Drain miss 时兜底，结果回填 Drain cache

各策略按优先级链式执行：JSON → Grok → Drain → LLM。输出 template_id + 提取的参数。

### Store
双存储架构：

- **DuckDB（数据面）**：所有日志存一张大表，保留原始文本。用 label system 给每行打标记（template_id、level、自定义 label）。Label 存储方案待验证 DuckDB 最佳实践（MAP 类型 vs 独立 label 表 vs LIST column）。
- **SQLite（控制面）**：template 定义、Drain tree 状态、parser 配置、Grok pattern 管理等元数据。

### Querier
查询层。按 template 聚合、时间过滤、参数搜索、label 筛选。全部走 DuckDB。

### CLI / API
用户入口，串联上面所有模块。

### UI
Web UI，具体形态 TBD。

### Testing
集成 Loghub-2.0 数据集作为 integration test，跑 GA/PA/FTA/NED 指标验证 Parser 准确性。
