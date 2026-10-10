# Argus 定位与架构方向

> 来源：2026-08-25 对 Argus 当前架构的审计和与 Playwright 的对比分析
> 关联文档：[agent-efficiency-todo.md](agent-efficiency-todo.md)、[roadmap.md](roadmap.md)、[CDP_UI_LIMITATIONS.md](../CDP_UI_LIMITATIONS.md)

## 核心判断

Argus 当前自己搭建了完整的 CDP 协议层（cdppool.go / cdp.go），但这一层是重复造轮子——chromedp 和 playwright-go 已经封装好了，而且更成熟。Argus 的真正差异化价值在感知层和证据层，不在动作层。

建议：动作层换成 chromedp，感知层和证据层继续自己做。

---

## 一、Argus 和 Playwright 的本质区别

| 维度 | Playwright | Argus |
|------|-----------|-------|
| 定位 | 测试框架 | Agent 的眼睛和手 |
| 用户 | 开发者（写死 selector） | AI（动态决定操作什么） |
| 执行方式 | 固定脚本，确定性 | 动态决策，非确定性 |
| 感知需求 | 不需要（开发者知道操作什么） | 高（AI 要自己看页面决定） |
| 证据需求 | 不需要 | 高（为什么这个操作失败了） |
| React 兼容 | 成熟 | click 可以，fill/select/drag 不行 |
| auto-wait | 完整 | 自己写了简化版 |
| selector 引擎 | 有自己的 locator | 用 querySelector |
| iframe/Shadow DOM | 完整 | 部分支持 |
| 动作证据链 | 没有 | 有（recorder/timeline/diagnose） |

Playwright 是"手"，Argus 想做"手 + 眼睛 + 证据层"。

---

## 二、Argus 当前自己搭建了什么

### CDP 协议层（重复造轮子）

```
cdppool.go:
  → WebSocket 连接池（gorilla/websocket）
  → 每个 page-level endpoint 一个持久连接
  → 请求多路复用（by ID）
  → 连接 epoch 管理（reconnect 前后 ID 不混淆）
  → readLoop + panic recovery
  → 连接死亡时清理 + pending 请求通知
  → event hub（per-subscriber bounded pub/sub）

cdp.go:
  → cdpRequest / cdpEvaluate
  → cdpDispatchMouseClick / cdpDispatchMouseHover
  → cdpDispatchKey / cdpInsertText
  → cdpCaptureScreenshot
  → Target 管理（create/close/activate）
  → HTTP 端点（/json/version, /json/list）
```

这部分 chromedp 和 playwright-go 都有，而且更成熟。

### 动作层（有 bug）

```
| 动作 | 当前底层机制 | React / 现代前端兼容性 | 备注 |
|------|-------------|----------------------|------|
| `click` | CDP `Input.dispatchMouseEvent` | ✓ 完全支持 | 坐标计算后走原生鼠标管线 |
| `type` | CDP `Input.insertText` | ✓ 完全支持 | 原生文字输入 |
| `key` | CDP `Input.dispatchKeyEvent` | ✓ 完全支持 | 原生按键事件 |
| `fill` | CDP `Input.dispatchMouseEvent` + 清空 + `Input.insertText` | ✓ 完全支持 | 原生管线模拟点击聚焦并输入 |
| `scroll` | CDP `Input.dispatchMouseEvent` (mouseWheel) | ✓ 完全支持 | 原生滚轮事件 |
| `drag` | CDP `Input.dispatchMouseEvent` (down/move/up) | 部分支持 | 复杂 HTML5 DnD 依赖原生拖拽数据传递 |
```

click 走对了路，fill/select/drag/scroll/focus 走错了路。详见 [agent-efficiency-todo.md](agent-efficiency-todo.md) 问题一。

### 证据层（差异化价值，Playwright 没有）

```
recorder.go:
  → flight recorder（动作 → 事件 → 证据链）
  → action_id 贯穿
  → target-scoped 证据

timeline:
  → action + network + navigation 统一时间线
  → heuristic vs exact_cdp_id 归因

diagnose:
  → target-scoped 诊断
  → 失败动作证据

trace export:
  → 0600 权限，脱敏
  → 受控目录

event hub:
  → per-subscriber bounded pub/sub
  → 无事件窃取

connection epoch:
  → reconnect 前后 CDP ID 不混淆
```

这是 Argus 的灵魂，Playwright 不做这些。

---

## 三、两个使用模式

Argus 要服务两个完全不同的场景，核心能力需求不同。

### 模式一：开发调试时"感知"网页

```
场景:
  用户: "这个按钮点了没反应"
  agent 要能定位到"哪个按钮"（不是猜）
  agent 要能看到"按钮的状态"（disabled? 事件没绑? React 没渲染?）
  agent 要能找到"为什么没反应"（console 错误? 网络请求失败? 事件被拦截?）

核心能力: 精准定位 + 证据链
这是 Argus 的灵魂，Playwright 不做这个
```

