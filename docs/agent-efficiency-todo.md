# Agent 效率问题与优化跟踪清单

> 更新日期：2026-09-25（基于当前代码实现与提交记录核对）
> 关联文档：[CDP_UI_LIMITATIONS.md](../CDP_UI_LIMITATIONS.md)、[roadmap.md](roadmap.md)

## 核心进展概述

经近期的核心迭代（提交 `845ceb4`、`017edda` 等），感知层过载、动作盲目、反馈哑巴的核心痛点已有关键落地：

- [x] **动作层原生输入管线**：`click`、`type`、`key`、`scroll` 均已切入 CDP 原生输入（`Input.dispatchMouseEvent` / `Input.dispatchKeyEvent` / `Input.insertText`）；
- [x] **精简感知工具落地**：引入 `look` 工具，过滤并压缩 DOM，仅返回带稳定编号的交互节点与精简摘要，Token 消耗大幅降低；
- [x] **操作自动回显反馈**：所有交互工具执行完毕后，自动携带变更后的 Mini Snapshot 和页面状态回显，单 Turn 即可确认结果；
- [x] **eval 上下文安全隔离**：`eval` 原生支持 `timeout_ms` 并在底层通过 IIFE 和动态包装防止 `const`/`let` 重复声明报错。

---

## 优化进展与状态追踪

### [已完成] 问题一：动作层原生管线迁移
- **状态**：已解决
- **实现**：
  - `scroll`：使用 CDP `Input.dispatchMouseEvent` 派发 `mouseWheel` 滚轮事件；
  - `click`：通过 `resolveClickPoint` 获取中心坐标并由 CDP `Input.dispatchMouseEvent` 派发事件；
  - `fill`：定位坐标 -> 鼠标原生点击聚焦 -> 清空 -> `Input.insertText` 原生文本插入。

### [已完成] 问题二：精简感知与 Token 降负
- **状态**：已解决
- **实现**：
  - 新增 `look` 工具与紧凑视网膜树（Compact Snapshot），仅呈现具有可交互特性的核心节点（按钮、输入框、链接）；
  - 工具集分层（`toolmeta.go`），默认仅注册 15 个常用交互闭环工具，避免暴露全部 45 个工具造成 Schema Token 浪费。

### [已完成] 问题三：操作自回显反馈闭环
- **状态**：已解决
- **实现**：
  - 交互动作执行后自动调用 Mini Snapshot 返回后置状态，Agent 无需在每个动作后再手工调用一次感知工具。

### [已完成] 问题四：eval 变量重复声明与超时保护
- **状态**：已解决（提交 `017edda`）
- **实现**：
  - `eval` 工具自动注入作用域隔离，支持 `timeout_ms`，防止死循环脚本 hang 住 MCP 服务。

---

## 仍需推进的真实瓶颈与待办

### [待推进] 瓶颈一：a11y_snapshot 的 nodeId 与 click 的 ref 互通
- **现状**：
  - `a11y_snapshot` 返回的是 CDP 无障碍树节点的 `nodeId`（数字字符串）；
  - `click(ref)` 底层主要解析 CSS 选择器或数字序号，若直接把 a11y 的 `nodeId` 传给 `click` 会报 `SyntaxError: is not a valid selector`。
- **目标**：
  - 在 `resolveClickPoint` 中增加对 a11y `backendNodeId` / `nodeId` 的识别，通过 `DOM.resolveNode` 或 `DOM.getBoxModel` 获取坐标，实现 ref 体系的统一。

### [待推进] 瓶颈二：Portal / Floating-UI 弹窗的主动等待（Auto-Wait）
- **现状**：
  - 针对 Radix UI、shadcn/ui 的下拉菜单和弹窗，点击触发按钮后，Portal 元素通常有几十到数百毫秒的动画或异步渲染延迟。
- **目标**：
  - 提供类似 Playwright 的行动前等待机制（Auto-wait for element to be visible and stable），避免 Agent 因微小渲染延迟判定动作失败。

### [待推进] 瓶颈三：跨 Frame（Iframe）与 Shadow DOM 穿透
- **现状**：
  - DOM 树和感知工具主要针对顶层 `document`，嵌套在第三方 iframe 或 Web Components `#shadow-root` 内部的组件不可选。
- **目标**：
  - 交互与感知工具递归探测 Frame Tree 与 Shadow Root。
