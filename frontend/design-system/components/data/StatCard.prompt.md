A dashboard KPI tile: label, big tabular value, a tone-colored lucide icon chip, optional breakdown line. Lifts on hover.

```jsx
<StatCard
  title="今日 Token 用量"
  value="1,284,500"
  icon={<DatabaseZap className="size-4" />}
  description="输入、输出、缓存合计"
  detail="输入 820,400 / 输出 464,100"
  tone="primary"
/>
```

Tones: `primary` (teal), `blue`, `emerald`, `amber`, `red`, `slate`. Lay them out in a responsive grid (2/3/6 columns).
