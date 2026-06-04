# Migration Numbering Notes

This directory is append-only. Do not rewrite, renumber, or reuse an issued
migration number after files have landed.

## Reserved Gaps

- `0080` is intentionally skipped. Do not reuse `0080` for future `.up.sql` or
  `.down.sql` files; continue with the next unused number.
