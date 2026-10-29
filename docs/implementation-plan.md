# G-Desk 架构与协议

## 架构

单个 Go 程序通过系统 SSH 采集服务器状态。Python 探针随程序分发，经 stdin 在远端运行，只读数据。

```text
SSH 配置 → 批量采集 → 进程与任务关联 → Bubble Tea 界面
               ├─ nvidia-smi：GPU、显存、利用率、进程
               ├─ /proc：用户、命令、运行时长、进程身份
               └─ task.json / status.json：任务与进度
```

| 目录 | 职责 |
|---|---|
| `cmd/gdesk`、`internal/app` | 启动、配置、迁移 |
| `internal/collect` | SSH 与远端探针 |
| `internal/core` | 协议、关联、空闲判断 |
| `internal/ui` | 交互与渲染 |
| `integrations` | 可选的状态写入器 |

## 通用元信息协议 v1

每个任务目录只需两份 UTF-8 JSON：`task.json` 是任务定义，`status.json` 是最新一次执行。两者必须有相同 `task_id` 和 `schema_version: 1`。

### 任务定义

| 字段 | 类型 / 要求 | 语义 |
|---|---|---|
| `schema_version` | integer，必填，值 1 | 不认识的版本显式报错 |
| `task_id` | 非空 string，必填 | 同一主机内唯一、续跑不变；推荐 UUID |
| `name` | 非空 string，必填 | 简短可读名称 |
| `project` | 非空 string，必填 | 所属项目 |
| `description` | 非空 string，必填 | 目的/工作内容，由人或脚本明确提供 |
| `kind` | string，可选 | 建议 train/eval/inference/data/benchmark/other |
| `tags` | string[]，可选 | 搜索标签 |
| `parameters` | object，可选 | 启动时实际采用的参数，领域无关 |
| `artifacts` | object[]，可选 | 每项 `role`、远端绝对 `path` |
| `provenance` | object，可选 | git commit、配置快照、哈希等 |
| `display.summary_fields` | string[]，可选 | 可选的摘要字段提示；第一版用于搜索摘要，总览不展开参数 |

参数值可以是 JSON 标量、数组或对象；带单位的值使用 `{"value": 120, "unit": "seconds"}`。摘要只解释 `parameters.<顶层键>`，不执行表达式或代码。没有展示提示时，按参数键排序取前四项。复杂嵌套参数主要放详情。

任务定义在一次实验中保持不变。旧任务的配置应来自启动时快照；当前仓库后来改过的配置不能作为历史运行的有效配置。重启或续跑生成新 attempt，实质改变实验参数则生成新 task。

### 最新执行状态

| 字段 | 类型 / 要求 | 语义 |
|---|---|---|
| `schema_version`、`task_id` | 必填 | 对应任务定义 |
| `attempt_id` | 非空 string，必填 | 本次启动标识；重启时更换 |
| `state` | enum，必填 | queued/running/paused/succeeded/failed/cancelled/unknown |
| `updated_at` | RFC3339，必填 | 此执行记录最近更新，必须有时区 |
| `started_at`、`finished_at` | RFC3339，可选 | 本次执行起止时间 |
| `stage`、`message` | string，可选 | 当前阶段和一句当前活动 |
| `processes` | identity[]，可空 | 当前 GPU workers 的可靠身份 |
| `progress` | object，可选 | completed、可选 total、unit、scope、updated_at |
| `metrics` | object，可选 | 例如 loss、accuracy、带单位的 throughput |
| `artifacts` | object[]，可选 | 本次 attempt 日志/输出等，日志优先于定义层 |
| `exit_code` | integer，可选 | 实际执行结果，未获得时不得编造 |

进程身份示例：`{"pid": 12345, "boot_id": "Linux-boot-uuid", "start_ticks": "2788784010"}`。由运行端自动读取，不要求用户手填。ticks 用字符串，避免跨语言大整数精度问题。主机由本地可信配置补充，远端元信息不能冒充其他主机。

进度例子：`{"completed":32,"total":100,"unit":"epochs","scope":"stage","updated_at":"2026-10-29T07:10:00Z"}`。`scope` 只允许 `task`、`stage`。总数未知时省略 total；completed 必须非负，总数存在时不得小于 completed。切换 stage 清除旧的阶段进度，不拼接不同阶段的百分比。心跳不能更新时间意义上的“最近进展”。

写入使用同目录临时文件 + 原子替换。每个任务只允许一个状态写入者；分布式 worker 将进度汇总给控制器。读取时隔离坏文件，不用上一份成功结果掩盖错误。第一版不保存 attempt 历史，也不做并行 attempt 聚合；需要同时运行的实验副本使用不同 task ID/目录。

## 关联与状态

- GPU 用 UUID 关联，界面显示卡号。占用关系来自实时查询。
- 进程身份由主机、boot ID、PID、start ticks 组成，防止 PID 复用误关联。
- 多个任务声明同一进程时报告冲突，不自动归属。未登记进程仍显示。
- 名称和说明可在本地修改；占卡、进度和执行结果以采集与上报为准。
- 断线保留上次观测并标记过期。低利用率不表示失败，进程消失不表示成功。
- 任务元信息默认 60 秒过期；终态记录无需继续心跳。时间偏差会显示提示。

## 界面

「全部」视图每卡一行；单机视图分两行。固定用户列，进度或时长靠右。长名称省略，Enter 查看详情；Tab 切换任务列表。排序和刷新保留选中项。

状态用小符号表示，完整原因在详情。日志按需读取。远端文本经过控制字符清理，不执行其中的命令。

## 采集限制

默认每 5 秒刷新，超时 12 秒，并发上限 4。每台主机同时最多一个请求；失败退避至最多 60 秒。

每台最多 128 个任务目录。单 JSON ≤1 MiB，SSH 输出 ≤4 MiB，日志尾部 ≤64 KiB / 160 行。只读普通文件。路径通过 JSON/base64 传输，不拼接到 shell。

不扫描实验目录，不保存历史指标，不控制远端任务。空闲候选卡要求观测不足 20 秒、查询成功、无计算进程、显存 ≤512 MiB、利用率 ≤5%。

## 验证

测试覆盖进程身份、共享 GPU、断线与未知状态、坏文件、日志范围、标注、搜索、排序、窄屏和中文布局。

依赖：[Bubble Tea](https://github.com/charmbracelet/bubbletea)、[Lip Gloss](https://github.com/charmbracelet/lipgloss)、[nvidia-smi](https://docs.nvidia.com/deploy/nvidia-smi/)。
