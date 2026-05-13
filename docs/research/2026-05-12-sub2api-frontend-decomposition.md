# Sub2API Frontend UI 风格 Decomposition 文档

**生成日期**: 2026-05-12  
**参考项目**: Sub2API (Vue 3 + Vite + Tailwind + Pinia + chart.js + @lobehub/icons)  
**受众**: HUAKAI Round 8 P1 Dashboard 重做（Gemini React/Next.js 14 stack）  
**分类**: Research / Reference Architecture

---

## 目录

1. [整体 Layout 架构](#1-整体-layout-架构)
2. [设计 Token 体系](#2-设计-token-体系)
3. [核心 UI 组件](#3-核心-ui-组件)
4. [图标方案](#4-图标方案)
5. [图表实现](#5-图表实现)
6. [中文文案体系](#6-中文文案体系)
7. [Dashboard 总览页详解](#7-dashboard-总览页详解)
8. [Top-N Table 展现模式](#8-top-n-table-展现模式)
9. [响应式设计](#9-响应式设计)
10. [HUAKAI 落地映射表](#10-huakai-落地映射表)

---

## 1. 整体 Layout 架构

### 1.1 页面结构

Sub2API 采用 **Sidebar + Main Content** 的经典两栏布局（文件: `~/refs/sub2api/frontend/src/components/layout/AppLayout.vue:1-22`）。

```
┌─────────────────────────────────────────────┐
│         AppHeader (顶部导航条)              │
├──────────┬────────────────────────────────┤
│          │                                │
│ Sidebar  │     Main Content (Router)     │
│ 可折叠   │     (p-4 md:p-6 lg:p-8)       │
│(w-64/)   │                                │
│(w-72px)  │                                │
│          │                                │
└──────────┴────────────────────────────────┘
```

**Sidebar 状态**:
- **展开**: `w-64` (256px)
- **折叠**: `w-[72px]` (72px icon-only mode)
- 响应式: `lg:ml-64` / `lg:ml-[72px]` 主内容区动态margin
- 深色背景装饰: `bg-mesh-gradient` (径向渐变mesh)

**关键 CSS 类** (文件: `~/refs/sub2api/frontend/src/style.css:194-206`):
- `.glass`: 毛玻璃效果 `bg-white/80 backdrop-blur-xl`
- `.glass-card`: 玻璃卡片 `bg-white/70 rounded-2xl border-white/20`

### 1.2 路由结构 (文件: `~/refs/sub2api/frontend/src/router/index.ts`)

**主要路由**：
- `/dashboard` - 用户仪表盘 (DashboardView.vue)
- `/admin/dashboard` - 管理员仪表盘 (DashboardView.vue)
- `/admin/ops` - 运维监控 (OpsDashboard.vue)
- `/keys`, `/usage`, `/redeem`, `/affiliate` - 用户功能页
- `/admin/users`, `/admin/channels`, `/admin/accounts`, `/admin/orders` - 管理功能

**视图文件数**: 50+ 个 Vue 文件 (`~/refs/sub2api/frontend/src/views/`)

---

## 2. 设计 Token 体系

### 2.1 色彩系统 (文件: `~/refs/sub2api/frontend/tailwind.config.js:7-49`)

#### 主色调（Primary - 青色/Teal系）
```
primary: {
  50: #f0fdfa,   100: #ccfbf1,  200: #99f6e4,
  300: #5eead4,  400: #2dd4bf,  500: #14b8a6 (主色),
  600: #0d9488,  700: #0f766e,  800: #115e59,
  900: #134e4a,  950: #042f2e
}
```
- **标准色**: `#14b8a6` (RGB: 20, 184, 166)
- **Hover**: `#0d9488`
- **亮模式背景**: `#f0fdfa`
- **暗模式背景**: `#042f2e`

#### 辅助色（Accent - 深蓝灰系）
```
accent: {
  50: #f8fafc,   100: #f1f5f9,  200: #e2e8f0,
  300: #cbd5e1,  400: #94a3b8,  500: #64748b,
  600: #475569,  700: #334155,  800: #1e293b,
  900: #0f172a,  950: #020617
}
```
- 用于**文本**、**边框**、**禁用状态**

#### 状态色
- **Success**: Emerald/Green (`#10b981`, `#06b6d4`)
- **Warning**: Amber (`#f59e0b`)
- **Error**: Red (`#ef4444`)
- **Info**: Blue (`#3b82f6`)

#### 深色模式 (Dark Theme)
- 深色背景: `#0f172a` (dark-900) / `#020617` (dark-950)
- 深色卡片: `#1e293b` (dark-800)
- 深色表面: `#334155` (dark-700)
- 深色文本: `#f3f4f6` (灰-100)

### 2.2 字体系统 (文件: `~/refs/sub2api/frontend/tailwind.config.js:51-65`)

**衬线字体** (Sans):
```
system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, Roboto, 
Helvetica Neue, Arial, PingFang SC, Hiragino Sans GB, Microsoft YaHei, sans-serif
```
- **优先级**: 系统字体 > 开源中文字体 > 备用

**等宽字体** (Mono):
```
ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace
```
- 用于代码、API Key 显示

### 2.3 字号阶梯 (Tailwind default)

| Class | Size | Usage |
|-------|------|-------|
| `text-xs` | 0.75rem | 标签、提示文本 |
| `text-sm` | 0.875rem | 表格单元格、按钮 |
| `text-base` | 1rem | 正文、小标题 |
| `text-lg` | 1.125rem | 卡片标题 |
| `text-xl` | 1.25rem | 统计数值 |
| `text-2xl` | 1.5rem | 重要统计值 |
| `text-3xl` | 1.875rem | 页面标题 |

**font-weight 阶梯**:
- `font-medium` (500): 标签、小标题
- `font-semibold` (600): 卡片标题
- `font-bold` (700): 统计值、重要信息

### 2.4 间距/Spacing 节奏

**基础单位**: 4px (Tailwind default)

| Spacing | Pixels | Usage |
|---------|--------|-------|
| `gap-1` | 4px | 紧凑组件内间距 |
| `gap-2` | 8px | 图标+文本间距 |
| `gap-3` | 12px | 卡片内部块间距 |
| `gap-4` | 16px | 主要分隔间距 |
| `gap-6` | 24px | 区块间间距 |
| `p-4` | 16px | 卡片内边距 |
| `p-6` | 24px | 大卡片内边距 |

### 2.5 边框圆角 (Border Radius)

```css
rounded-lg    /* 8px */
rounded-xl    /* 12px */ - 按钮、输入框默认
rounded-2xl   /* 16px */ - 卡片、模态框默认
rounded-4xl   /* 32px */ - hero/banner 元素
```

### 2.6 阴影系统 (文件: `~/refs/sub2api/frontend/tailwind.config.js:67-74`)

```javascript
boxShadow: {
  glass: '0 8px 32px rgba(0, 0, 0, 0.08)',
  'glass-sm': '0 4px 16px rgba(0, 0, 0, 0.06)',
  glow: '0 0 20px rgba(20, 184, 166, 0.25)',         // 青色发光
  'glow-lg': '0 0 40px rgba(20, 184, 166, 0.35)',
  card: '0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06)',
  'card-hover': '0 10px 40px rgba(0, 0, 0, 0.08)',
  'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
}
```

**动画阴影**:
- 卡片 hover: 从 `shadow-card` → `shadow-card-hover` (提升效果)
- 主色按钮: 带 `shadow-primary-500/25` (彩色阴影)

---

## 3. 核心 UI 组件

### 3.1 卡片 (Card)

**样式定义** (文件: `~/refs/sub2api/frontend/src/style.css:207-240`):

```css
.card {
  @apply bg-white dark:bg-dark-800/50;
  @apply rounded-2xl;
  @apply border border-gray-100 dark:border-dark-700/50;
  @apply shadow-card;
  @apply transition-all duration-300;
}

.card-hover {
  @apply hover:-translate-y-0.5 hover:shadow-card-hover;
  @apply hover:border-gray-200 dark:hover:border-dark-600;
}

.card-header { @apply border-b border-gray-100 dark:border-dark-700 px-6 py-4; }
.card-body { @apply p-6; }
.card-footer { @apply border-t border-gray-100 dark:border-dark-700 px-6 py-4; }
```

**实际用法** (文件: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:3-5`):
```html
<div class="card p-4">
  <div class="flex items-center gap-3">
    <!-- content -->
  </div>
</div>
```

**特性**:
- 边框: 1px 灰色 (浅: `#f3f4f6`, 暗: `dark-700`)
- 圆角: 16px (rounded-2xl)
- 阴影: 极淡 `card` + hover 提升效果
- Padding: `p-4` (16px) 到 `p-6` (24px)
- Transition: 300ms all (smooth hover)

### 3.2 表格 (DataTable)

**文件**: `~/refs/sub2api/frontend/src/components/common/DataTable.vue:1-100`

**特性**:
- **Sticky Header**: 顶部行固定滚动
- **Responsive**: 桌面端表格 / 移动端卡片列表
- **Sortable**: 按列头点击排序
- **Row Hover**: `hover:bg-gray-50 dark:hover:bg-dark-800/30`
- **虚拟列表**: @tanstack/vue-virtual (大数据集)
- **分页**: Pagination 组件集成

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:290-319`):
```css
.table-header { 
  @apply bg-gray-50 dark:bg-dark-800/50;
  @apply text-gray-600 dark:text-dark-300;
}
.table td { 
  @apply px-4 py-3;
  @apply border-b border-gray-100 dark:border-dark-800;
}
.table tbody tr { 
  @apply hover:bg-gray-50 dark:hover:bg-dark-800/30;
  @apply transition-colors duration-150;
}
```

### 3.3 状态徽章 (Badge / Tag)

**文件**: `~/refs/sub2api/frontend/src/components/common/StatusBadge.vue`

**代码**:
```vue
<template>
  <div class="flex items-center gap-1.5">
    <span :class="['inline-block h-2 w-2 rounded-full', variantClass]"></span>
    <span class="text-sm text-gray-700 dark:text-gray-300">{{ label }}</span>
  </div>
</template>
<script>
const variantClass = computed(() => {
  switch (props.status) {
    case 'active': case 'success': return 'bg-green-500'
    case 'warning': case 'inactive': return 'bg-yellow-500'
    case 'error': case 'danger': return 'bg-red-500'
    default: return 'bg-gray-400'
  }
})
</script>
```

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:321-349`):
```css
.badge {
  @apply inline-flex items-center gap-1;
  @apply rounded-full px-2.5 py-0.5 text-xs font-medium;
}

.badge-primary { @apply bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400; }
.badge-success { @apply bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400; }
.badge-warning { @apply bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400; }
.badge-danger { @apply bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400; }
.badge-gray { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-300; }
```

### 3.4 数据指标 (StatCard)

**文件**: `~/refs/sub2api/frontend/src/components/common/StatCard.vue:1-81`

**组件签名**:
```typescript
interface Props {
  title: string           // "API Keys"
  value: number | string  // 42
  icon?: Component        // Icon 组件
  iconVariant?: 'primary' | 'success' | 'warning' | 'danger'
  change?: number         // +15 (百分比)
  changeType?: 'up' | 'down' | 'neutral'
  formatValue?: (value) => string
}
```

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:242-288`):
```css
.stat-card {
  @apply card p-5;
  @apply flex items-start gap-4;
}

.stat-icon {
  @apply h-12 w-12 rounded-xl;
  @apply flex items-center justify-center;
  @apply text-xl;
}

.stat-value { @apply text-2xl font-bold text-gray-900 dark:text-white truncate; }
.stat-label { @apply text-sm text-gray-500 dark:text-dark-400; }

.stat-trend { @apply mt-1 flex items-center gap-1 text-xs font-medium; }
.stat-trend-up { @apply text-emerald-600 dark:text-emerald-400; }
.stat-trend-down { @apply text-red-600 dark:text-red-400; }
```

**实际用法** (文件: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:1-68`):
```html
<div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
  <div class="card p-4">
    <div class="flex items-center gap-3">
      <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30">
        <svg class="h-5 w-5 text-emerald-600 dark:text-emerald-400">...</svg>
      </div>
      <div>
        <p class="text-xs font-medium text-gray-500">{{ t('dashboard.balance') }}</p>
        <p class="text-xl font-bold text-emerald-600">${{ formatBalance(balance) }}</p>
        <p class="text-xs text-gray-500">{{ t('common.available') }}</p>
      </div>
    </div>
  </div>
</div>
```

### 3.5 输入框 (Input)

**文件**: `~/refs/sub2api/frontend/src/components/common/Input.vue:1-103`

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:154-191`):
```css
.input {
  @apply w-full rounded-xl px-4 py-2.5 text-sm;
  @apply bg-white dark:bg-dark-800;
  @apply border border-gray-200 dark:border-dark-600;
  @apply transition-all duration-200;
  @apply focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500/30;
}

.input-label { @apply mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300; }
.input-error { @apply border-red-500 focus:ring-red-500/30; }
```

### 3.6 按钮 (Button)

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:67-152`):

```css
.btn {
  @apply inline-flex items-center justify-center gap-2;
  @apply rounded-xl px-4 py-2.5 text-sm font-medium;
  @apply transition-all duration-200 ease-out;
  @apply focus:outline-none focus:ring-2 focus:ring-primary-500/50;
  @apply disabled:cursor-not-allowed disabled:opacity-50;
  @apply active:scale-[0.98];
}

.btn-primary {
  @apply bg-gradient-to-r from-primary-500 to-primary-600;
  @apply text-white shadow-md shadow-primary-500/25;
  @apply hover:from-primary-600 hover:to-primary-700 hover:shadow-lg;
}

.btn-secondary {
  @apply bg-white dark:bg-dark-800;
  @apply text-gray-700 dark:text-gray-200;
  @apply border border-gray-200 dark:border-dark-600;
  @apply hover:bg-gray-50 dark:hover:bg-dark-700;
}

.btn-danger, .btn-success, .btn-warning {
  @apply bg-gradient-to-r ... /* 各有自己的颜色渐变 */
}

.btn-sm { @apply rounded-lg px-3 py-1.5 text-xs; }
.btn-md { @apply rounded-xl px-4 py-2 text-sm; }
.btn-lg { @apply rounded-2xl px-6 py-3 text-base; }
```

**特性**:
- 渐变背景 (primary 从 500→600)
- 彩色阴影 (shadow-primary-500/25)
- Active 压下效果 (scale-[0.98])
- Disabled 灰化

### 3.7 下拉菜单 (Select / Dropdown)

**样式** (文件: `~/refs/sub2api/frontend/src/style.css:351-368`):
```css
.dropdown {
  @apply absolute z-50;
  @apply bg-white dark:bg-dark-800;
  @apply rounded-xl;
  @apply border border-gray-200 dark:border-dark-700;
  @apply shadow-lg;
  @apply origin-top-right animate-scale-in;
}

.dropdown-item {
  @apply px-4 py-2 text-sm;
  @apply hover:bg-gray-100 dark:hover:bg-dark-700;
  @apply cursor-pointer transition-colors;
}
```

---

## 4. 图标方案

### 4.1 @lobehub/icons 库

**文件**: `~/refs/sub2api/frontend/package.json:18`
```json
"@lobehub/icons": "^4.0.2"
```

**说明**: 
- 不在代码中直接使用 @lobehub/icons 组件
- 而是**提取 SVG path 并自实现** Icon 组件
- 参考文件: `~/refs/sub2api/frontend/src/components/icons/Icon.vue:13-134`

### 4.2 Icon.vue 实现

**文件**: `~/refs/sub2api/frontend/src/components/icons/Icon.vue`

**组件签名**:
```typescript
interface Props {
  name: keyof typeof icons   // 'key', 'chart', 'clock', etc.
  size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl'
  strokeWidth?: number       // default 1.5
}
```

**尺寸映射**:
```typescript
xs: 'h-3 w-3'     /* 12px */
sm: 'h-4 w-4'     /* 16px */
md: 'h-5 w-5'     /* 20px */
lg: 'h-6 w-6'     /* 24px */
xl: 'h-8 w-8'     /* 32px */
```

**支持的图标** (部分):
- 动作: `play`, `refresh`, `edit`, `trash`, `plus`, `search`, `more`
- 导航: `chevronDown`, `chevronRight`, `chevronLeft`, `arrowRight`, `arrowLeft`
- 状态: `check`, `x`, `eye`, `eyeOff`, `checkCircle`, `xCircle`
- 数据: `chart`, `chartBar`, `trendingUp`, `database`, `cube`
- 账户: `user`, `users`, `userPlus`, `userCircle`
- 其他: `key`, `lock`, `shield`, `menu`, `calendar`, `home`, `settings`

**关键 Icon 用法** (文件: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:24-128`):
```html
<Icon name="key" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
<Icon name="chart" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
<Icon name="dollar" size="md" class="text-purple-600 dark:text-purple-400" :stroke-width="2" />
```

---

## 5. 图表实现

### 5.1 依赖栈

**文件**: `~/refs/sub2api/frontend/package.json:23, 31`
```json
"chart.js": "^4.4.1",
"vue-chartjs": "^5.3.0"
```

### 5.2 图表类型使用

**Doughnut (甜甜圈)** - 模型分布
- 文件: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardCharts.vue:25-58`
- 配置: Legend at top, 并行表格展示详细数据

**Line (折线)** - Token 趋势
- 文件: `~/refs/sub2api/frontend/src/components/charts/TokenUsageTrend.vue:1-228`
- 多条线: Input, Output, Cache Creation, Cache Read, Cache Hit Rate (%)
- 双 Y 轴: 左侧数值, 右侧百分比
- 色系: 蓝(Input), 绿(Output), 黄(Cache Creation), 青(Cache Read), 紫(Hit Rate)

### 5.3 图表颜色响应式 (暗色模式)

**文件**: `~/refs/sub2api/frontend/src/components/charts/TokenUsageTrend.vue:57-69`

```javascript
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb',
  input: '#3b82f6',      // 蓝
  output: '#10b981',     // 绿
  cacheCreation: '#f59e0b',  // 黄
  cacheRead: '#06b6d4',   // 青
  cacheHitRate: '#8b5cf6'   // 紫
}))
```

### 5.4 图表共通配置

**File**: `~/refs/sub2api/frontend/src/components/charts/TokenUsageTrend.vue:126-205`

```javascript
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { intersect: false, mode: 'index' },
  plugins: {
    legend: {
      position: 'top',
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        padding: 15,
        font: { size: 11 }
      }
    },
    tooltip: {
      callbacks: {
        label: (context) => {
          if (context.dataset.yAxisID === 'yPercent') {
            return `${context.dataset.label}: ${context.raw.toFixed(1)}%`
          }
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        }
      }
    }
  },
  scales: {
    x: { grid: { color: chartColors.value.grid }, ticks: { color: chartColors.value.text } },
    y: { grid: { color: chartColors.value.grid }, ticks: { color: chartColors.value.text } },
    yPercent: { position: 'right', min: 0, max: 100, grid: { drawOnChartArea: false } }
  }
}))
```

---

## 6. 中文文案体系

### 6.1 i18n 结构

**文件**: `~/refs/sub2api/frontend/src/i18n/locales/`
- `en.ts` - 英文 (200+ keys)
- `zh.ts` - 中文

### 6.2 Dashboard 相关 Keys 示例

**文件**: `~/refs/sub2api/frontend/src/i18n/locales/en.ts:1-100`

```typescript
export default {
  dashboard: {
    balance: 'Balance',
    apiKeys: 'API Keys',
    todayRequests: 'Today Requests',
    todayTokens: 'Today Tokens',
    totalTokens: 'Total Tokens',
    todayCost: 'Today Cost',
    avgResponse: 'Avg Response Time',
    performance: 'Performance',
    modelDistribution: 'Model Distribution',
    tokenUsageTrend: 'Token Usage Trend',
    recentUsage: 'Recent Usage',
    last7Days: 'Last 7 Days',
    noUsageRecords: 'No Usage Records',
    startUsingApi: 'Start using API to see usage here',
    viewAllUsage: 'View All Usage',
    timeRange: 'Time Range',
    granularity: 'Granularity',
    day: 'Day',
    hour: 'Hour',
    model: 'Model',
    requests: 'Requests',
    tokens: 'Tokens',
    actual: 'Actual',
    standard: 'Standard',
    input: 'Input',
    output: 'Output',
    averageTime: 'Average Time',
    noDataAvailable: 'No Data Available'
  },
  common: {
    active: 'Active',
    total: 'Total',
    error: 'Error',
    available: 'Available',
    refresh: 'Refresh',
    login: 'Login'
  },
  admin: {
    dashboard: {
      title: 'Admin Dashboard',
      description: 'System Overview',
      accounts: 'Accounts',
      todayRequests: 'Today Requests',
      users: 'New Users',
      tokenUsageTrend: 'Token Usage Trend'
    }
  }
}
```

### 6.3 i18n 使用模式

```vue
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
</script>

<template>
  <p class="text-xs font-medium text-gray-500">{{ t('dashboard.balance') }}</p>
  <p class="text-xl font-bold">{{ t('common.active') }}</p>
</template>
```

---

## 7. Dashboard 总览页详解

### 7.1 用户 Dashboard 布局

**文件**: `~/refs/sub2api/frontend/src/views/user/DashboardView.vue:1-37`

**页面结构**:
```vue
<AppLayout>
  <div class="space-y-6">
    <!-- 加载态 -->
    <LoadingSpinner v-if="loading" />
    
    <template v-else-if="stats">
      <!-- 第一行: 核心统计卡片 (4列 Grid) -->
      <UserDashboardStats :stats="stats" :balance="user?.balance" :is-simple="isSimple" />
      
      <!-- 第二行: 图表区域 -->
      <UserDashboardCharts 
        v-model:startDate="startDate" 
        v-model:endDate="endDate" 
        v-model:granularity="granularity"
        :loading="loadingCharts"
        :trend="trendData"
        :models="modelStats"
        @dateRangeChange="loadCharts"
      />
      
      <!-- 第三行: 2/3 列 + 侧边栏 -->
      <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div class="lg:col-span-2">
          <UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" />
        </div>
        <div class="lg:col-span-1">
          <UserDashboardQuickActions />
        </div>
      </div>
    </template>
  </div>
</AppLayout>
```

### 7.2 核心统计行 (UserDashboardStats)

**文件**: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:1-134`

**第一行** (4 列 grid, `grid-cols-2 gap-4 lg:grid-cols-4`):

1. **余额 (Balance)** - 仅非简易模式
   - 图标: 钱币 (emerald-100 bg)
   - 大字: `$1,234.56` (text-xl bold emerald-600)
   - 标签: "Balance" (text-xs gray)
   - 描述: "Available"

2. **API Keys**
   - 图标: 钥匙 (blue-100 bg)
   - 大字: `42` (text-xl bold gray-900)
   - 标签: "API Keys"
   - 描述: `38 Active`

3. **今日请求**
   - 图标: 图表 (green-100 bg)
   - 大字: `12,345` (text-xl bold gray-900)
   - 标签: "Today Requests"
   - 描述: `Total: 1.2M`

4. **今日成本**
   - 图标: 美元 (purple-100 bg)
   - 大字: `$12.34 / $50.00` (text-xl bold mixed color)
   - 标签: "Today Cost"
   - 描述: `Total: $123.45 / $500.00`

**第二行** (4 列 grid):

5. **今日 Tokens**
   - 数值 + 分解: Input / Output

6. **总 Tokens**
   - 数值 + 分解: Input / Output

7. **性能指标** (RPM/TPM)
   - RPM: 大字 (text-xl bold)
   - TPM: 小字 (text-sm bold violet-600)

8. **平均响应时间**
   - 单位: ms / s
   - 图标: 时钟 (rose-100 bg)

**样式要点**:
- 每个卡片: `card p-4` (rounded-2xl, border-gray-100, shadow-card)
- 图标背景: `rounded-lg bg-[color]-100 p-2 dark:bg-[color]-900/30`
- 图标颜色: `text-[color]-600 dark:text-[color]-400`
- 数值颜色: 根据状态 (余额绿色, 成本紫色等)

### 7.3 图表行 (UserDashboardCharts)

**文件**: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardCharts.vue:1-80`

**时间范围过滤** (card p-4):
```
[开始日期] [结束日期] | [刷新按钮] | ... | [Granularity: Day/Hour]
```

**图表网格** (`grid grid-cols-1 gap-6 lg:grid-cols-2`):

1. **模型分布 Doughnut**
   - 左: Doughnut 图 (h-48 w-48)
   - 右: 模型表格 (max-h-48 overflow-y-auto)
     - 列: Model | Requests | Tokens | Actual | Standard
     - 颜色高亮: Actual (绿色 `text-green-600`)

2. **Token 趋势 Line**
   - 高度: h-48
   - 5 条线: Input/Output/Cache Creation/Cache Read/Hit Rate
   - 双 Y 轴

### 7.4 最近使用 + 快速操作 (3列 layout)

**左侧 (lg:col-span-2)** - UserDashboardRecentUsage

**文件**: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:1-57`

```html
<div class="card">
  <div class="flex items-center justify-between border-b border-gray-100 px-6 py-4">
    <h2 class="text-lg font-semibold">Recent Usage</h2>
    <span class="badge badge-gray">Last 7 Days</span>
  </div>
  <div class="p-6">
    <div v-for="log in data" :key="log.id" 
         class="flex items-center justify-between rounded-xl bg-gray-50 p-4 
                hover:bg-gray-100 transition-colors">
      <!-- 左: 图标 + 模型名 + 时间 -->
      <div class="flex items-center gap-4">
        <div class="h-10 w-10 rounded-xl bg-primary-100 dark:bg-primary-900/30">
          <Icon name="beaker" size="md" class="text-primary-600" />
        </div>
        <div>
          <p class="text-sm font-medium">{{ log.model }}</p>
          <p class="text-xs text-gray-500">{{ formatDateTime(log.created_at) }}</p>
        </div>
      </div>
      <!-- 右: 成本 + tokens -->
      <div class="text-right">
        <p class="text-sm font-semibold">
          <span class="text-green-600">${{ formatCost(log.actual_cost) }}</span>
          <span class="text-gray-400"> / ${{ formatCost(log.total_cost) }}</span>
        </p>
        <p class="text-xs text-gray-500">{{ totalTokens.toLocaleString() }} tokens</p>
      </div>
    </div>
    <router-link to="/usage" class="flex items-center justify-center gap-2 py-3 
                 text-sm font-medium text-primary-600 hover:text-primary-700">
      View All Usage
      <Icon name="arrowRight" size="sm" />
    </router-link>
  </div>
</div>
```

**特性**:
- 列表项: 卡片风格 `bg-gray-50 rounded-xl p-4 hover:bg-gray-100`
- 最多 5 条记录 + "查看全部" 链接
- 左图标, 右数值对齐

**右侧 (lg:col-span-1)** - UserDashboardQuickActions
- 快速操作按钮集合 (生成 API Key, 查看文档等)

### 7.5 分页 & 加载状态

**加载态**: `<LoadingSpinner />`
**空态**: `<EmptyState title="..." description="..." />`

---

## 8. Top-N Table 展现模式

### 8.1 虚拟列表集成

**文件**: `~/refs/sub2api/frontend/package.json:20`
```json
"@tanstack/vue-virtual": "^3.13.23"
```

**使用场景**: 
- Admin 用户列表 (User Management)
- Admin 订单列表 (Order Management)
- Admin 账户列表 (Account Management)

### 8.2 DataTable 响应式模式

**文件**: `~/refs/sub2api/frontend/src/components/common/DataTable.vue:1-200`

**桌面端** (Desktop Viewport):
```html
<table class="w-full divide-y divide-gray-200">
  <thead class="bg-gray-50 dark:bg-dark-800">
    <tr>
      <th v-for="column in columns" :key="column.key" 
          :class="['sticky-header-cell', column.sortable && 'cursor-pointer hover:bg-gray-100']"
          @click="column.sortable && handleSort(column.key)">
        {{ column.label }}
        <!-- Sort indicator -->
      </th>
    </tr>
  </thead>
  <tbody>
    <tr v-for="row in sortedData" class="hover:bg-gray-50 dark:hover:bg-dark-800/30">
      <td v-for="column in columns" :key="column.key" class="px-4 py-3">
        <slot :name="`cell-${column.key}`" :row="row" :value="row[column.key]">
          {{ column.formatter ? column.formatter(row[column.key], row) : row[column.key] }}
        </slot>
      </td>
    </tr>
  </tbody>
</table>
```

**移动端** (Mobile Viewport):
```html
<div v-for="row in sortedData" class="rounded-lg border border-gray-200 bg-white p-4">
  <div v-for="column in dataColumns" class="flex justify-between">
    <span class="text-xs font-medium uppercase text-gray-500">{{ column.label }}</span>
    <div class="text-right text-sm text-gray-900">
      {{ column.formatter ? column.formatter(row[column.key], row) : row[column.key] }}
    </div>
  </div>
</div>
```

### 8.3 Top-N 展现策略

**不分页表格 (Top 10-20)**:
- 简单表格直接显示
- 示例: "Recent Usage" (5 行) → 链接到详情页

**分页表格 (Top 50+)**:
- 默认每页 20-50 行
- 底部 Pagination 组件
- 支持"更多"加载

**虚拟滚动**:
- 用于超大数据集 (1000+)
- @tanstack/vue-virtual 优化渲染性能
- Admin 列表页通常启用

---

## 9. 响应式设计

### 9.1 Tailwind 断点使用

**文件**: `~/refs/sub2api/frontend/tailwind.config.js` (default breakpoints)

```
sm:  640px   (平板竖屏)
md:  768px   (平板横屏)
lg:  1024px  (桌面)
xl:  1280px  (大屏)
2xl: 1536px  (超大屏)
```

### 9.2 Dashboard 响应式示例

**文件**: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:2-6`

```html
<!-- Grid 根据屏幕宽度调整列数 -->
<div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
  <!-- 小屏: 2列  |  大屏: 4列 -->
</div>
```

**文件**: `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardCharts.vue:23`

```html
<div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
  <!-- 小屏: 1列  |  大屏: 2列 -->
</div>
```

### 9.3 Sidebar 响应式

**文件**: `~/refs/sub2api/frontend/src/components/layout/AppLayout.vue:10-12`

```html
<div :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']">
  <!-- 
    小屏 (< lg): margin 为 0 (sidebar 可能隐藏或 overlay)
    大屏 (>= lg): margin 取决于 sidebar 状态
  -->
</div>
```

### 9.4 表格响应式 (DataTable 双模式)

**触发条件**: `isDesktopViewport` computed
- 如果不是桌面视口, 渲染卡片列表
- 否则渲染传统表格

---

## 10. HUAKAI 落地映射表

### 10.1 必须借鉴的架构模式

| Sub2API | HUAKAI (React/Next.js 14) | 说明 |
|---------|---------------------------|------|
| Sidebar + Main Content Layout | 保持相同 | 两栏布局是通用的 |
| Tailwind + CSS @layer | Tailwind + Shadcn/UI / custom CSS | 设计 token 完全移植 |
| Color palette (青色主 + 灰色辅) | 直接使用相同色值 | `#14b8a6` primary, `#64748b` secondary |
| Grid `grid-cols-2 lg:grid-cols-4` | Grid 布局同样概念 | Responsive breakpoints 映射 |
| Card + Shadow system | Card 组件 + Tailwind shadow | 阴影定义完全兼容 |
| Badge / Status Indicator | Badge 组件 (success/warning/error) | 4-5 种状态色 |
| Icon + size variant | Icon 库 (lucide-react / heroicons) | 替换 @lobehub/icons → lucide-react |
| Statistics Grid (4 columns) | 相同 Grid 结构 | 统计卡片组件化 |
| 图表 (chart.js) | recharts 或 chart.js (via npm) | 多线折线图, 甜甜圈图 |
| i18n (vue-i18n) | next-i18n-router / i18next | 支持中英文切换 |

### 10.2 必须避免或改写的 Vue 特定写法

| Vue 模式 | React 替换 | 说明 |
|---------|-----------|------|
| `v-if` / `v-for` | `{条件 && ...}` / `.map()` | React JSX 语法 |
| `v-model` | `useState()` hook | React 状态管理 |
| `@click` / `@change` | `onClick={}` / `onChange={}` | 事件处理 |
| `<RouterLink>` | `<Link>` (Next.js) | Next.js 内置路由 |
| Pinia store | zustand / Redux / Jotai | React 状态库 |
| `useI18n()` composable | `useTranslation()` (next-i18n) | React hook 替换 |
| Template slots | children props | React composition |
| Computed properties | `useMemo()` hook | React 性能优化 |

### 10.3 色彩系统完全映射

**Primary Color**:
```
Vue Tailwind:  primary-500: #14b8a6
React/Next.js: CSS variable / Theme token
--color-primary: #14b8a6
```

**Palette Copy**:
```javascript
// tailwind.config.ts (Next.js)
export const tailwindConfig = {
  theme: {
    extend: {
      colors: {
        primary: {
          50: '#f0fdfa',
          500: '#14b8a6',
          600: '#0d9488',
          // ... 完整复制 primary 色板
        },
        accent: {
          // ... 完整复制 accent 色板
        },
        dark: {
          // ... 完整复制 dark 色板
        }
      },
      // boxShadow / borderRadius / animation 完全相同
    }
  }
}
```

### 10.4 Component 映射表

| Sub2API Component | HUAKAI Equivalent | Notes |
|------------------|------------------|-------|
| `UserDashboardStats.vue` | `<DashboardStats>` | 统计卡片 Grid, 无 Vue 特定语法 |
| `UserDashboardCharts.vue` | `<DashboardCharts>` | 图表容器, recharts 替换 vue-chartjs |
| `DataTable.vue` | `<DataTable>` | 表格组件, 虚拟化可选 |
| `StatusBadge.vue` | `<Badge>` | 状态指示器 |
| `StatCard.vue` | `<StatCard>` | 单个指标卡片 |
| `Input.vue` | `<Input>` (Shadcn) | 输入框组件 |
| 无 (原生 SVG) | lucide-react Icon | 图标库替换 |

### 10.5 Dashboard 页面层级映射

**User Dashboard** (`/dashboard`):
```
UserDashboardPage (Server Component)
├─ DashboardStats (Client Component)
│  ├─ StatCard (4 columns)
│  ├─ StatCard (4 columns)
├─ DashboardCharts (Client Component)
│  ├─ DateRangePicker
│  ├─ DoughnutChart (recharts)
│  ├─ LineChart (recharts, multi-line)
├─ DashboardRecentUsage (Client Component)
│  └─ DataTable
└─ DashboardQuickActions (Client Component)
   └─ Button grid
```

**Admin Dashboard** (`/admin/dashboard`):
```
类似用户 Dashboard，另加运维相关指标
```

### 10.6 关键 File Structure

```
HUAKAI (Round 8 P1 Dashboard)
├─ app/
│  ├─ dashboard/
│  │  └─ page.tsx (User Dashboard)
│  └─ admin/
│     └─ dashboard/
│        └─ page.tsx (Admin Dashboard)
├─ components/
│  ├─ dashboard/
│  │  ├─ DashboardStats.tsx
│  │  ├─ DashboardCharts.tsx
│  │  ├─ DashboardRecentUsage.tsx
│  │  └─ DashboardQuickActions.tsx
│  ├─ common/
│  │  ├─ Card.tsx
│  │  ├─ StatCard.tsx
│  │  ├─ Badge.tsx
│  │  ├─ DataTable.tsx
│  │  └─ Input.tsx
│  └─ layout/
│     ├─ Sidebar.tsx
│     ├─ Header.tsx
│     └─ AppLayout.tsx
├─ lib/
│  ├─ tokens.ts (Design tokens, colors, shadows, etc.)
│  └─ format.ts (格式化函数)
├─ styles/
│  ├─ globals.css (@layer directives)
│  └─ variables.css (CSS variables)
└─ tailwind.config.ts (完全复制 Sub2API 配置)
```

### 10.7 关键实现亮点提取

#### 10.7.1 多线折线图 (Multi-line Chart)

**关键数据**:
- X 轴: 日期 (YYYY-MM-DD)
- Y 轴左: Token 数值 (M/K 单位)
- Y 轴右: 百分比 (0-100%)
- 5 条线: Input, Output, Cache Creation, Cache Read, Cache Hit Rate

**recharts 配置**:
```tsx
<LineChart data={trendData}>
  <CartesianGrid strokeDasharray="3 3" />
  <XAxis dataKey="date" />
  <YAxis yAxisId="left" />
  <YAxis yAxisId="right" orientation="right" domain={[0, 100]} />
  <Tooltip formatter={...} />
  <Line dataKey="input_tokens" yAxisId="left" stroke="#3b82f6" />
  <Line dataKey="output_tokens" yAxisId="left" stroke="#10b981" />
  <Line dataKey="cache_creation_tokens" yAxisId="left" stroke="#f59e0b" />
  <Line dataKey="cache_read_tokens" yAxisId="left" stroke="#06b6d4" />
  <Line dataKey="cache_hit_rate" yAxisId="right" stroke="#8b5cf6" strokeDasharray="5,5" />
</LineChart>
```

#### 10.7.2 响应式表格 (Mobile-first)

**策略**:
- 小屏: `<div className="space-y-3">` 卡片列表
- 大屏: `<table>` 传统表格

```tsx
{isDesktop ? (
  <table>...</table>
) : (
  <div className="space-y-3">
    {data.map(row => (
      <div key={row.id} className="rounded-lg border bg-white p-4">
        {/* 每行数据展示为 label: value 对 */}
      </div>
    ))}
  </div>
)}
```

#### 10.7.3 状态徽章系统

**映射**:
```
success     → 绿色 bg (#10b981)
warning     → 黄色 bg (#f59e0b)
danger      → 红色 bg (#ef4444)
info        → 蓝色 bg (#3b82f6)
```

**实现**:
```tsx
<Badge 
  status={row.status}  // 'active' | 'error' | 'warning'
  variant={statusVariant}
/>
```

#### 10.7.4 暗色模式切换

**Sub2API 做法**: Tailwind `darkMode: 'class'`
- 根类上 `.dark` 切换
- CSS 变量自动响应

**HUAKAI 落地**:
```tsx
// tailwind.config.ts
export default {
  darkMode: 'class',
  // ...
}

// 在根组件
<html className={isDark ? 'dark' : ''}>
```

---

## 11. Reference Links & File Citations

### 11.1 关键文件索引

| 功能区 | 文件路径 | 行数 |
|--------|---------|------|
| 色彩系统 | `~/refs/sub2api/frontend/tailwind.config.js` | 7-49 |
| 全局样式 | `~/refs/sub2api/frontend/src/style.css` | 1-500+ |
| Layout | `~/refs/sub2api/frontend/src/components/layout/AppLayout.vue` | 1-52 |
| Sidebar | `~/refs/sub2api/frontend/src/components/layout/AppSidebar.vue` | 1-100+ |
| 用户 Dashboard | `~/refs/sub2api/frontend/src/views/user/DashboardView.vue` | 1-37 |
| 统计卡片 | `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue` | 1-162 |
| 图表 | `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardCharts.vue` | 1-80 |
| 折线图 | `~/refs/sub2api/frontend/src/components/charts/TokenUsageTrend.vue` | 1-228 |
| 数据表 | `~/refs/sub2api/frontend/src/components/common/DataTable.vue` | 1-200+ |
| 图标 | `~/refs/sub2api/frontend/src/components/icons/Icon.vue` | 1-145 |
| 输入框 | `~/refs/sub2api/frontend/src/components/common/Input.vue` | 1-103 |
| 状态徽章 | `~/refs/sub2api/frontend/src/components/common/StatusBadge.vue` | 1-40 |
| 路由配置 | `~/refs/sub2api/frontend/src/router/index.ts` | 1-862 |
| i18n (English) | `~/refs/sub2api/frontend/src/i18n/locales/en.ts` | 1-100+ |

### 11.2 Design Token 源数据

```
Primary Color: #14b8a6 (Teal 500)
Accent Color: #64748b (Slate 500)
Error: #ef4444 (Red 500)
Success: #10b981 (Emerald 500)
Warning: #f59e0b (Amber 500)

Shadow System:
  - card: 0 1px 3px rgba(0,0,0,0.04)
  - card-hover: 0 10px 40px rgba(0,0,0,0.08)
  - glow: 0 0 20px rgba(20,184,166,0.25)

Border Radius:
  - lg: 8px
  - xl: 12px
  - 2xl: 16px (cards, modals)

Spacing Unit: 4px (Tailwind default)
```

---

## 12. 清洁室注意事项

**本文档的目的**: 提供架构模式、设计决策、组件结构的参考，帮助 HUAKAI Dashboard 重做保持 Sub2API 的视觉风格和交互模式。

**避免的行为**:
- ❌ 不复制 Vue/TypeScript 代码片段到 HUAKAI 代码库
- ❌ 不照搬 Pinia store 的实现细节
- ❌ 不直接使用 vue-chartjs，而用 React 等价物 (recharts)
- ❌ 不照抄 i18n keys 的英文原文（已在文档中作示例）

**推荐的行为**:
- ✅ 提取设计 token (色彩、阴影、间距)，映射到 Tailwind/CSS 变量
- ✅ 研究组件层级和交互流程 (不涉及具体实现)
- ✅ 参考响应式断点策略和移动端适配方式
- ✅ 学习统计卡片、图表、表格的信息架构和排版
- ✅ 为中文 Dashboard 设计合理的 i18n 结构

---

## 附录 A: Sub2API 完整组件清单

```
components/
├─ layout/
│  ├─ AppLayout.vue (★ 核心)
│  ├─ AppSidebar.vue (★ 核心)
│  ├─ AppHeader.vue
│  ├─ TablePageLayout.vue
│  └─ AuthLayout.vue
├─ user/
│  └─ dashboard/
│     ├─ UserDashboardStats.vue (★ 关键)
│     ├─ UserDashboardCharts.vue (★ 关键)
│     ├─ UserDashboardRecentUsage.vue (★ 关键)
│     └─ UserDashboardQuickActions.vue
├─ common/
│  ├─ Card.vue (CSS-only)
│  ├─ DataTable.vue (★ 关键)
│  ├─ Input.vue
│  ├─ StatusBadge.vue (★ 参考)
│  ├─ StatCard.vue
│  ├─ Badge.vue
│  ├─ Button.vue
│  ├─ Select.vue
│  ├─ Pagination.vue
│  ├─ LoadingSpinner.vue
│  ├─ EmptyState.vue
│  ├─ Toast.vue
│  ├─ Modal.vue
│  └─ DateRangePicker.vue
├─ charts/
│  ├─ TokenUsageTrend.vue (★ 关键)
│  └─ ...
├─ icons/
│  └─ Icon.vue (★ 参考, 自实现 SVG)
└─ admin/
   └─ ... (80+ components)
```

---

## 文档版本

- **版本**: 1.0
- **生成日期**: 2026-05-12
- **作者**: Sonnet Explorer (Clean-room Analysis)
- **审阅对象**: HUAKAI Round 8 P1 Dashboard Gemini Dev
- **有效期**: 本轮 Dashboard 重做周期

