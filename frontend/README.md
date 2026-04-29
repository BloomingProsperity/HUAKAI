# HUAKAI Frontend

This directory will hold the TypeScript admin UI per [DR-003](../docs/decisions/DR-003-technology-stack.md) (Go backend + TypeScript frontend with types generated from the backend's OpenAPI contract).

## Status

**Empty placeholder.** Frontend implementation is Gemini's domain per [GEMINI.md](../GEMINI.md). Phase 7 admin UI starts here.

## Planned structure (Phase 7+)

```
frontend/
  package.json
  tsconfig.json
  vite.config.ts          # per DR-005 (React + Vite)
  src/
    api/                  # codegen from ../backend (or ../docs/openapi/openapi.yaml)
    pages/
      pools/              # F-POOL-001 admin UI
      provider-accounts/  # F-POOL-001 + F-AUTH-005 + F-RATE-001
      usage/              # F-OBS-001
      billing/
      audit/
    components/
    lib/
  public/
```

## Contract source

`docs/openapi/openapi.yaml` is the locked HTTP contract. Frontend codegen consumes it to produce typed API clients.
