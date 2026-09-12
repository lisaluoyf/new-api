# HTTP 200 假成功识别、Fallback 与诊断日志存档

> 日期：2026-09-12（北京时间）
>
> 状态：已上线生产
>
> 生产服务器：`188.245.245.213`
>
> NewAPI 上线版本：`4264afc3a871358f1955740dd502b8f07b0d2a7e`
>
> APIMaster 工作区版本：`adc157db857304a2d88387877a7b2764dfbfb3d1`
>
> 影响范围：OpenAI Responses API 的流式、非流式响应，以及 Responses 转 Chat Completions 的路径

## 1. 结论

部分上游会返回 HTTP 200，但响应体或 SSE 流实际上包含错误，或者整个流只有生命周期状态帧、没有任何文本、推理内容、工具调用等有效输出。旧逻辑可能把这种请求记成正常结束，客户端却拿不到可用回答，也不会自动换渠道。

本次修复把这类响应定义为“HTTP 200 假成功”：在任何有效输出发送给客户端之前识别并丢弃当前渠道响应，将其转成可重试的 HTTP 502 错误，进入 NewAPI 原有多渠道 fallback。每次命中都会留下结构化数据库日志并发送飞书诊断卡片。

`Selected model is at capacity` 是本次已知的一个具体表现，但实现不是只匹配这句文案。判断依据是响应结构、响应状态、是否出现终止事件、是否产生有效输出以及流如何结束。

## 2. 首个已知案例

| 字段 | 值 |
|---|---|
| request ID | `20260911022010996142888268d9d6qcQR7OyA` |
| 日期 | 2026-09-11 |
| 渠道 | `94` |
| 模型 | `gpt-5.6-terra` |
| 客户端看到的错误 | `Selected model is at capacity` |
| 日志中的流状态 | `{"end_reason":"eof","status":"ok"}` |
| prompt tokens | `0` |
| completion tokens | `0` |
| quota | `0` |

这个案例在旧日志中看起来像一个正常请求，原因是：

- HTTP 层没有留下非 2xx 错误；根据当时的流式处理行为，推断上游响应是 HTTP 200，但旧日志没有保存原始 HTTP 状态，因此这不是可回溯证实的字段。
- `EOF` 只表示上游连接或响应体读完，不表示模型成功生成了内容。
- `status=ok` 表示旧扫描器没有记录传输错误，不表示业务响应有效。
- Token 和 quota 都为 0，说明没有产生可计费输出，但单凭这三个 0 不能证明具体错误类型。

旧版本没有保存该请求的原始上游 SSE 帧，因此无法事后确认这个历史请求究竟是下面哪一种结构：

```json
{
  "type": "response.failed",
  "response": {
    "status": "failed",
    "error": {
      "type": "server_error",
      "code": "server_is_overloaded",
      "message": "Selected model is at capacity"
    }
  }
}
```

或者是顶层 `error`、`response.error`，又或者只有生命周期帧后直接 EOF。上面的 JSON 是新逻辑覆盖并通过测试的典型结构，不应被当作该历史请求的已证实原始返回。

## 3. 为什么旧逻辑会漏掉

Responses 流通常先发送生命周期帧，例如：

```text
response.created
response.in_progress
```

这些帧只说明任务被创建或正在处理，不是模型输出。旧逻辑可能在收到这些帧后就开始向客户端写 SSE；如果随后收到 `response.failed`，或者上游直接 EOF，客户端响应已经开始，NewAPI 便无法再透明切换渠道。

历史数据排查中还发现一批 `done/eof + no usage` 的普通流式日志。它们没有显式错误事件，所以仅识别 `response.failed` 或 capacity 文案仍会漏掉。由此将方案从“识别 capacity 错误”扩展为“验证 HTTP 200 响应是否真的产生了有效输出并正常终止”。

同时，`completion_tokens=0 AND quota=0` 只能作为排查入口，不能直接等同于假成功，原因包括：

- 合法的内容过滤终止可能没有普通文本输出。
- 后台异步 Responses 请求可以合法返回 `queued` 或 `in_progress`。
- 客户端断开、内部请求、计费方式差异等情况也可能产生 0 值。
- 旧日志没有保存全部原始帧，历史请求不能全部精确归因。

## 4. 新的判定原则

核心原则：HTTP 2xx 只是传输成功；只有出现真实内容或明确允许的空终止，才算业务成功。

### 4.1 计为有效输出

下列内容任一非空时，流可以提交给客户端：

- 输出文本：`response.output_text.*`
- 推理文本或推理摘要：`response.reasoning_text.*`、`response.reasoning_summary_text.*`
- 拒绝内容：`response.refusal.*`
- 音频内容：`response.audio.*`
- 函数调用参数：`response.function_call_arguments.*`
- 完整的 message、reasoning、function call、custom tool call 或其他已完成输出项
- `response.completed` 终止帧中携带的真实输出，即使此前没有 delta

