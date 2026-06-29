Data table for account pools, usage records, audit rows. Uppercase muted header, hairline rows, hover highlight.

```jsx
<Table>
  <THead><TR hover={false}>
    <TH>账号</TH><TH>供应商</TH><TH>健康状态</TH><TH>并发</TH>
  </TR></THead>
  <TBody>
    <TR>
      <TD>claude-pool-01</TD>
      <TD>Anthropic</TD>
      <TD><Badge variant="success">健康</Badge></TD>
      <TD mono>3/10</TD>
    </TR>
  </TBody>
</Table>
```

Use `mono` on TD for IDs, concurrency (3/10), latencies, timestamps. Drop Badge into status cells.
