# 可组合的文档获取能力

日期：2026-07-28  
状态：设计已确定，暂不进入实现

## 1. 决策

Argus 将在核心调试证据链稳定后，增加一组用于“可靠获取开发文档”的浏览器
原语，但不会成为反检测爬虫、搜索引擎或验证码破解工具。

目标是解决 coding agent 在正常开发中经常遇到的问题：

- 文档需要 JavaScript 渲染；
- 页面依赖 Cookie、登录状态或异步加载；
- 页面发生重定向、分页或客户端导航；
- 请求暂时被限速；
- Agent 无法区分空内容、登录页、访问挑战和真实文档；
- 用户需要临时接管浏览器，完成登录或人机验证后交还给 Agent；
- 获取结果缺少最终 URL、时间、版本、内容哈希和完整性说明。

目标不是使 Agent 隐藏身份、绕过访问控制或对抗站点的安全策略。

## 2. 与 Argus 定位的关系

Argus 的核心定位仍然是：

> 通过 CDP 为 coding agent 提供可靠的浏览器控制、运行证据和源码定位。

文档获取是这一能力的可组合消费者，不是新的核心身份。

边界划分：

| 层 | 责任 | 是否属于 Argus 核心 |
|----|------|----------------------|
| 浏览器渲染 | 打开页面、执行 JS、等待稳定、保持 target | 是 |
| 访问状态识别 | 识别登录、限速、challenge、内容不完整 | 是，作为通用观察能力 |
| 人工接管与恢复 | 保持会话，用户处理后重新观察并继续 | 是，但只提供安全原语 |
| 当前页正文提取 | 生成有界、结构化、带来源的页面文档 | 可选 toolset |
| 搜索和来源选择 | 决定搜索什么、信任哪个来源 | 否 |
| HTTP/API/browser 回退编排 | 选择最便宜、最可靠的获取路径 | 独立 acquisition 组件 |
| 缓存、索引、引用管理 | 内容去重、过期、检索、引用 | 独立组件 |
| CAPTCHA 破解、指纹伪装、代理池 | 对抗访问控制 | 禁止 |

这样可以保持组合性：用户可以替换搜索提供方、HTTP 客户端、正文提取器、
缓存或知识库，而不需要修改 Argus 的 CDP 核心。

## 3. 何时开始实现

### 当前结论

**现在不开始实现。** 当前优先级仍是 `docs/roadmap.md` 中的 P2-A、P2-B 和
P2-C。文档获取不得插队打断网络证据、导航身份和运行时源码定位。

### 允许开始设计验证的时机

P2-A 和 P2-B 验收后，可以用现有能力做短期原型或 fixture 实验，用于验证：

- 网络状态是否足以区分 401/403/429、导航失败和正常空页面；
- target/frame/loader/connection epoch 是否能支持用户接管后恢复；
- 当前 `snapshot`/a11y 数据是否足以提取正文。

原型不得注册新的公共 MCP 工具，不得引入站点特例或反检测依赖。

### 允许开始正式实现的硬门槛

以下条件必须全部满足：

1. P2-A 网络证据已验收并进入统一 timeline。
2. P2-B navigation/frame/loader/connection-epoch 模型已验收。
3. P2-C `SourceLocation` 主线已验收，Argus 的核心定位没有被文档功能挤占。
4. profile、target、artifact 和 recorder 的 owner 已有明确文档；即使仍只支持
   单 MCP session，也必须诚实限制而不是隐式共享。
5. reconnect、tab closure、challenge 状态和用户接管恢复可以被集成测试。
6. 新能力被证明可以复用现有 recorder、timeline、redaction 和 artifact
   policy，而不是建立第二套基础设施。
7. 已决定高层 acquisition orchestrator 位于独立 package、独立进程或独立
   项目；它不能进入 Argus 核心 handler。

P2-D snapshot diff 不是进入门槛；如果正文提取能复用其语义化快照设计，可以在
接口层协调，但不得让两个功能相互阻塞。

