A rounded pill for status and labels; the console uses it for account health (健康/降级/失败) and schedule state.

```jsx
<Badge variant="success">健康</Badge>
<Badge variant="warning">冷却中</Badge>
<Badge variant="destructive">失败</Badge>
<Badge variant="outline">可调度</Badge>
```

Tones: `default` (teal), `secondary` (slate), `destructive` (red), `outline`, `success` (emerald), `warning` (amber), `info` (blue). Convention: operational→success, degraded/cooling→warning, failed/error→destructive.
