---
kind: frontend_style
name: 基于 Tailwind CSS + CSS 变量的暗/亮双主题系统
category: frontend_style
scope:
    - '**'
source_files:
    - web/tailwind.config.ts
    - web/postcss.config.js
    - web/src/styles/index.css
    - web/package.json
    - web/src/App.tsx
    - web/src/components/ui/Button.tsx
    - web/src/components/ui/Card.tsx
    - web/src/components/ui/Chip.tsx
---

## 1. 使用的体系与工具

- **构建与样式管线**：Vite（`vite.config.ts`）+ PostCSS（`postcss.config.js`，启用 `tailwindcss`、`autoprefixer`）。
- **原子化样式框架**：Tailwind CSS v3（`tailwind.config.ts`），通过 `content: ['./index.html', './src/**/*.{ts,tsx}']` 扫描源码生成 CSS。
- **主题切换机制**：在 `html` 根节点上切换 `.dark` / `.light` class（见 `web/src/styles/index.css` 中的 `:root.dark` / `:root.light` 选择器），配合 Tailwind 的 `darkMode: 'class'` 实现运行时主题切换。
- **设计令牌（Design Tokens）**：所有颜色通过 CSS 自定义属性集中定义（`--bg`、`--card`、`--border`、`--text`、`--accent`、`--info`、`--warn`、`--ok`、`--danger` 等），并在 Tailwind 配置中以 `rgb(var(--token) / <alpha-value>)` 形式暴露为语义色名（如 `bg`、`card`、`accent`、`text-muted` 等），使组件代码无需感知明暗主题。
- **字体**：无衬线体使用 Inter + system-ui，等宽体使用 JetBrains Mono（`tailwind.config.ts` 中 `fontFamily` 扩展）。
- **动画**：Tailwind `animation` 扩展了 `pulse-dot`；全局定义了 `rise`、`fade-in`、`scale-in` 三个 keyframes，并通过 `@media (prefers-reduced-motion: no-preference)` 尊重减少动效偏好。
- **第三方 UI 库适配**：对 react-flow 的 Controls / MiniMap 提供深色/浅色两套覆盖规则（`.react-flow__controls`、`.react-flow__minimap` 等），使其与 Ongrid 的 zinc 主题一致。
- **打印输出**：通过 `@media print` 将报告区域强制渲染为白底黑字 PDF，保留 accent 色并避免卡片跨页断裂。

## 2. 关键文件

- `web/tailwind.config.ts` — Tailwind 主题扩展（字体、语义色、动画、keyframes）。
- `web/postcss.config.js` — PostCSS 插件链（tailwindcss + autoprefixer）。
- `web/src/styles/index.css` — 全部设计令牌、明/暗主题变量、zinc 色到语义色的 light-mode remap、全局滚动条、focus ring、Markdown body 样式、react-flow 覆盖、打印样式。
- `web/package.json` — 依赖声明（React 18、Tailwind、PostCSS、Autoprefixer、recharts、xterm、zustand、lucide-react 等）。
- `web/src/App.tsx` — 路由入口，页面按业务域拆分至 `pages/`，布局由 `components/Layout.tsx` 承载。
- `web/src/components/ui/` — 共享基础 UI 组件（Button、Card、Chip、EmptyState、PageHeader、PaginationFooter、RoleSelect）。

## 3. 架构与约定

### 3.1 主题与令牌分层

- **层 1：CSS 变量**（`index.css` 顶部 `:root.dark` / `:root.light`）—— 定义品牌色（`--accent` 取自 logo #8C6DF0）、状态色（info/warn/ok/danger）及中性色阶（bg/card/border/text）。注释明确说明每个 token 的来源与设计意图。
- **层 2：Tailwind 语义色映射**（`tailwind.config.ts` 的 `theme.extend.colors`）—— 把 CSS 变量包装成 `bg`、`card`、`accent` 等语义类，供组件直接使用，从而屏蔽明暗差异。
- **层 3：Light-mode zinc 重映射**（`index.css` 中大量 `html.light .bg-zinc-*` 规则）—— 由于历史原因组件广泛硬编码 `zinc-*` 工具类（注释称约 1500 处），通过更高特异性的 `html.light` 选择器将常用 zinc 值映射到对应语义 token，保证现有代码在亮色下可读。新增组件应优先使用语义 token，而非继续添加 zinc 硬编码。