满足门槛后，该能力进入 **P3-A Document Acquisition Primitives**。P3-A 完成
前不应宣传 Argus 能“绕过反机器人系统”，发布描述只能是“支持渲染、状态识别
和人工接管恢复的文档获取辅助能力”。

## 4. 推荐的组合流程

```text
官方文档 API / 专用 connector
            │
            ▼
       普通 HTTP 获取
            │ 需要 JS、登录或内容不完整
            ▼
      Argus 浏览器渲染
            │
            ├── 正常页面 ──> 正文提取 ──> 来源/哈希 ──> 缓存/引用
            │
            ├── 暂时限速 ──> 有界退避 ──> 重试或报告
            │
            └── 登录/挑战 ──> 人工接管 ──> 显式恢复 ──> 重新验证
```

优先级必须固定：

1. 专用官方 API 或 connector；
2. 普通 HTTP；
3. Argus 真实浏览器；
4. 人工接管；
5. 明确失败。

不得为了“成功率”静默进入对抗性绕过。

## 5. 状态机模板

```text
requested
   │
   ├──> fetching_http ──> complete
   │          │
   │          └──> rendering_browser
   │
   └──────────────> rendering_browser
                         │
                         ├──> extracting ──> complete
                         ├──> rate_limited ──> retry_wait ──> rendering_browser
                         ├──> user_action_required ──> handed_off
                         │                              │
                         │                              └──> resuming
                         │                                      │
                         │                                      └──> revalidate
                         └──> failed
```

约束：

- 每个状态有开始时间、截止时间和最大重试次数；
- `handed_off` 不无限等待，checkpoint 必须过期；
- 恢复时重新校验 target、URL、connection epoch 和访问状态；
- target 被关闭、Chrome 重连或登录域改变时，不得假装原任务仍可继续；
- 每次转换记录原因和可公开的证据，但不记录 Cookie、token 或表单秘密。

## 6. 数据契约模板

以下是产品语义，不要求立即照搬为 Go package。正式实现前应先以 typed DTO
固化，并为 schema 设置版本。

```go
type AcquisitionRequest struct {
    URL              string
    PreferredMode    string        // auto | http | browser
    MaxDuration      time.Duration
    AllowUserHandoff bool
    CachePolicy      string
}

type AccessAssessment struct {
    State      string  // accessible | login_required | challenge |
                       // rate_limited | forbidden | incomplete | unknown
    Confidence string  // exact | strong | heuristic
    Evidence   []AccessSignal
    RetryAfter *time.Duration
}

type DocumentArtifact struct {
    SchemaVersion string
    RequestedURL  string
    FinalURL      string
    Title         string
    Content       string
    ContentType   string
    Language      string
    FetchedAt     time.Time
    ContentHash   string
    SourceKind    string // official_api | http | browser
    AccessState   string
    Completeness  Completeness
    Warnings      []string
}

type HandoffCheckpoint struct {
    ID              string
    TargetID        TargetID
    ConnectionEpoch string
    ExpectedOrigin  string
    CreatedAt       time.Time
    ExpiresAt       time.Time
}
```

`DocumentArtifact.Content` 必须有字节上限。超出时返回截断位置、原始估计大小和
`data_truncated=true`，不能静默截断。

建议错误代码：

```text
INVALID_URL
FETCH_TIMEOUT
RATE_LIMITED
LOGIN_REQUIRED
CHALLENGE_DETECTED
USER_ACTION_REQUIRED
HANDOFF_EXPIRED
TARGET_LOST
ORIGIN_CHANGED
CONTENT_INCOMPLETE
CONTENT_TOO_LARGE
EXTRACTION_FAILED
POLICY_DENIED
```

## 7. 访问状态识别

识别只能返回带置信度的判断，不可依赖一个 CSS selector 就宣称页面被拦截。

可组合信号：