### 模式二：执行任务时"操控"浏览器

```
场景:
  用户: "帮我查三家供应商报价对比"
  agent 要能打开网页、搜索、翻页、点击、填表、下载
  agent 要能操作 React/Vue/各种现代网站
  agent 要能自由导航、多 tab、前进后退
  agent 要能稳定执行，不卡在某个下拉菜单上

核心能力: 动作可靠 + 自由度
这是 Playwright/chromedp 做了多年的事
```

### 两个模式的能力需求对比

| 维度 | 模式一（感知/调试） | 模式二（操控/收集） |
|------|-------------------|-------------------|
| 场景 | 开发调试 | 任务执行 |
| 用户输入 | "这个按钮没反应" | "帮我查三家供应商报价" |
| agent 要做的 | 定位+诊断 | 导航+操作+采集 |
| 感知需求 | 高（精准定位具体元素） | 中（知道有什么可点的就行） |
| 动作需求 | 低（主要是看） | 高（要稳定操作） |
| 证据需求 | 高（为什么没反应） | 低（拿到数据就行） |
| React 兼容 | 不急（调试时可以 eval） | 急（要操作各种网站） |
| 自由度 | 低（聚焦用户指的元素） | 高（自由导航多 tab） |

---

## 四、建议的目标架构

```
Argus = 感知层（自己做） + 动作层（用 chromedp） + 证据层（自己做）

┌─────────────────────────────────────────────┐
│  MCP 工具层（暴露给 agent）                    │
│  perceive | locate | act | diagnose | trace  │
├─────────────────────────────────────────────┤
│  感知层（Argus 自己做）                        │
│  → perception: 精简可交互元素列表              │
│  → locate: "用户说的按钮" → 具体元素           │
│  → state: 元素状态 + 页面状态                  │
│  → evidence: console/network/dom 证据          │
├─────────────────────────────────────────────┤
│  证据层（Argus 自己做）                        │
│  → recorder: 动作 → 事件 → 证据链              │
│  → timeline: 统一时间线                        │
│  → diagnose: "为什么没反应"                    │
├─────────────────────────────────────────────┤
│  动作层（用 chromedp）                         │
│  → click/fill/select/drag/scroll              │
│  → auto-wait                                  │
│  → selector 引擎                              │
│  → React 合成事件                              │
│  → iframe/Shadow DOM                          │
├─────────────────────────────────────────────┤
│  CDP 协议层（chromedp 已封装）                 │
│  → WebSocket 连接池                            │
│  → 请求多路复用                                │
│  → 事件分发                                    │
└─────────────────────────────────────────────┘
```

### 分层原则

- **动作层用 chromedp**：不自己造轮子，立刻解决 React 兼容、auto-wait、selector、iframe
- **感知层自己做**：Playwright 没有"精简可交互元素列表"，这是 agent 能"看懂"页面的关键
- **证据层自己做**：Playwright 没有"动作→事件→证据链"，这是 Argus 的差异化价值
- **适配层自己写**：把 chromedp 的动作事件桥接到 recorder，action_id 贯穿

---

## 五、两个模式的工具设计

### 模式一：感知/调试工具

```
perceive
  → 返回精简可交互元素列表
  → 每个: { ref, role, label, state, box, confidence }
  → 200-500 字节
  → agent 一眼看到"页面上有什么能点的"

locate
  → 输入: 用户描述（"右上角的提交按钮"）
  → 输出: 匹配的元素 ref + 置信度 + 候选列表
  → agent 不用猜，直接拿到"用户说的是哪个"

inspect_element
  → 输入: ref
  → 输出: 元素完整状态
    → DOM 结构（精简）
    → 事件监听器（getEventListeners）
    → 计算样式（display/visibility/pointer-events）
    → React fiber 状态（如果有）
    → 是否被遮挡（z-index/hit-test）

diagnose
  → 输入: ref + 症状描述（"点击没反应"）
  → 输出: 诊断报告
    → console 有没有错误
    → 网络有没有失败请求
    → 事件有没有绑定
    → 元素是不是 disabled
    → 是不是被 overlay 遮挡
    → React 状态是否异常
    → 建议的修复方向
```

### 模式二：操控/收集工具

```
navigate(url)
  → 打开网页，等加载完成

perceive
  → 同上，但用于"看看页面上有什么"

click(ref | text | x,y)
  → 走 chromedp 的可靠点击

fill(ref, value)
  → 走 chromedp 的可靠输入

select(ref, option)
  → 走 chromedp 的可靠选择

scroll(direction | delta)
  → 可靠滚动

new_tab(url) / close_tab / switch_tab
  → 多 tab 管理

extract(ref | selector)
  → 提取元素文本/属性/表格数据

screenshot
  → 截图（给视觉模型用）
```

