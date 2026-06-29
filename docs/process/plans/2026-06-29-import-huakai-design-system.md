# 2026-06-29 import-huakai-design-system
| Owner directive | "帮我将里面的内容传到github上的huakai 路劲" |
| Scope | Extract `C:\Users\h\Downloads\HUAKAI Design System.zip` into the HUAKAI repo, preserve the archive structure under a single frontend-owned directory, then commit and push to the existing tracked branch. |
| Success criteria | All archive files exist in the repo under one isolated path, `git status` shows only the intended additions, and the branch push to `origin` succeeds. |
| Time estimate | 10-15 minutes |
| Blast radius | Low to medium. Adds a new frontend asset subtree and one plan file; no backend, billing, auth, schema, or deployment files change. |
| Failure modes | Wrong destination path pollutes the repo root; mitigated by nesting under a dedicated directory. Push failure due to auth or remote state; mitigated by checking branch/remote state before push. Archive extraction collisions; mitigated by choosing a new directory with no existing files. |
| Decision points | Confirm destination path and branch if the Owner wants something other than the recommended `frontend/design-system/` on `claude/phase-1`. |
| Pre-execution checklist | 1. Verify the local repo and remote. 2. Verify the archive contents. 3. Extract into `frontend/design-system/`. 4. Review `git status`. 5. Commit with an explicit message. 6. Push the tracked branch to `origin`. |

## Working assumption

Unless the Owner redirects it, the archive contents will be added under:

`frontend/design-system/`

This keeps the imported design-system files isolated from the existing frontend app while making them easy to browse on GitHub.