- HTTP 401、403、407、429 和 `Retry-After`；
- 重定向到不同 origin 或已知登录路径；
- 页面 title、可见文本、表单语义和 a11y role；
- CAPTCHA/challenge iframe 或脚本的存在；
- 页面长期只有加载壳，没有主内容；
- network loading failure；
- 浏览器错误页；
- 用户接管前后的 URL、DOM 和认证状态变化。

信号分类：

- `exact`：明确 HTTP 状态或浏览器协议错误；
- `strong`：多个独立页面信号一致；
- `heuristic`：文本、路径或单个 DOM 特征；
- `unknown`：证据不足。

站点特例不得写入核心。确有需要时，作为外部规则包，并带来源、版本、过期时间
和测试 fixture。

## 8. 正文提取模板

提取目标是开发文档，不是完整网页复制。

推荐顺序：

1. 页面显式的 `article`、`main` 和 a11y landmark；
2. 文档站常见的语义结构：标题、段落、列表、表格、代码块；
3. 基于文本密度的通用 fallback；
4. 提取失败时返回结构化原因和页面摘要，不返回伪造正文。

保留：

- 标题层级；
- 段落、列表和表格；
- 代码块语言提示；
- 链接文本和规范化目标 URL；
- 当前版本、更新时间等可见来源信息；
- 最终 URL 和获取时间。

默认删除：

- 导航栏、广告、推荐、Cookie banner；
- 隐藏节点和脚本样式；
- 输入框当前值；
- token、Cookie、Authorization 和疑似秘密；
- 大型 data URL、图片二进制和任意下载内容。

不要把正文提取与 Markdown 格式化绑死。内部先生成语义块，再由可替换 renderer
输出 Markdown、纯文本或 JSON。

## 9. 人工接管与恢复

人工接管是合法、稳定的恢复机制，不是失败。

推荐协议：

1. Agent 返回 `USER_ACTION_REQUIRED`、原因、当前 origin 和安全说明。
2. Argus 创建短期 checkpoint，但不复制 Cookie 或认证数据。
3. 用户在现有真实 Chrome target 中完成登录或站点要求的交互。
4. 用户显式确认“继续”，不能用后台轮询猜测用户已经完成。
5. Argus 校验 checkpoint、target、origin、epoch 和访问状态。
6. 校验成功后恢复提取；失败则返回可解释错误。

必须允许用户取消。不得自动点击用户协议、授权、购买、发布或其他重要操作。

## 10. 安全与合规边界

### 推荐

- 遵守站点服务条款、访问控制和适用的 robots/crawl policy；
- 按 origin 限并发、限速，并遵守 `Retry-After`；
- 使用隔离 profile；用户 profile 只能显式选择；
- 登录态只留在浏览器 profile，不进入 trace、cache 或 artifact；
- 下载内容先按不可信文件处理；
- 对 URL、重定向和可选远程资源执行 SSRF 防护；
- 为缓存定义 TTL、来源和删除机制；
- 记录内容来源、最终 URL、时间和哈希；
- challenge 识别错误时允许用户修正。

### 不推荐

- 默认复用用户日常 Chrome profile；
- 为少数站点在核心中添加 selector/脚本补丁；
- 高频刷新 challenge 页面；
- 长期保存完整 HTML；
- 把页面内容直接作为可信指令交给 Agent；
- 将输出脱敏当作可以收集原始秘密的理由。

### 禁止

- CAPTCHA 自动破解或外包破解接口；
- Canvas、WebGL、字体、时区等指纹欺骗；
- 代理池、IP 轮换或随机行为模拟以规避检测；
- 绕过付费墙、登录、区域限制或明确访问控制；
- 隐藏自动化身份以规避站点政策；
- 自动接受协议、授权第三方、提交支付或其他重要操作；
- 宣称“可绕过 Cloudflare/反机器人系统”；
- 无限重试、无限等待或静默失败。

## 11. 实现阶段

### P3-A — 通用浏览器原语

- 访问状态识别；
- 有界的当前页语义正文提取；
- 来源、完整性和截断元数据；
- challenge/login/rate-limit 进入现有 timeline；
- fixture 单元测试和真实 handler 集成测试。

