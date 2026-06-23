# Migration Numbering Notes

This directory is append-only. Do not rewrite, renumber, or reuse an issued
migration number after files have landed.

## Reserved Gaps

- `0080` is intentionally skipped. Do not reuse `0080` for future `.up.sql` or
  `.down.sql` files; continue with the next unused number.
- `0148` is also a gap: the on-disk sequence jumps from `0147_provider_account_rpm_tpm_limit`
  straight to `0149_account_proxy_mutual_exclusive` (no `0148` file exists; verified by
  directory listing — highest issued number is `0151_media_task_orphans`). Do not reuse
  `0148`; continue with the next unused number.
