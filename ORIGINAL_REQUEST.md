# Original User Request

## 2026-10-12T03:10:56Z

全面重构与升级 Argus MCP 服务器：实现并解决 GitHub 现存全部 9 个核心 Issue，对当前 40+ 工具执行激进精简与合并，剔除对 Agent 实际工作无显著效用的冗余工具，并在完全隔离的运行环境下（独立 Headless Chrome / 临时端口与临时 profile，绝对严禁干扰本地 CDP 9224 常驻 Chrome 实例及其他 Agent 会话）完成全量测试与实机端到端成效闭环验证。

Working directory: /Users/tangtszho/Work/olympus/argus
Integrity mode: development

## Requirements

### R1. 修复并实现全部 9 个核心 Issue 特性
- **Issue #1 (batch_actions alias)**: 增强 `batch_actions`，兼容支持 `key` 动作别名，避免 Agent 混淆时出现非预期执行中断。
- **Issue #2 (controlled component & transition-aware wait)**: 强化 `fill` 对现代响应式前端（React/Vue/Element UI）受控组件的触发机制，并支持输入后的过渡状态智能等待。
- **Issue #3 (unified a11y nodeId reference)**: 统一 `a11y_snapshot` 与交互工具（`click`/`fill` 等）的目标定位机制，支持直接通过 a11y nodeId 定位目标。
- **Issue #4 (act-and-verify loop)**: 实现单轮「动作-验证」闭环反馈机制，交互后自动回传因果关联状态证据或轻量视觉变化确认。
- **Issue #5 (smart framework locator)**: 增强定位器对现代前端容器的智能嗅探（如点击父级卡片/标签触发内部 input、组件外层包装容器智能识别）。
- **Issue #6 (CDP dialog anti-deadlock)**: 增加对原生 JS 对话框（alert/confirm/prompt）的自动监听与防挂死保护机制，防止 CDP 调用无限挂起。
- **Issue #7 (dev overlay auto-diagnose)**: 智能识别 Vite/Next.js/Webpack 等前端常见开发报错浮层（Overlay），快速定位根因并尽早中断无效等待循环。
- **Issue #8 (element screenshot & lightweight mode)**: 截图工具支持精确截取单个 DOM 元素，并提供轻量级直接写入磁盘返回路径的文件模式。
- **Issue #9 (mock_route tool)**: 新增高阶路由/网络拦截与 Mock 工具，支持独立于真实后端验证前端交互与异常分支 UI。

### R2. 激进审查并精简现有工具集（Tool Set Consolidation）
- 逐一审查现存全部 40+ 个 MCP 工具，以「是否对 Agent 实际浏览器操作/感知/排查有切实增益」为唯一准绳。
- 坚决删除无实战价值、过细或仅属于一次性冷门调试的低效工具。
- 将功能重叠的同质化工具收敛合并（例如查找、读取、状态检查等分散工具整合为更具表现力的统一接口），大幅压缩 MCP 暴露工具总数，降低 Agent 的选择与决策开销。
- 保持编译无警告、无废弃 schema、无死代码残留。

### R3. 双层验证闭环与环境隔离约束（CRITICAL ISOLATION REQUIREMENT）
- **隔离环境运行保证**：在执行自动化测试与真实浏览器实机验证时，**严禁连接或触碰本地常驻的 Chrome Argus 实例（CDP 127.0.0.1:9224）**，严禁干扰本机其他 Agent 正在使用的浏览器状态。
- **测试环境构建**：必须启动完全隔离的独立临时 Headless Chrome 实例（使用随机/空闲调试端口如 9333+ 及 `mktemp -d` 独立临时 profile），验证完毕后干净回收进程与临时目录。
- **代码级测试**：所有修改、重构与新增功能必须具备严谨的 Go 单元测试与场景用例，保证 `go test ./...` 纯代码测试全量通过且无竞态（`-race`）。
- **端到端实机验证**：在上述独立隔离 Chrome 实例中针对 9 个核心场景（受控组件输入、弹窗防挂死、dev overlay 诊断、a11y 节点引用、mock route 等）执行真实验证，产出确凿的执行日志与证据。

## Acceptance Criteria

### 功能特性验证
- [ ] 针对 Issue #1 ~ #9 的每一个具体需求，均有明确的自动化测试用例，`go test ./...` 执行通过率 100%。
- [ ] batch_actions 正确识别并执行 `key` 指令，受控组件在 `fill` 后正确触发双向绑定与 onChange 事件。
- [ ] `a11y_snapshot` 节点 ID 可无缝传递给后续点击或填充工具并成功命中元素。
- [ ] 页面出现 `alert()` / `confirm()` 等原生弹窗时，后续操作不发生死锁超时，能自动解封或拦截。
- [ ] 前端呈现 Vite/Webpack 错误浮层时，诊断逻辑能准确定位错误标题与代码堆栈并提前终止。
- [ ] 元素截图能够按 CSS selector / nodeId 精准裁切，轻量模式直接落盘并返回文件路径。
- [ ] `mock_route` 能够成功拦截指定 URL pattern 并返回自定义 HTTP status / headers / body。

