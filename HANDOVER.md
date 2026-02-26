# LAPP Handover

## 项目概述
LAPP (Log Auto Pattern Pipeline) — 自动化日志模式识别与分析系统，用 Go 实现。

## 当前状态
- **Research 阶段已完成**，29 篇 paper review 在 `~/playground/GitHub/wiki.strrl.dev/content/GTD/references/lapp/`
- **架构设计已完成**，见 `ARCHITECTURE.md`
- **代码：零**，尚未开始实现

## 核心设计决策

### 技术栈
- **语言**：Go
- **数据面存储**：DuckDB（`github.com/duckdb/duckdb-go`，database/sql 接口）
- **控制面存储**：SQLite（template 定义、Drain 状态、配置等元数据）
- **LLM**：OpenRouter API（`anthropic/claude-sonnet-4.6`），OpenAI 兼容接口
- **Agentic 框架**：暂不引入，先自己写 thin wrapper，复杂了再考虑 `cloudwego/eino`

### Parser 策略（按优先级链式执行）
1. JSON log → 直接结构化
2. Grok patterns → 预定义正则
3. Drain → 自动 template 发现（需从 scratch 实现 Go 版）
4. LLM Extractor → Drain miss 时兜底，结果回填 cache

### 建议实现顺序
1. Drain（核心算法）
2. Multiline detection（多行合并，归属 Ingestor 或 Parser 待定）
3. DuckDB Store（parsed log 写入 + label system）
4. LLM 增强层
5. Benchmark（Loghub-2.0 integration test）

## 关键参考资料

### 必读
- `lapp/00-lapp-design-notes.md` — 所有 paper 的设计启发汇总
- `lapp/01-lilac-log-parsing-llm-cache.md` — LILAC 架构（control plane/data plane split，LAPP 主要参考）
- `lapp/29-logbatcher-llm-log-parsing-ase24.md` — LogBatcher（另一个顶级 parser）
- `lapp/08-drain3-logpai.md` — Drain3 算法
- `lapp/02-ibm-label-broadcasting-log-analytics.md` — Label system 和报告类型设计

### 参考实现（已 clone 到 `~/playground/github/.claude-playground/`）
- LILAC repo
- Loghub-2.0 repo（benchmark 数据集，14 个数据集，avg 3.6M lines/set）

### Benchmark 指标
- GA (Grouping Accuracy)
- PA (Parsing Accuracy)
- FTA (F1-score of Template Accuracy)
- NED (Normalized Edit Distance)

## 待解决问题
- DuckDB label 存储方案：MAP 类型 vs 独立 label 表 vs LIST column
- 多行合并放 Ingestor 还是 Parser
- UI 形态 TBD
- Ingestor 当前只做文件上传，其他 source 后续扩展
