# CDP UI 交互与前端限制分析报告

> 更新日期：2026-09-25（基于当前最新代码库核对与清理）

---

## 一、已验证修复的问题（历史限制已闭环）

以下问题在早期测试中曾被记录，但已在近期代码重构（提交 `845ceb4`、`017edda` 等）中修复：

### 1. `navigate` 在 Attach 模式下失效（已修复）
- **原问题**：在 attach 模式下调用 `navigate` 报 protocol 错误或无法触发加载。
- **现状**：已修复为使用标准 CDP `Page.navigate`，并增加了对 `about:blank` 及非 URL 前缀的规范化补全，支持多轮平滑跳转。

### 2. `eval` 变量重复声明报错与超时不可控（已修复）
- **原问题**：Agent 在连续调用 `eval` 时，若使用 `const` 或 `let` 重复声明变量会导致 JS 语法错误；遇到死循环或长耗时脚本无超时保护。
- **现状**：底层已引入上下文隔离包装器与动态 IIFE 重试机制，并原生支持 `timeout_ms` 选项与 CDP 调用超时控制。

### 3. `click` / `scroll` 基础原生事件管线（已修复）
- **原问题**：早期版本曾使用 `element.click()` 或 `dispatchEvent` 派发合成事件，导致部分 React 框架无法响应。
- **现状**：`click` 统一使用 `resolveClickPoint` 定位后走 CDP 原生 `Input.dispatchMouseEvent`（包含 mouseMoved、mousePressed、mouseReleased）；`scroll` 统一走原生 `mouseWheel`。

---

## 二、当前仍客观存在的真正前端与架构边界限制

以下问题是目前 Argus 在应对现代复杂前端框架时**真实存在的瓶颈**：

### 1. Radix UI / Floating-UI / Headless UI 的 Portal 元素定位
- **问题表现**：
  下拉菜单（Dropdown）、Select 下拉浮层、Popover、Dialog、Tooltip 往往被挂载在 `document.body` 顶层的独立 Portal 容器（如 `<div data-radix-portal>`）中，而不是直接挂在触发按钮内部。
- **根因**：
  - 点击按钮后，Portal 异步挂载到 DOM 并带有入场动画（CSS Transition / Opacity / Transform）；
  - 若 Agent 紧接着执行获取，因缺乏针对特定 Portal 出现的主动等待（Auto-wait for dynamic overlay），可能在 DOM 尚未渲染完成时判定不存在；
  - 部分全屏 Backdrop/Overlay 带有 `pointer-events: auto`，若坐标计算未避开遮罩层，原生鼠标点击可能被遮罩拦截。

### 2. Iframe 与跨域嵌套上下文盲区
- **问题表现**：
  嵌在 `<iframe>` 内的元素（例如第三方支付表单、OAuth 授权框、嵌入式报表）无法被常规选择器选中。
- **根因**：
  Argus 当前的大部分交互与选择器逻辑（`tools_interaction.go`）直接运行在主 Frame 的 `document` 上。CDP 需先通过 `Target.getTargets` 或 `Page.getFrameTree` 获取子 Frame 的 Execution Context，再使用该上下文的 `ExecutionContextId` 进行 DOM 查询和操作。目前尚无显式的 `switch_frame` 或自动跨 Frame 穿透。

### 3. Web Components 与 Shadow DOM 穿透限制
- **问题表现**：
  现代 Web Components（自定义元素 `<my-element>` 内部的 `#shadow-root`）对 `document.querySelector` 完全隔离。
- **根因**：
  原生 CSS 选择器无法跨越 Shadow Boundary。需要使用深度穿透选择器（如 `/deep/` 替代方案或递归遍历 `node.shadowRoot`）才能探测到 Shadow Root 内部的可点击元素。

### 4. 动态 SPA 中 `:nth-of-type` 绝对选择器的脆弱性
- **问题表现**：
  在没有 `id` 或 `data-testid` 的列表或卡片元素中，`snapshot` 工具会回退到使用 `:nth-of-type` 向上拼接 CSS 路径。
- **根因**：
  在 React / Vue 等单页应用中，若列表动态重新排序、插入了广告 Banner、或用户操作导致同级元素数量发生变化，`:nth-of-type` 路径会立即失效或定位到错误的兄弟节点。

### 5. 并行工具调用（Parallel Tool Calling）下的全局 Target 竞态
- **问题表现**：
  如果 Agent 客户端在一个 Turn 内同时下发针对不同页面/标签的工具调用，可能会出现操作穿插错乱。
- **根因**：
  `target.go` 中的 `currentTargetID` 是包级全局变量，缺乏会话级/请求级的 Target 上下文隔离。