### 3.2 组件级样式约定

- 组件位于 `src/components/`，通用原子组件集中在 `src/components/ui/`，业务组件按功能目录组织（`monitor/`、`topology/`、`marketplace/`、`icons/`）。
- 图标统一使用 lucide-react（`package.json` 依赖），不内嵌 SVG。
- 图表使用 recharts，流程图使用 @xyflow/react（react-flow），表格使用原生 HTML table + Tailwind 工具类。
- Markdown 内容（助手气泡、知识库等）统一通过 `.md-body` 类应用排版样式，包含标题层级、代码块、列表、表格、链接等。

### 3.3 响应式策略

- 完全基于 Tailwind 的断点系统（sm/md/lg/xl 等），未见媒体查询手写布局逻辑。
- E2E 测试中包含 `packet-viewer-responsive.spec.ts`，验证移动端响应行为。

### 3.4 可访问性（a11y）

- 全局 `*:focus-visible` 定义 outline，输入控件通过边框变化表达 focus（`input:focus` 等显式 `outline: none !important` 以避免双重指示）。
- 动画遵循 `prefers-reduced-motion`。
- 代码块、表格、链接等在明暗主题下分别调整对比度以满足 AA 要求（注释中有明确说明）。

## 4. 约定与约束

| 约定 | 来源/证据 | 说明 |
|------|-----------|------|
| 主题通过 `html.dark` / `html.light` class 切换 | `index.css` 中 `:root.dark` / `:root.light` 选择器 | 组件不应直接操作 `document.body.style` 切换主题，而是依赖该 class |
| 颜色必须走语义 token（`bg`、`card`、`accent`…） | `tailwind.config.ts` 的 `colors` 扩展 + `index.css` 中注释 | 新组件应使用 `bg-card`、`text-text` 等，而非直接写 `#xxx` 或 `zinc-*` |
| 新增组件优先使用语义 token，避免继续增加 zinc 硬编码 | `index.css` 中“Migrating each to semantic tokens is a multi-day refactor”注释 | 历史遗留的 ~1500 处 zinc 硬编码通过 light-mode remap 兼容，但新代码应避免 |
| 品牌主色固定为 `--accent`（#8C6DF0 家族） | `index.css` 注释“Brand accent — middle stop of the logo's left pillar” | 按钮主 CTA 使用中性高对比填充（`bg-zinc-100.text-zinc-900` 在亮色下翻转为深底浅字），brand purple 仅用于徽章/强调面 |
| 第三方库需适配主题 | `index.css` 中对 react-flow 的 `.react-flow__controls`、`.react-flow__minimap` 覆盖 | 引入新 UI 库时需为其提供 dark/light 两套覆盖规则 |
| 打印输出强制白底黑字 | `index.css` `@media print` 块 | 报告导出通过 `window.print()` 隔离，使用 `.report-print-area` 包裹内容 |
| 动画尊重用户偏好 | `index.css` `@media (prefers-reduced-motion: no-preference)` | 所有微动画（rise/fade/scale/pulse-dot）在此条件下生效 |
| 图标统一来自 lucide-react | `package.json` 依赖 | 不在组件内联 SVG 路径 |
| 代码字体统一为 JetBrains Mono | `tailwind.config.ts` `fontFamily.mono` | 终端（xterm）、代码块、monospace 文本均使用该字体 |

## 5. 总结

Ongrid Web 前端采用 **Tailwind CSS 原子化 + CSS 自定义属性语义令牌** 的双层样式架构：底层用 `--bg/--card/--accent` 等 token 定义品牌与状态色，上层通过 Tailwind 语义色名暴露给组件。主题切换通过 `html.dark` / `html.light` class 驱动，同时用大量 `html.light .bg-zinc-*` 规则对历史遗留的 zinc 硬编码进行透明重映射，确保渐进迁移。整体风格以 zinc 深色系为主、辅以品牌紫色 accent，兼顾暗/亮模式的可读性与一致性，并对 react-flow、markdown、打印输出等场景做了针对性适配。