A teal-filled action button; use for the primary action in a toolbar, form, or dialog. Pair an outline/ghost variant for secondary actions.

```jsx
<Button onClick={save}>保存</Button>
<Button variant="outline" size="sm"><RefreshCw className="size-4" /> 刷新</Button>
<Button variant="destructive">删除账号</Button>
```

Variants: `default` (teal), `destructive` (red), `outline`, `secondary`, `ghost`, `link`.
Sizes: `sm` (36px), `md` (40px, default), `lg` (44px), `icon` (40×40 square — pass a single icon as children).
Icons are lucide glyphs sized `size-4` (16px); place before the label with the built-in gap.
