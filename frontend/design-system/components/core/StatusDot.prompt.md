A glowing status dot with a soft halo ring; sits inside heartbeat chips and account rows.

```jsx
<span style={{display:'flex',alignItems:'center',gap:8}}>
  <StatusDot tone="online" /> 已连接
</span>
<StatusDot tone="live" pulse />
```

Tones: `online` (emerald), `offline` (red), `pending` (amber), `idle` (slate), `live` (teal). Use `pulse` for active polling.
