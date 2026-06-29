The console's primary surface. Compose the sub-parts; set `interactive` for clickable cards.

```jsx
<Card>
  <CardHeader>
    <CardTitle><Layers className="size-4" /> Top 5 供应商账号</CardTitle>
    <CardDescription>实时账号池健康</CardDescription>
  </CardHeader>
  <CardContent>…</CardContent>
</Card>
```

Default padding is 20px (`--space-5`). CardTitle is a flex row so a lucide icon sits inline. Use `interactive` only for nav/entry cards.
