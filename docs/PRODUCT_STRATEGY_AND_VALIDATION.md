# Argus 战略定位、迭代计划与全链路验证体系

> **创建日期**：2026-09-25  
> **文档定位**：面向“个人开发、个人使用”场景，确立以 **“首先有能力，其次快速完成”** 为北极星的架构演进、工具收敛、迭代路线与 Agent 全链路验证闭环。  
> **关联文档**：[docs/argus-vs-playwright.md](argus-vs-playwright.md)、[docs/roadmap.md](roadmap.md)、[CDP_UI_LIMITATIONS.md](../CDP_UI_LIMITATIONS.md)

---

## 目录
1. [项目定位与防自嗨法则](#一项目定位与防自嗨法则)
2. [核心原则：能力与速度优先](#二核心原则能力与速度优先)
3. [核心能力突破矩阵（攻克深水区）](#三核心能力突破矩阵攻克深水区)
4. [全链路验证与评测体系（黄金用例库）](#四全链路验证与评测体系黄金用例库)
5. [工具集瘦身与认知负荷收敛](#五工具集瘦身与认知负荷收敛)
6. [分阶段迭代路线图（Roadmap）](#六分阶段迭代路线图roadmap)

---

## 一、项目定位与防自嗨法则

### 1.1 明确自身定位
Argus **不是** 面向企业级自动化测试脚本的 Playwright 替代品，而是 **个人全栈开发者在日常 Coding 场景下，AI Coding Agent 的“眼睛、手与诊断黑匣子”**。
- **使用者**：LLM Agent（通过 MCP 协议调用），而非人类测试工程师；
- **核心场景**：本地 DevServer 界面排错、现代单页应用（SPA）交互验证、控制台与网络异常快速溯源、视觉/布局感知；
- **核心价值**：大幅减少开发者手动在 IDE 与浏览器 F12 之间来回切换的上下文损耗。

### 1.2 个人开发者的“三不”防自嗨法则
为避免陷入“闭门造车”与“虚假繁荣”，建立以下硬性准则：

1. **不盲目追求 CDP 协议全覆盖（拒绝 Feature Bloat）**：
   - 绝不因为 CDP 有某个 API 就顺手包装一个 MCP 工具；
   - 每一个新工具必须有自己日常开发中“明确发生过 3 次以上”的真实痛点支撑。
2. **不迷信代码级单元测试全绿（拒绝 Mock Mirage）**：
   - `go test -race` 只能证明底层 Go 序列化和通道没崩，**证明不了 Agent 能把任务做完**；
   - 必须以真实大模型（Agent-in-the-loop）驱动的完整用例作为第一验收标准。
3. **不把 Token 节约作为首要目标（拒绝 False Economy）**：
   - 过度省 Token（如粗暴截断报错堆栈、省略节点层级）会导致 Agent 盲猜、反复尝试，最终引发更多轮次和更高总消耗；
   - **Token 是反映交互摩擦力的“温度计”，而非节衣缩食的“紧箍咒”**。

---

## 二、核心原则：能力与速度优先

```
   ┌─────────────────────────────────────────────────────────┐
   │                    核心诉求优先级                        │
   ├────────────────────────────┬────────────────────────────┤
   │  P0: 首先要有能力          │  P1: 其次要快速完成        │
   │  (Can it actually do it?)  │  (Turn-efficient & Fast)   │
   ├────────────────────────────┼────────────────────────────┤
   │ • 现代受控组件必须能提交   │ • 单轮往返 (Single-Turn)   │
   │ • 异步/Portal 浮层不能漏检 │ • 复合批处理动作           │
   │ • Shadow DOM / Frame 穿透  │ • 报错附带针对性自愈建议   │
   │ • 复杂异常现场完整保留     │ • 现场信息一次给全，不返工 │
   └────────────────────────────┴────────────────────────────┘
```

### 2.1 任务 ROI 评估公式
未来评判 Argus 任何改动优劣的唯一公理：

$$\text{Task ROI} = \frac{\text{任务达成率（是否成功解决实际问题）}}{\text{交互轮次 (Turns)} \times \text{单轮等待时间 (Latency)}}$$

- **极度推崇**：单轮直接搞定（如 `look` 合并图文感知、动作后置 `miniSnapshot` DOM 回显）；
- **坚决剔除**：需要 Agent 反复调用 3 次以上才能拼凑出完整状态的碎化工具。

---

## 三、核心能力突破矩阵（攻克深水区）

要让 Agent 在真实开发中不卡壳，必须攻克现代 Web 技术（React 18、Vue 3、Tailwind、Next.js、Radix/Shadcn UI）的 4 大深水区：

### 3.1 现代受控组件（Controlled Inputs）原生穿透
- **现状缺陷**：单纯修改 `el.value = text` 会绕过 React 内部对 `HTMLInputElement.prototype.value` 的 setter 拦截，受控组件在表单提交或重渲染时直接回滚旧值。
- **能力要求**：底层必须通过原生原型链 Setter 赋值并触发完整的合成事件链：
  ```javascript
  const nativeSetter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set;
  if (nativeSetter) {
    nativeSetter.call(el, text);
  } else {
    el.value = text;
  }
  el.dispatchEvent(new Event('input', { bubbles: true, composed: true }));
  el.dispatchEvent(new Event('change', { bubbles: true, composed: true }));
  ```

### 3.2 动态稳定态侦测（DOM Auto-settling）
- **现状缺陷**：操作下发后若立刻执行快照，遇上微任务调度、数据拉取或 CSS 进场动画，快照容易捕获到空白或过渡态。
- **能力要求**：
  - 在动作执行后，等待 `requestAnimationFrame` 驱动重绘；
  - 监听 DOM Mutation 变化，确保页面在 50~100ms 窗口期内处于稳定状态（Settled State）后再返回快照；
  - 彻底终结 Agent “点击了按钮却看不到弹窗，不得不手动调 wait_for” 的低效等待。

### 3.3 复合动作批处理（Batch Action Pipeline）
- **现状缺陷**：登录一个后台需要 `fill(user)` $\rightarrow$ `fill(pass)` $\rightarrow$ `click(submit)`，耗费 3 个 LLM 轮次（约 15~20 秒）。
- **能力要求**：提供支持批处理的交互原语 `batch_actions`，允许 Agent 在单次请求中下发动作序列，内部流水线执行后一次性返回最终状态：
  ```json
  {
    "actions": [
      {"action": "fill", "target": "#username", "value": "dev"},
      {"action": "fill", "target": "#password", "value": "pass123"},
      {"action": "click", "target": "button[type='submit']"}
    ]
  }
  ```

### 3.4 高信噪比、零脱敏损失的现场还原
- **现状缺陷**：脱敏规则将包含敏感关键字的报错直接整串替换为 `[REDACTED]`，导致文件名、行号与调用栈全失。
- **能力要求**：
  - 仅对敏感 Query/Header 键值进行局部掩码，绝不破坏异常类型和堆栈行号；
  - 页面 500 时，必须在诊断信息中直接附带响应体（Response Body）片段与网络生命周期，让 Agent 看到直接能修代码。

---

## 四、全链路验证与评测体系（黄金用例库）

不再依赖空洞的代码覆盖率，建立面向 Agent 真实体感的四层验证体系：

### 4.1 五大黄金实战用例（The 5 Golden Scenarios）
在项目内建立 `test/scenarios/` 目录，部署 5 个包含现代前端典型特征的最小用例页面，用于一键回归：

| 用例编号 | 场景名称 | 页面特征与挑战 | 核心通过标准 |
| :---: | :--- | :--- | :--- |
| **G1** | **本地 DevServer 报错定位** | 页面加载时触发组件级未捕获 JS 异常，且关键 API 返回 500 | Agent 调 `diagnose` 后，**在 2 轮内**准确定位到报错文件名、代码行及接口失败原因。 |
| **G2** | **复杂受控表单提交** | 基于 React Hook Form 的必填校验登录表单 | Agent 单次完成填表并点击提交，页面成功跳转，**不得出现表单值回滚或触发必填校验警告**。 |
| **G3** | **异步 Portal 浮层控制** | 点击按钮后经 100ms 挂载到 `document.body` 顶层的 Dropdown 菜单 | 后置 `miniSnapshot` **直接可见浮层节点**；Agent 第二轮可直接命中浮层选项，无需额外 `wait`。 |
| **G4** | **选择器歧义自愈** | 页面包含 3 个一模一样的“删除”按钮，仅上下文有别 | 当 Agent 传入通用选择器时，返回的 `Ambiguity Hints` 能引导 Agent 在下一轮直接使用正确属性或 `ref`。 |
| **G5** | **长列表与复杂 DOM 探索** | 拥有 100+ 虚拟滚动条目或嵌套表格 | 返回给 Agent 的 DOM 结构紧凑且高可读，Token 控制在合理范围，Agent **不发生定位迷失**。 |

### 4.2 三大质检北极星指标

```
[Agent 启动任务]
       │
       ├──► 首次尝试是否直接成功？ ────► 【一击必中率】 (目标: > 85%)
       │
       ├──► 消耗了多少个交互往返？ ────► 【平均解决轮次】 (目标: ≤ 2 轮)
       │
       └──► 是否原地打转/盲目重试？ ───► 【摩擦卡顿重试率】 (目标: < 10%)
```

### 4.3 极简轻量级评测脚手架（Eval Runner）
在工程根目录维护一个轻量评测脚本（如 `scripts/run_eval.sh` 或 `eval.py`）：
1. 一键后台启动本地测试静态 Server；
2. 编译并运行当前 Argus 二进制；
3. 用固定测试 Prompt 分别驱动真实的 Agent 执行 G1~G5 场景；
4. 汇总输出控制台看板：
   ```text
   === Argus Agentic Eval Report ===
   G1 (DevServer Error): PASS (Turns: 1, Tokens: 1.4k, Duration: 3.2s)
   G2 (Controlled Form): PASS (Turns: 2, Tokens: 2.1k, Duration: 4.8s)
   G3 (Async Portal):    PASS (Turns: 2, Tokens: 1.9k, Duration: 4.1s)
   G4 (Ambiguity Hint):  PASS (Turns: 2, Tokens: 2.5k, Duration: 5.0s)
   G5 (Virtual List):    PASS (Turns: 1, Tokens: 3.1k, Duration: 3.8s)
   ---------------------------------
   Overall Success Rate: 100% | Mean Turns: 1.6 | Total Wall Time: 20.9s
   ```

---

## 五、工具集瘦身与认知负荷收敛

### 5.1 默认工具集大刀收敛（44 个 ➔ 核心 9 个）
为彻底消除大模型的“选择瘫痪”并节省 System Prompt 空间，默认暴露的工具集大幅缩编：

```
                    ┌─────────────────────────┐
                    │  Argus 默认核心工具 (9) │
                    └────────────┬────────────┘
         ┌───────────────────────┼───────────────────────┐
         ▼                       ▼                       ▼
    【感知层】              【交互层】              【排错层】
  • look                  • click                 • diagnose
  • get_element           • fill                  • network_requests
                          • batch_actions (新增)  • eval (万能兜底)
                          • navigate
```

### 5.2 进阶能力按需开启
- 多标签管理（`new_tab`, `select_tab`）、存储存取（`get_storage`）、移动端模拟（`emulate`）归入 `--toolsets=advanced`；
- 原生文件下载管理、PDF 打印等低频运维操作归入 `--toolsets=admin`；
- 日常 Coding 默认仅加载核心 9 工具，保持模型思维敏捷。

---

## 六、分阶段迭代路线图（Roadmap）

### Phase 0: 紧急排雷与稳定性加固（P0 止血）
- [ ] **解除 EventHub Critical 发布死锁**：重构 `eventhub.go`，禁止在 WebSocket `readLoop` 协程中对满载通道做同步无限等待；
- [ ] **封堵 `pdf` 任意本地文件覆写高危漏洞**：增加路径受控目录检查，增加覆盖保护；
- [ ] **修正 `timeline` 因果时序倒挂**：废弃 `RFC3339Nano` 字符串字典序比对，统一改为 `time.Time` 比较；
- [ ] **修复 `splitComma` 配置解析 Bug**：加入 `strings.TrimSpace`，防止命令行逗号后带空格导致配置项静默丢弃。

### Phase 1: 现代 UI 交互深度与轮次压缩（核心能力构建）
- [ ] **实现 React/Vue 受控表单原生 Setter 穿透**；
- [ ] **实现动作后置微任务/动画帧感知（Auto-settling）**，消除 Portal 浮层漏检；
- [ ] **实现 `batch_actions` 批处理操作工具**，单轮搞定多输入+提交；
- [ ] **脱敏器重构**：改为基于正则的局部值替换，杜绝整行覆盖造成报错现场丢失。

### Phase 2: 全链路评测与真实使用闭环（验证体系落地）
- [ ] **建立本地 G1~G5 黄金基准前端工程**与轻量评测驱动脚本；
- [ ] **默认工具集大刀收敛**，优化核心 9 工具的 MCP Schema 与 Description；
- [ ] **引入本地使用埋点（`~/.argus/usage.jsonl`）**，客观统计日常工具调用频次与重试热点。

### Phase 3: 架构演进与并发隔离（长期演进）
- [ ] **消除包级全局 Target 状态**：工具入参支持显式可选 `target_id`，解除并行 Tool Calling 的竞态风险；
- [ ] **评估底层 CDP 协议栈迁移**：考虑逐步将动作层委托给成熟稳定的 `chromedp` 引擎，Argus 聚焦深耕独有的“感知压缩”与“飞行记录器因果排错”护城河。