### 工具集精简与规范性
- [ ] 产出工具审查清算报告，清晰记录保留工具、合并工具及删除工具的决策理由。
- [ ] MCP 工具清单精简收敛，清理无用工具的 schema、handler 与测试代码，工程无死代码。

### 隔离端到端实机验证
- [ ] 所有实机测试均在独立隔离临时端口与独立 profile 下运行，全程未触碰 CDP 9224 端口。
- [ ] 成功在独立浏览器环境中完成典型场景的端到端调用，并输出各特性验证通过的证据日志。

## 2026-10-12T04:50:26Z

【最高优先级警报与执行红线】
立即停止任何可能触发 launchChrome() 的测试与命令！刚才已有子任务在测试中由于执行 `ARGUS_CDP_PORT=59999 go test ...`，导致触发了内部 `ensureChromeRunning()` -> `launchChrome()`。而在 macOS（`chrome_darwin.go`）中，`launchChrome()` 会调用 `/usr/bin/open -n -a "/Applications/Chrome Argus.app"`，导致 macOS LaunchServices 持续在用户的屏幕上弹出空白 Chrome 窗口及新建空白标签页！

现已强制终止该测试进程并关闭了所有空白标签页。请立即同步给 Orchestrator 及所有 Worker/Explorer：
1. 绝对禁止裸跑未 mock 的集成测试！常规测试必须强制加 `-short` 参数（即 `go test -short ./...`），跳过所有需要启动 Chrome 的测试。
2. 绝对禁止在 `configuredAttach == false`（未设置 attach 模式）且无 CDP 响应的端口上运行调用了 `resolveTarget` / `ensureChromeRunning` 的测试。
3. 任何需要真实 Chrome 的端到端测试，必须由测试代码自身通过 `exec.Command(binaryPath, ...)` 直接启动带 `--headless=new`、`--user-data-dir=$(mktemp -d)` 的独立二进制进程（严禁通过 macOS `/usr/bin/open -a` 启动 .app），且 Argus 必须以 `ARGUS_ATTACH=true` 模式连接该临时端口，绝不允许触发 `launchChrome()`！

## 2026-10-12T05:43:11Z

服务器已恢复，请继续推进 Milestone 1 的第二轮修复（worker_m1_r2）及后续里程碑。请报告当前状态并继续执行。



## 2026-10-12T06:01:29Z

服务器已重启。请继续恢复执行任务：目前 Milestone 1 第二轮实施已取得突破（真机生命周期与屏障测试全绿），请继续完成终验放行，并立即推进 Milestone 2（Issue #1~#5 核心交互与受控组件重构）及后续里程碑！请报告当前最新状态并继续推进。

## 2026-10-12T07:14:23Z

服务器已重启，请继续执行任务。

## 2026-10-12T07:18:26Z

服务器已重启。请继续恢复执行任务：目前 Milestone 2 第二轮修复补丁（m2_r2_autowait_diff.md, m2_r2_a11y_fill_diff.md）已就绪，请继续调度 Worker 落地补丁、完成二次复核并进入 Milestone 3（Issue #6~#9）！请报告最新状态并继续推进。

## 2026-10-12T07:41:00Z

刚刚被中断了，重启一下subagent并继续任务：Milestone 2 第二轮修复补丁已全部落地并通过测试，请继续唤醒门禁委员会完成复核放行，并立即推进 Milestone 3（Issue #6~#9）及后续里程碑！

## 2026-10-12T11:23:15Z

刚刚被中断了，重启一下subagent并继续任务：Milestone 3 第二轮整改中，请继续推进 Milestone 3 门禁放行与 Milestone 4 工具集精简！

## 2026-10-12T14:21:27Z

刚刚被中断了，重启一下subagent并继续任务：Milestone 4 工具集精简（48 ➜ 26）实施已就绪，请继续推进 Milestone 4 门禁复核与 Milestone 5 全量 E2E 验收！

## 2026-10-12T14:37:42Z

刚刚被中断了，重启一下subagent并继续任务：继续推进 Milestone 4 门禁审核放行与 Milestone 5 终验验收！

## 2026-10-12T15:58:48Z

刚刚被中断了，重启一下subagent并继续任务：Milestone 4 已全票放行归档，请全速推进最终收官里程碑 Milestone 5（全场景实机 E2E 终检闭环与胜利审计）！

## 2026-10-12T17:36:12Z

刚刚被中断了，重启一下subagent并继续任务：Milestone 5 第二轮修复（mock_route 锁重入与弹窗沉降）冲刺收官，请继续推进 Gate 5 终审放行与 Victory Audit 终验确认！
