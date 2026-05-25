# DEFERRED Hermes Enable Empty Body

Deferred review findings:
- [S2] Hermes enable endpoint rejects empty body while OpenAPI marks the request body optional — source: Codex Round 3 finding #3; rationale: this is an API ergonomics/contract mismatch, not a current Slice 1.1 safety blocker; follow-up: support empty body as managed default when UI or client workflow needs bodyless enable; Owner decision: none for this commit.