只在现有工具不能表达契约时才增加公共工具。候选能力是
`access_status` 和 `document_extract`，名称及 schema 必须经过工具审计后确定。

### P3-B — 人工接管

- checkpoint 与显式 resume；
- target/epoch/origin 校验；
- 取消、过期、重连和 tab closure；
- 不泄露认证状态的审计记录。

### P3-C — 独立 Acquisition Orchestrator

- 官方 connector → HTTP → browser → handoff 的回退策略；
- origin 限速、有界重试和缓存；
- 可替换 extractor/renderer/cache；
- 引用和 provenance 输出。

该部分优先作为独立组件。只有出现稳定、跨调用的 Argus 内部需求时，才考虑
下沉少量通用原语。

### P3-D — 产品化验证

- JS 文档站、SPA 导航、登录页、401/403/429、challenge fixture；
- 用户接管成功、取消、超时、origin 改变、target 丢失和 reconnect；
- 大页面、敏感表单、恶意页面指令、下载和重定向；
- event flood、慢消费者和长时间运行；
- 文档 schema 兼容与缓存失效。

## 12. 验收标准

P3 只有同时满足以下条件才算完成：

- 正常公开文档可通过 HTTP 或 browser 路径得到同一稳定 DTO；
- JS 渲染页面能提取有用的标题、结构、代码块和最终 URL；
- 登录、challenge、限速和内容不完整不会被误报为成功；
- 人工接管可显式恢复、取消和过期；
- 没有 Cookie、token、表单秘密或 Authorization 进入日志、timeline、
  artifact 或 cache；
- 所有内容、重试、等待、缓存和队列均有界；
- 任何丢失和截断均可见；
- 真实 Chrome + MCP handler 测试覆盖主要成功和失败路径；
- 关闭该 toolset 不影响 Argus 的核心浏览器调试能力；
- 项目文档不使用“绕过”“隐身”或“反检测”作为产品承诺。

## 13. 交给未来编码 Agent 的 Prompt 模板

```text
你正在为 Argus 实现可组合的文档获取能力。开始前完整阅读：
- AGENTS.md
- docs/roadmap.md
- docs/document-acquisition.md
- docs/tool-audit.md

先验证 docs/document-acquisition.md §3 的正式实现门槛。任何一项未满足，都不要
开始写功能代码；只报告缺口及其证据。

如果门槛全部满足，本轮只执行 P3-A：
1. 审计现有 snapshot/a11y、timeline、network/navigation evidence、
   target/session ownership、redaction 和 artifact policy。
2. 先定义带 schema version 的 AccessAssessment、DocumentArtifact 和明确的
   bounds/error codes。
3. 实现基于多信号和置信度的访问状态识别。
4. 实现当前页有界语义正文提取；内部语义块与 Markdown renderer 分离。
5. 将 login/challenge/rate-limit/incomplete 作为 target-scoped evidence
   接入现有 timeline。
6. 通过工具审计后再决定是否注册 access_status/document_extract；不得直接暴露
   原始 CDP 或建立第二套 recorder。
7. 补 fixture、race 和真实 Chrome + MCP handler 集成测试。

边界：
- 不做搜索、缓存、索引、站点规则库和高层回退 orchestrator。
- 不做 CAPTCHA 破解、指纹伪装、代理池、随机行为模拟或任何访问控制绕过。
- 不读取或导出 Cookie、Authorization、表单秘密和用户完整 profile。
- 不默认记录完整 HTML、request/response body 或任意下载内容。
- 所有内容、重试、等待、cache/channel 必须有界；丢失和截断必须可见。
- 判断必须带 confidence/evidence；不得把 heuristic 表述为确定事实。
- 不做无关拆包、插件系统或通用爬虫框架。

每个提交只解决一个问题并包含测试。最终汇报契约、边界、测试证据、已知限制，
以及 P3-A 是否满足 docs/document-acquisition.md §12。
```