`response.created`、`response.in_progress` 等生命周期帧不计为有效输出，也不能抢先锁定当前渠道。

### 4.2 允许的无普通输出终止

以下情况不会误判为假成功：

- `response.incomplete` 且原因是 `content_filter`。
- 原生 Responses 非流式后台任务，`background=true` 且状态为 `queued` 或 `in_progress`。

### 4.3 命中假成功的类型

| trigger | 判定条件 | 典型例子 |
|---|---|---|
| `response_error` | HTTP 2xx 响应内出现错误事件或嵌套 error | `error`、`response.error`、`response.failed`、`response.error != nil` |
| `response_failed_status` | 非流式响应状态是 `failed`，但没有可提取的结构化 error | `{"status":"failed"}` |
| `empty_response` | 非流式 HTTP 2xx 响应没有任何可用输出 | `{"status":"completed","output":[],"usage":null}` |
| `empty_stream` | 收到终止事件，但整个流没有可用输出 | 空的 `response.completed` |
| `missing_terminal_event` | 流以 `done` 或 `eof` 结束，但没有 Responses 终止事件，也没有可用输出 | 只有 `response.created` 后 EOF |
| `abnormal_stream_end` | 没有可用输出，且流超时、扫描失败、panic、ping 失败或以其他异常原因结束 | `timeout`、`scanner_error`、`panic` |

## 5. 处理流程

流式请求在首个有效输出之前暂存上游状态帧，并暂停下游 keepalive ping，避免提前写入客户端响应。

```text
上游返回 HTTP 2xx
        |
        v
解析响应或 SSE 帧
        |
        +-- 出现有效输出 -----------------> 提交暂存帧，继续正常响应
        |
        +-- 合法空终止/后台状态 ----------> 正常返回
        |
        +-- 错误、空响应或无输出异常结束 --> 丢弃暂存帧
                                           |
                                           v
                                      转成 HTTP 502
                                           |
                                           v
                                进入现有多渠道 retry/fallback
```

典型 capacity 事件会保留上游 `error.type`、`error.code` 和 `error.message`，但对 NewAPI 重试层表现为 HTTP 502。因此，只要客户端响应尚未开始、还有重试次数和候选渠道，也没有指定单一渠道等禁止重试条件，当前渠道不会把这条错误直接发给 Codex，而是继续 fallback。

如果所有候选渠道均失败或重试次数耗尽，最终仍会按现有逻辑向客户端返回失败。

## 6. 日志与飞书诊断

### 6.1 失败渠道日志

每次命中会写入该渠道的错误日志：

```text
logs.other.upstream_false_success
```

结构化字段包括：

| 字段 | 含义 |
|---|---|
| `trigger` | 命中哪一类假成功规则 |
| `upstream_http_status` | 上游实际 HTTP 状态，当前规则仅标记 2xx |
| `stream` | 是否流式请求 |
| `event_type` | 最后或触发错误的 Responses 事件类型 |
| `response_status` | Responses 对象状态 |
| `error_type` | 上游 `error.type` |
| `error_code` | 上游 `error.code` |
| `error_message` | 上游错误信息 |
| `stream_end_reason` | NewAPI 扫描器记录的结束原因 |
| `terminal_event` | 是否收到 `response.completed`/`response.incomplete` |
| `usable_output` | 是否已观察到有效输出 |
| `raw_response` | 脱敏并截断后的原始响应或关键帧 |
| `action` | 识别器要求的处理动作，目前固定为 `discard_channel_and_fallback` |

`raw_response` 会经过敏感信息脱敏，最多保留 4096 个 Unicode 字符。流式无输出场景只保存必要的首尾帧；不会无限保存整个 SSE 流。

`action` 记录识别器的处理目标，不等同于最终一定发生了 fallback。实际是否重试必须同时查看 `retry_decision.should_retry` 和 `retry_decision.reason`。例如已经向客户端写入有效输出时，reason 会是 `stream_already_started`，此时不能透明重放。

### 6.2 Fallback 最终成功日志

如果前面的渠道命中假成功、后续渠道成功，最终成功日志会在下面的位置留下汇总：

```text
logs.other.admin_info.upstream_false_success
```

示例：

```json
{
  "triggered": true,
  "count": 1,
  "triggers": ["response_error"],
  "error_codes": ["server_is_overloaded"]
}
```

这使得后续可以区分“用户一次成功”和“前面发生过假成功，依靠 fallback 才成功”。

### 6.3 飞书通知

每次命中发送标题为下面内容的卡片：

```text
NewAPI HTTP 200 假成功拦截
```

卡片只展示上游诊断信息和 retry decision，不展示用户邮箱、用户名、user ID、请求模型、渠道名称或渠道链路。这样可以聚焦上游实际返回结构，减少无关信息和个人数据传播。

## 7. 查询与上线后验证

生产日志库为 MySQL，可以使用 JSON 查询统计命中量。执行前应先确认实际表名、时区和日志保留周期。