### 两个模式共享

- perceive 工具（同一个感知层）
- 证据层（recorder/timeline）
- CDP 连接（chromedp 管理）

---

## 六、最终形态示例

### 开发场景（模式一）

```
用户: "这个按钮点了没反应"
agent:
  → perceive → 看到页面上有 5 个按钮
  → locate("用户说的按钮") → 匹配到 ref=btn-submit
  → inspect_element(btn-submit) → 发现 disabled=true
  → diagnose(btn-submit, "点击没反应") →
      "元素是 disabled 状态，pointer-events: none，
       React fiber 显示 loading=true，
       console 无错误，
       建议: 等待 loading 结束或检查 disabled 条件"
  → 用户: "哦，是 loading 没结束"
```

### 任务场景（模式二）

```
用户: "帮我查三家供应商的报价对比"
agent:
  → navigate("供应商A网站") → perceive → 找到报价表 → extract
  → navigate("供应商B网站") → perceive → fill(搜索框, "产品型号")
                              → click(搜索) → perceive → extract
  → navigate("供应商C网站") → perceive → click(报价) → extract
  → 整理三家报价 → 交付对比表

  全程 chromedp 执行，React 网站也能操作
```

---

## 七、落地路径

| 步骤 | 内容 | 工作量 | 收益 |
|------|------|--------|------|
| 第一步 | 动作层换 chromedp，click/fill/select/drag/scroll 全部走 chromedp | 1-2 天 | React 网站从"不可用"到"可用"，模式二立刻"自由" |
| 第二步 | 新增 perceive 工具，返回精简可交互元素列表，绑定 chromedp 元素引用 | 3-5 天 | 模式一和模式二都受益，token 消耗降 10x |
| 第三步 | 新增 locate 工具，用户描述 → 匹配元素，多候选 + 置信度 | 3-5 天 | 模式一的核心能力，agent 不再猜 |
| 第四步 | 新增 inspect_element + diagnose，事件监听器 + 计算样式 + React fiber + 证据关联 | 5-7 天 | 模式一的差异化价值 |
| 第五步 | chromedp 动作事件桥接到 recorder，action_id 贯穿，timeline 统一 | 持续 | 证据层适配，保留 Argus 灵魂 |

---

## 八、需要废弃的代码

引入 chromedp 后，以下自己搭建的 CDP 协议层可以废弃：

| 文件 | 内容 | 替代方案 |
|------|------|---------|
| cdppool.go | WebSocket 连接池、请求多路复用、事件分发 | chromedp 内部管理 |
| cdp.go | cdpRequest/cdpEvaluate/cdpDispatch* | chromedp API |
| 部分 tools_interaction.go | fill/select/drag 的 eval+dispatchEvent 实现 | chromedp 动作 API |
| 部分 tools_inspect.go | 基于 cdpEvaluate 的 snapshot | chromedp DOM API |

### 需要保留的代码

| 文件 | 内容 | 原因 |
|------|------|------|
| recorder.go | flight recorder | 证据层，chromedp 没有 |
| eventhub.go | per-subscriber pub/sub | 证据层基础设施 |
| action.go | action_id 管理 | 证据层核心 |
| timeline 相关 | 统一时间线 | 证据层差异化 |
| diagnose 相关 | 诊断报告 | 证据层差异化 |
| trace export | 受控导出 | 证据层差异化 |
| autowait.go | auto-wait | 可以保留或换 chromedp 的 |

---

## 九、三种选择对比

### 选择一：全自己搭（现状）

- 优点：完全控制，证据层和动作层紧密耦合
- 缺点：CDP 协议层重复造轮子，fill/select/drag 有 bug，维护成本高
- 适合：有能力持续维护 CDP 协议层

### 选择二：底层用 chromedp，上层自己做证据（推荐）

- 优点：动作层立刻成熟，专注做差异化证据层，开发量减半
- 缺点：chromedp 事件模型和 recorder 要适配，action_id 贯穿要重新设计
- 适合：想快速解决动作层问题，专注做证据层

### 选择三：全用 Playwright，Argus 变成薄包装

- 优点：最少代码
- 缺点：放弃证据层差异化，和 playwright-mcp 重复
- 适合：只想要能用的浏览器 MCP，不追求差异化

---

## 十、结论

Argus 的真正价值不是"又一个浏览器自动化库"，而是"让 agent 能看懂页面 + 能诊断问题"的感知+证据层。

- 动作层（CDP 协议 + click/fill/select/drag）→ 用 chromedp，不自己造
- 感知层（perceive/locate/inspect）→ 自己做，Playwright 没有
- 证据层（recorder/timeline/diagnose）→ 自己做，这是灵魂

开发场景靠 perceive+locate+diagnose 精准定位问题，任务场景靠 chromedp 自由操控浏览器。两层分工，不要混在一起自己造。
