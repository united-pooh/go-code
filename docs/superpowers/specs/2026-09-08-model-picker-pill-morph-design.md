# Web 模型选择器：胶囊 morph 搜索框设计（Model Picker Pill Morph）

日期：2026-09-08
状态：待评审（交互原型已在视觉伴侣中确认）
范围：`internal/ui/web/ui/src/components/Composer.tsx`、`internal/ui/web/ui/src/styles/workbench.css`

## 背景与问题

Web 端输入区上方的「卡片堆」（`.deck-card`）中，模型与推理强度两个字段使用原生 `<select>`：

- 点击模型胶囊弹出**系统默认下拉框**，视觉风格与暖纸色系 UI 割裂，长模型名（如 `deepseek/deepseek-v4.1-flash-exp`）在系统弹层中显示与截断不可控；
- 无搜索能力：模型目录变长后只能靠滚动查找；
- 系统弹层渲染在页面层级之外，无法与卡片堆的「抽出」动画形成连贯叙事。

## 目标

- 点击「模型」胶囊后，胶囊**向右延展 morph 成搜索框**（同一 DOM 元素承担两种形态，`width` 过渡）；
- 候选列表位于 `.deck-card` **内部**、控件行之下，展开时把卡片高度**往上挤**（非外部浮层）；列表可滚动；
- 输入实时筛选，匹配片段高亮；候选变少时列表与卡片高度**平滑回落**；
- 键盘可达：↑/↓ 移动、Enter 选定、Esc 先清空再关闭；
- 选定或点击外部后，搜索框收缩回胶囊，胶囊宽度**自适应新模型名长度**。

## 非目标

- 推理强度字段本次保持原生 `<select>` 不动；
- 不改 `loadModelOptions` / `onSelectModel` 接口与 Go 端 handlers；
- 不改卡片堆既有的 hover/focus 抽出交互（47px ↔ 82px）；
- 不做分组（provider 分组标题）、不做模型元信息（desc/图标）展示——列表项仅模型 ID 与选中勾。

## 结构设计

### DOM（替换现有 `.deck-field` 内的 `<select aria-label="切换模型">`）

```text
.deck-card
├─ .deck-peek                      （不变）
├─ .deck-row
│  ├─ .deck-field.model-field
│  │  ├─ .deck-tag「模型」
│  │  └─ .model-pill               ← 同一元素双形态
│  │     ├─ .pill-face             （胶囊态：模型名 + chevron）
│  │     └─ .search-face           （搜索态：放大镜 + input + 清除✕；
│  │                                 胶囊态下 position:absolute 脱离布局，
│  │                                 避免 flex:1 的 input 把胶囊撑宽）
│  └─ .deck-field（推理强度 <select>，不变）
└─ .model-dropdown                 ← 卡片内部、控件行下方
   └─ .dropdown-list > .model-option × N
```

要点：`.model-dropdown` 是 `.deck-card` 的子元素（`.deck-row` 的兄弟），`top: 47px`，高度由 CSS 变量 `--list-h` 驱动。

### 卡片高度模型

| 状态 | `deck-card` 高度 |
|---|---|
| 静置 | `47px`（只露 peek 预览带，不变） |
| hover / focus-within | `82px`（抽出控件行，不变） |
| 搜索态（`.search-open`） | `calc(82px + var(--list-h))` —— 列表把卡片往上挤 |

`margin-bottom: -35px` 交叠量不变，输入卡纹丝不动，多出的高度全部向上生长（视觉伴侣中已验证）。`.deck-card` 恢复 `overflow: hidden`，列表不收起时不可见。

### 胶囊宽度模型（自适应）

- **胶囊态**：`width: auto`，完全由模型名内容撑开（无 `max-width` 截断）；
- **展开**：JS 先记录当前胶囊宽度为 `--pill-w`（动画起点，强制 reflow），下一帧设为目标宽度 `clamp(220px, 胶囊宽 + 90px, 460px)`；
- **收缩**：设 `--pill-w` 为当前搜索框宽（起点）→ 移除类后下一帧量出 `width:auto` 下的自然内容宽 → 过渡到该宽度 → 动画结束移除 `--pill-w` 交还 auto 布局。

### 列表高度模型（自适应）

- `render()` 末尾计算 `listH = min(list.scrollHeight, 216px)`（约 6 条，超出内部滚动）；
- 写入 `--list-h` 到 `.deck-card` 与 `.model-dropdown`，两者各有 `height` 过渡，筛选变少时平滑回落；
- 空结果显示单行「没有匹配「query」的模型」。

### 交互与状态机

- 开关状态由 `.deck-card.search-open` 单一类名承载（不引入第二个 `open` 类）；
- 打开：点击胶囊 / 胶囊聚焦时 Enter·Space；打开后 200ms 聚焦 input；
- 关闭：点击卡片外（document click）、Esc（有文字先清空、再按才关）、选定后延迟 140ms 关闭（让打勾动画可见）；
- 搜索态期间卡片强制保持抽出（`.search-open` 与 `:hover` 同效果），移开鼠标不回落；
- 键盘：↑/↓ 移动高亮（`scrollIntoView({block:'nearest'})`）、Enter 选定；
- 选定后联动：`pill-face` 文案、`.deck-peek` 摘要（`名称 · 强度`）、推理强度 select 的 disabled（`reasoning_capable` 为 false 时）——与现有 `applyModelSelection` 逻辑一致。

### 可达性

- 胶囊：`role="button"`、`tabindex="0"`、`aria-label="切换模型"`、`aria-expanded` 跟随开关；
- 列表：`role="listbox"`，选项 `role="option"` + `aria-selected`；
- 输入框：`aria-label="搜索模型"`。

## 错误处理

- 候选为空：显示占位行，Enter 不响应；
- 模型目录在打开期间变化（`modelOptions` 更新）：下一次 `render()` 自然反映，不额外处理；
- `selectingModel`（请求进行中）时重复点击：沿用现有防抖，关闭动画不受请求影响。

## 测试（Composer.test.tsx 更新）

1. 渲染卡片堆，胶囊显示当前模型名（替换原 `<select>` 断言）；
2. 点击胶囊 → 出现搜索框（聚焦）与候选列表，卡片带 `search-open` 类；
3. 输入 `beta` → 列表只剩 `local/beta`；Enter → 触发 `onSelectModel({model_id:'local/beta'})`，胶囊文案更新；
4. 选中不支持推理的模型 → 推理强度 select 禁用（沿用现有断言）；
5. Esc 两次行为：先清空输入、再关闭；点击外部关闭；
6. 未提供 `loadModelOptions` 时不渲染卡片堆（现有断言保留）。

## 落地文件

- `Composer.tsx`：`deck-field` 内 `<select>` → `.model-pill` 双形态结构 + `.model-dropdown` 子树；新增 `searchOpen` / `query` / `activeIdx` 状态与开关/键盘逻辑；
- `workbench.css`：新增 `.model-pill` / `.pill-face` / `.search-face` / `.model-dropdown` / `.dropdown-list` / `.model-option` 规则；`.deck-card` 增加 `.search-open` 高度规则；删除 `.deck-field select` 中仅服务于模型 select 的规则（推理强度规则保留）。