统计各 trigger：

```sql
SELECT
  JSON_UNQUOTE(JSON_EXTRACT(other, '$.upstream_false_success.trigger')) AS trigger,
  COUNT(*) AS hits
FROM logs
WHERE created_at >= UNIX_TIMESTAMP('2026-09-12 00:00:00')
  AND JSON_EXTRACT(other, '$.upstream_false_success') IS NOT NULL
GROUP BY trigger
ORDER BY hits DESC;
```

统计上游错误码和错误文案：

```sql
SELECT
  JSON_UNQUOTE(JSON_EXTRACT(other, '$.upstream_false_success.error_type')) AS error_type,
  JSON_UNQUOTE(JSON_EXTRACT(other, '$.upstream_false_success.error_code')) AS error_code,
  JSON_UNQUOTE(JSON_EXTRACT(other, '$.upstream_false_success.error_message')) AS error_message,
  COUNT(*) AS hits
FROM logs
WHERE created_at >= UNIX_TIMESTAMP('2026-09-12 00:00:00')
  AND JSON_EXTRACT(other, '$.upstream_false_success') IS NOT NULL
GROUP BY error_type, error_code, error_message
ORDER BY hits DESC;
```

统计依靠 fallback 最终成功的请求：

```sql
SELECT COUNT(*) AS recovered_requests
FROM logs
WHERE created_at >= UNIX_TIMESTAMP('2026-09-12 00:00:00')
  AND JSON_UNQUOTE(JSON_EXTRACT(other, '$.admin_info.upstream_false_success.triggered')) = 'true';
```

上线后重点验证：

1. 飞书是否收到首条 `NewAPI HTTP 200 假成功拦截` 卡片。
2. `raw_response` 是否包含足够的错误结构，同时确认 key、Authorization 等敏感信息已脱敏。
3. 命中日志的 `action` 是否为 `discard_channel_and_fallback`，retry decision 是否允许重试。
4. 后续成功日志是否出现 `admin_info.upstream_false_success` 汇总。
5. 是否存在大量同一种新 trigger，排除合法响应被误判的可能。

部署完成后的第一分钟尚未出现真实命中，因此当时只能确认代码、容器版本和健康检查正确；新日志路径需要用上线后的首个真实案例完成闭环验证。

## 8. 明确边界与剩余风险

- 本次只改 OpenAI Responses 协议及 Responses 转 Chat Completions 的处理路径，不代表其他供应商协议已经自动获得相同的假成功识别。
- 流式 fallback 仅能在有效输出尚未写给客户端时透明进行。一旦已经发送真实文本、推理内容或工具调用，后续再中断不能切换渠道重放，否则会造成内容重复和状态错乱。
- 当前没有实现多渠道同时竞速，也没有修改“哪个状态帧算竞速获胜”的通用策略。本次做的是单渠道收到响应后的验证与串行 fallback。
- 不依赖 `Selected model is at capacity` 文案匹配，因此可以覆盖其他结构化错误和完全无错误字段的空流；但未知的新事件格式仍需依靠日志样本继续补充。
- 合法的无文本结果必须显式加入允许列表。目前仅确认 `content_filter` 和原生 Responses 后台任务状态。
- 旧数据缺少原始上游帧，不能用新分类器对历史记录做完全准确的回填。

## 9. 实现与测试索引

主要实现：

- `relay/channel/openai/false_success.go`：假成功诊断对象与 trigger 分类。
- `relay/channel/openai/relay_responses.go`：原生 Responses 流式和非流式识别。
- `relay/channel/openai/chat_via_responses.go`：Responses 转 Chat Completions 的识别。
- `types/error.go`：结构化诊断字段、脱敏和长度限制。
- `controller/relay.go`：错误日志、飞书通知和 retry 上下文。
- `service/upstream_false_success.go`：跨 fallback 尝试的成功日志汇总。
- `service/log_info_generate.go`：把汇总写入最终成功日志。

测试覆盖：

- `response.failed + server_is_overloaded + Selected model is at capacity`
- 顶层 `error` 事件
- 终止帧存在但没有输出
- `done`/`eof` 结束但没有终止帧和输出
- 非流式 HTTP 200 空响应
- 终止帧内才出现文本的合法响应
- `content_filter` 合法空终止
- 数据库日志字段、成功汇总和飞书字段精简

上线前验证已通过：

- 相关 Go 包定向测试
- `go test ./...`
- `go vet`
- 前端 typecheck、lint 和 build
- GitHub Actions run `34668059145`

生产部署使用工作区脚本 `/opt/scripts/go-live.sh`，通过 `roma-prod` 跳板机连接 `188.245.245.213`。部署后 `new-api-blue` 与 worker 均运行 NewAPI revision `4264afc3a871358f1955740dd502b8f07b0d2a7e`，Nginx 指向 `127.0.0.1:3002`，内部和公网 `/api/status` 健康检查均通过。
