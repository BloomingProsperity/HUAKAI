#!/usr/bin/env python3
"""Multi-AI parallel-edit coordination core (see .coordination/README.md).

Per-agent lock files in locks/<agent>.json avoid the meta-collision of a single
shared status file. Subcommands: claim | check | release.
"""
import sys, os, json, re, datetime, urllib.request, urllib.error, urllib.parse, ssl, posixpath, subprocess


def _norm_files(items):
    """Match the server's norm_files so a conflict check on './backend/x.go' or
    'backend//x.go' still collides with a stored 'backend/x.go'."""
    out = set()
    for f in items or []:
        f = (f or "").strip().replace("\\", "/")
        if f.startswith("./"):
            f = f[2:]
        f = posixpath.normpath(f).lstrip("/")
        if f and f != ".":
            out.add(f)
    return out

DIR = os.path.dirname(os.path.abspath(__file__))

# --- Remote mode: when COORD_URL is set, talk to the cross-machine coord-server
# (see server/coord_server.py) instead of local files. Same CLI either way, so the
# 3 machines (1 local + 2 servers) share ONE live board. COORD_TOKEN must match.
COORD_URL = os.environ.get("COORD_URL", "").rstrip("/")
COORD_TOKEN = os.environ.get("COORD_TOKEN", "")
# COORD_CACERT pins the server's (self-signed) cert for cross-machine TLS. When the
# URL is https and a CA cert is provided, verify against ONLY it (cert pinning).
COORD_CACERT = os.environ.get("COORD_CACERT", "")
_SSLCTX = None
if COORD_URL.startswith("https") and COORD_CACERT:
    _SSLCTX = ssl.create_default_context(cafile=COORD_CACERT)


def _http(method, path, body=None):
    req = urllib.request.Request(COORD_URL + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + COORD_TOKEN,
                                          "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=10, context=_SSLCTX) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except Exception:
            return e.code, {"error": str(e)}
    except Exception as e:
        # network/TLS/timeout error -> code 0 so callers fail CLOSED (cannot verify)
        return 0, {"error": str(e)}


def remote_claim(agent, files_csv, feature, purpose):
    files = [x.strip() for x in files_csv.split(",") if x.strip()]
    # L5: pass the session id so the server can refuse a SECOND live session of the same
    # agent (one agent = one editing session). Empty when COORD_SESSION is unset -> the
    # server keeps the legacy single-session behaviour, so this is backward-compatible.
    code, obj = _http("POST", "/claim", {"agent": agent, "files": files,
                                         "core_feature": feature, "purpose": purpose,
                                         "session": os.environ.get("COORD_SESSION", "")})
    if code == 200:
        print(f"✓ claimed {files} for [{feature}] — {purpose}")
        return 0
    if code == 409:
        print("⚠️  CONFLICT — claim REFUSED. Already being edited by another live agent:")
        for c in obj.get("conflicts", []):
            print(f"    - {c.get('agent')} overlaps: {', '.join(c.get('files', []))}")
        print("    Per protocol: do NOT overwrite; pick other work / wait / coordinate.")
        return 2
    print(f"server error {code}: {obj}")
    return 1


def remote_check(target):
    if target:
        code, obj = _http("GET", "/check?file=" + urllib.parse.quote(target))
        if code != 200:
            # FAIL CLOSED: the pre-edit safety gate must NOT report "free" when it
            # could not verify (bad token / server down / network). Treat as do-not-edit.
            print(f"⚠️  cannot verify {target}: coord error {code}: {obj.get('error', obj)} "
                  f"— treat as DO-NOT-EDIT until resolved", file=sys.stderr)
            return 3
        hits = obj.get("conflicts", [])
        if hits:
            print(f"⚠️  {target} is being edited:")
            for l in hits:
                print(f"    - {l.get('agent')} | {l.get('core_feature')} | {l.get('purpose')}")
            return 2
        print(f"✓ {target} is free (no live lock)")
        return 0
    code, obj = _http("GET", "/board")
    if code != 200:
        print(f"⚠️  cannot reach coord server (error {code}: {obj.get('error', obj)})", file=sys.stderr)
        return 3
    live = obj.get("locks", [])
    if not live:
        print("(no live edits)"); return 0
    print("ACTIVE EDITS (live locks, shared across all machines):")
    for l in live:
        print(f"  ● {l.get('agent')}  [{l.get('core_feature')}]  {l.get('purpose')}")
        for fp in l.get("files", []):
            print(f"      {fp}")
    return 0


def remote_release(agent):
    code, obj = _http("POST", "/release", {"agent": agent})
    if code != 200:
        # Surface failure: the agent must NOT believe it released when it did not
        # (the lock then holds until TTL and blocks others / itself).
        print(f"⚠️  release may have FAILED (server {code}: {obj.get('error', obj)}); "
              f"lock holds until TTL — retry or fix token/URL", file=sys.stderr)
        return 1
    print(f"✓ released locks for {agent}")
    return 0


# --- Task-dispatch client (remote only — the backlog is a shared ledger, not a
# per-machine file). Workers: mine/start/review/block. Dispatcher: assign/pass/
# bounce/park/load. assign goes through the server's verify_rounds>=3 gate. ---
def _need_remote():
    if not COORD_URL:
        print("task commands need COORD_URL (shared ledger lives on the coord server).", file=sys.stderr)
        return False
    return True


def _agent():
    return os.environ.get("COORD_AGENT", "")


def remote_tasks(status=None, assignee=None, wave=None):
    if not _need_remote():
        return 1
    qs = []
    if status:
        qs.append("status=" + urllib.parse.quote(status))
    if assignee:
        qs.append("assignee=" + urllib.parse.quote(assignee))
    if wave:
        qs.append("wave=" + urllib.parse.quote(wave))
    code, obj = _http("GET", "/tasks" + ("?" + "&".join(qs) if qs else ""))
    if code != 200:
        print(f"⚠️  cannot read tasks (error {code}: {obj.get('error', obj)})", file=sys.stderr)
        return 3
    ts = obj.get("tasks", [])
    if not ts:
        print("(no tasks)")
        return 0
    for t in ts:
        v = "✓3" if t.get("verify_rounds", 0) >= 3 else f"⚠{t.get('verify_rounds', 0)}/3"
        risk = f" !{t['risk']}" if t.get("risk") else ""
        print(f"  [{t.get('status', ''):<11}] {t.get('id', ''):<10} {t.get('wave', '')}  "
              f"{t.get('title', '')}  → {t.get('assignee') or '(unassigned)'}  ({v}{risk})")
        if t.get("acceptance"):
            print(f"        ✔ DoD: {t['acceptance']}")
        if t.get("spec_refs"):
            print(f"        📚 specs: {', '.join(t['spec_refs'])}")
        if t.get("scope_files"):
            print(f"        📁 files: {', '.join(t['scope_files'])}")
        if t.get("review_notes"):
            print(f"        ↩ review/owner: {t['review_notes']}")
    return 0


def _get_task(tid):
    code, obj = _http("GET", "/tasks")
    if code != 200:
        return None
    for t in obj.get("tasks", []):
        if t.get("id") == tid:
            return t
    return None


def remote_task_status(tid, status, **extra):
    if not _need_remote():
        return 1
    body = {"id": tid, "status": status}
    # L4: carry the actor identity so the server can enforce its actor/role matrix
    # (assignee-only progress, dispatcher-only verdicts, reviewer != assignee). Falls
    # back to COORD_AGENT; the server treats an empty actor as "identity unknown" and
    # degrades to legacy behaviour, so this stays backward-compatible for old callers.
    ag = _agent()
    if ag and "actor" not in extra:
        body["actor"] = ag
    body.update({k: v for k, v in extra.items() if v is not None})
    code, obj = _http("POST", "/tasks/status", body)
    if code == 200:
        print(f"✓ {tid} → {status}")
        return 0
    print(f"server error {code}: {obj.get('error', obj)}", file=sys.stderr)
    return 1


def remote_task_start(tid):
    if not _need_remote():
        return 1
    ag = _agent()
    if not ag:
        print("set COORD_AGENT first", file=sys.stderr)
        return 1
    t = _get_task(tid)
    if not t:
        print(f"no such task {tid}", file=sys.stderr)
        return 1
    if t.get("assignee") and t["assignee"] != ag:
        print(f"⚠️  {tid} is assigned to {t['assignee']}, not you ({ag}); not starting.", file=sys.stderr)
        return 2
    # Claim the task's scope files first (true atomic file lock) — refuse on conflict.
    files = t.get("scope_files", [])
    claimed = False
    if files:
        code, obj = _http("POST", "/claim", {"agent": ag, "files": files,
                          "core_feature": t.get("feature") or tid, "purpose": "task " + tid,
                          "session": os.environ.get("COORD_SESSION", "")})
        if code == 409:
            print("⚠️  cannot start — files already locked by another agent:")
            for c in obj.get("conflicts", []):
                print(f"    - {c.get('agent')} overlaps: {', '.join(c.get('files', []))}")
            return 2
        if code != 200:
            print(f"claim error {code}: {obj.get('error', obj)}", file=sys.stderr)
            return 1
        claimed = True
    rc = remote_task_status(tid, "in_progress", updated_by=ag)
    if rc != 0 and claimed:
        # The status flip failed (e.g. transition/verify gate) — release the file
        # lock we just took so we don't block other workers on a task that didn't start.
        _http("POST", "/release", {"agent": ag})
        print(f"  (rolled back file lock for {ag} — start did not complete)", file=sys.stderr)
    return rc


def remote_task_assign(tid, agent, verify_rounds, verify_notes=""):
    if not _need_remote():
        return 1
    try:
        vr = int(verify_rounds)
    except (TypeError, ValueError):
        print("verify_rounds must be an integer (3 = passed 3 self-verify rounds)", file=sys.stderr)
        return 1
    code, obj = _http("POST", "/tasks", {"id": tid, "assignee": agent, "status": "assigned",
                      "verify_rounds": vr, "verify_notes": verify_notes,
                      "updated_by": _agent() or "dispatcher"})
    if code == 200:
        print(f"✓ assigned {tid} → {agent} (verify_rounds={vr})")
        return 0
    if code == 422:
        print(f"⛔ refused: {obj.get('error')} — 分配前必须 3 轮自反验证", file=sys.stderr)
        return 2
    print(f"server error {code}: {obj.get('error', obj)}", file=sys.stderr)
    return 1


def remote_task_load(path):
    if not _need_remote():
        return 1
    try:
        items = json.load(open(path))
    except Exception as e:
        print(f"cannot read {path}: {e}", file=sys.stderr)
        return 1
    if isinstance(items, dict):
        items = items.get("tasks", [])
    ok = 0
    for it in items:
        it.setdefault("updated_by", _agent() or "dispatcher")
        code, obj = _http("POST", "/tasks", it)
        if code == 200:
            ok += 1
        else:
            print(f"  ✗ {it.get('id')}: {code} {obj.get('error', obj)}", file=sys.stderr)
    print(f"loaded {ok}/{len(items)} tasks")
    return 0 if ok == len(items) else 1


def remote_heartbeat(agent):
    if not _need_remote():
        return 1
    code, _ = _http("POST", "/heartbeat", {"agent": agent})
    return 0 if code == 200 else 1


def remote_task_show(tid):
    if not _need_remote():
        return 1
    t = _get_task(tid)
    if not t:
        print(f"no such task {tid}", file=sys.stderr)
        return 1
    print(f"{t['id']}  [{t.get('status')}]  {t.get('wave', '')}  risk={t.get('risk') or '-'}  "
          f"verify={t.get('verify_rounds', 0)}/3")
    print(f"  title:      {t.get('title', '')}")
    if t.get('detail'):
        print(f"  detail:     {t['detail']}")
    print(f"  assignee:   {t.get('assignee') or '(unassigned)'}")
    print(f"  acceptance: {t.get('acceptance') or '(none)'}")
    print(f"  spec_refs:  {', '.join(t.get('spec_refs', [])) or '(none)'}")
    print(f"  files:      {', '.join(t.get('scope_files', [])) or '(none)'}")
    if t.get('review_notes'):
        print(f"  review/owner: {t['review_notes']}")
    if t.get('notes'):
        print(f"  notes:      {t['notes']}")
    return 0


def remote_task_approve(tid):
    """Owner-only: approve a parked (needs_owner) task to done. Requires the
    separate COORD_OWNER_TOKEN secret — workers/dispatcher cannot do this."""
    if not _need_remote():
        return 1
    ot = os.environ.get("COORD_OWNER_TOKEN", "")
    if not ot:
        print("set COORD_OWNER_TOKEN (Owner-only secret) to approve high-risk tasks", file=sys.stderr)
        return 1
    code, obj = _http("POST", "/tasks/status", {"id": tid, "status": "done",
                      "owner_token": ot, "reviewed_by": "owner"})
    if code == 200:
        print(f"✓ Owner approved {tid} → done")
        return 0
    print(f"approve failed {code}: {obj.get('error', obj)}", file=sys.stderr)
    return 1


# Landing base the unmerged work/* branches are diffed against (L7). Mirrors the
# worker-loop self-update branch; override with COORD_LANDING_BASE if the landing
# branch ever changes so the conflict scan keeps diffing against the right base.
LANDING_BASE = os.environ.get("COORD_LANDING_BASE",
                              os.environ.get("COORD_UPDATE_BRANCH", "fix/hermes-phase-1-e33d940"))


def _git(*args):
    """Best-effort git in the repo root (.coordination/..). Returns (rc, stdout) and
    NEVER raises — a missing git / no-network / not-a-repo environment must fail soft so
    the conflicts pre-check can't crash or hang the dispatcher's assign flow (L7)."""
    repo = os.path.dirname(DIR)
    try:
        p = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True, timeout=20)
        return p.returncode, p.stdout
    except Exception:
        return 1, ""


def _branch_conflicts(want):
    """L7: an unmerged work/* branch still 'owns' every file it changed, even after its
    ledger task has left the non-terminal set (e.g. done-but-not-yet-merged, or files it
    touched OUTSIDE its declared scope_files). For each origin/work/* head, diff its real
    changed files vs the landing base and intersect with the candidate scope. Best-effort:
    a fetch/diff failure prints a soft warning and is skipped — it never blocks assigning."""
    hits = []
    rc, out = _git("ls-remote", "--heads", "origin", "work/*")
    if rc != 0 or not out.strip():
        return hits  # no remote work branches visible (or git/network unavailable) -> soft
    # Make sure the landing base + the work heads are locally resolvable for the diff.
    _git("fetch", "-q", "origin", LANDING_BASE)
    branches = []
    for line in out.splitlines():
        parts = line.split("\t")
        if len(parts) == 2 and parts[1].startswith("refs/heads/"):
            branches.append(parts[1][len("refs/heads/"):])  # e.g. work/s2-048
    for br in branches:
        _git("fetch", "-q", "origin", br)
        rc, diff = _git("diff", "--name-only", f"origin/{LANDING_BASE}...origin/{br}")
        if rc != 0:
            print(f"    (note: could not diff origin/{br} vs {LANDING_BASE} — skipped)", file=sys.stderr)
            continue
        changed = _norm_files(diff.splitlines())
        ov = want & changed
        if ov:
            hits.append((br, sorted(ov)))
    return hits


def remote_task_conflicts(files_csv):
    """Q3 guard: does a candidate scope overlap any NON-TERMINAL task — including
    parked (needs_owner) or blocked work that stopped mid-edit — OR the real changed
    files of any unmerged origin/work/* branch (L7)? Use before assigning."""
    if not _need_remote():
        return 1
    want = _norm_files(files_csv.split(","))
    code, obj = _http("GET", "/tasks")
    if code != 200:
        print(f"⚠️  cannot read tasks ({code})", file=sys.stderr)
        return 3
    nonterminal = {"assigned", "in_progress", "review", "needs_owner", "blocked"}
    hits = []
    for t in obj.get("tasks", []):
        if t.get("status") in nonterminal:
            ov = want & _norm_files(t.get("scope_files", []))
            if ov:
                hits.append((t["id"], t.get("status"), t.get("assignee"), sorted(ov)))
    branch_hits = _branch_conflicts(want)
    if not hits and not branch_hits:
        print("✓ no scope conflict with any active/parked/blocked task or unmerged work branch")
        return 0
    if hits:
        print("⚠️  scope overlaps existing non-terminal tasks (incl. parked/blocked — may resume):")
        for i, s, a, ov in hits:
            print(f"    - {i} [{s}] {a or '-'} overlaps: {', '.join(ov)}")
    if branch_hits:
        print("⚠️  scope overlaps UNMERGED work branches (finished-but-unmerged still owns its files):")
        for br, ov in branch_hits:
            print(f"    - origin/{br} changed: {', '.join(ov)}")
    return 2


LOCKS = os.path.join(DIR, "locks")
LOG = os.path.join(DIR, "activity.log")
DEFAULT_TTL = 1800


def now_iso():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def parse_iso(s):
    try:
        return datetime.datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=datetime.timezone.utc)
    except Exception:
        return None


def safe(name):
    return re.sub(r"[^A-Za-z0-9._-]", "_", name)


def load_locks():
    out = []
    if not os.path.isdir(LOCKS):
        return out
    for fn in sorted(os.listdir(LOCKS)):
        if not fn.endswith(".json"):
            continue
        try:
            with open(os.path.join(LOCKS, fn)) as f:
                d = json.load(f)
            d["_file"] = fn
            out.append(d)
        except Exception:
            pass  # tolerate a half-written peer lock; never crash the board
    return out


def is_stale(lock, ref):
    hb = parse_iso(lock.get("heartbeat_at", "")) or parse_iso(lock.get("started_at", ""))
    ttl = lock.get("ttl_seconds", DEFAULT_TTL)
    if hb is None:
        return False
    return (ref - hb).total_seconds() > ttl


def log_event(event):
    try:
        with open(LOG, "a") as f:
            f.write(json.dumps(event, ensure_ascii=False) + "\n")
    except Exception:
        pass  # best-effort broadcast; never block on the log


def cmd_claim(agent, files_csv, feature, purpose):
    os.makedirs(LOCKS, exist_ok=True)
    files = [x.strip() for x in files_csv.split(",") if x.strip()]
    ref = datetime.datetime.now(datetime.timezone.utc)
    # conflict check vs OTHER live editing locks
    conflicts = []
    for lk in load_locks():
        if lk.get("agent") == agent:
            continue
        if lk.get("status") != "editing" or is_stale(lk, ref):
            continue
        overlap = set(files) & set(lk.get("files", []))
        if overlap:
            conflicts.append((lk.get("agent"), lk.get("core_feature"), sorted(overlap)))
    if conflicts:
        # Refuse the claim outright — do NOT write a second lock on a file another
        # live agent holds. The caller must resolve before editing.
        log_event({"ts": now_iso(), "event": "claim_blocked", "agent": agent,
                   "files": files, "core_feature": feature,
                   "conflicts": [{"agent": a, "feature": f, "files": o} for a, f, o in conflicts]})
        print("⚠️  CONFLICT — claim REFUSED. These files are already being edited by another live agent:")
        for ag, feat, ov in conflicts:
            print(f"    - {ag} (feature: {feat}) overlaps: {', '.join(ov)}")
        print("    Per protocol: do NOT overwrite; pick other work / wait for done / coordinate.")
        return 2
    path = os.path.join(LOCKS, safe(agent) + ".json")
    started = now_iso()
    if os.path.exists(path):
        try:
            started = json.load(open(path)).get("started_at", started)
        except Exception:
            pass
    lock = {
        "agent": agent,
        "session": os.environ.get("COORD_SESSION", ""),
        "status": "editing",
        "files": files,
        "core_feature": feature,
        "purpose": purpose,
        "started_at": started,
        "heartbeat_at": now_iso(),
        "ttl_seconds": DEFAULT_TTL,
    }
    with open(path, "w") as f:
        json.dump(lock, f, ensure_ascii=False, indent=2)
    log_event({"ts": now_iso(), "event": "claim", "agent": agent, "files": files,
               "core_feature": feature, "purpose": purpose})
    print(f"✓ claimed {files} for [{feature}] — {purpose}")
    return 0


def cmd_check(target=None):
    ref = datetime.datetime.now(datetime.timezone.utc)
    locks = load_locks()
    live = [l for l in locks if l.get("status") == "editing" and not is_stale(l, ref)]
    if target:
        hits = [l for l in live if target in l.get("files", [])]
        if hits:
            print(f"⚠️  {target} is being edited:")
            for l in hits:
                print(f"    - {l.get('agent')} | {l.get('core_feature')} | {l.get('purpose')}")
            return 2
        print(f"✓ {target} is free (no live lock)")
        return 0
    if not live:
        print("(no live edits)")
        return 0
    print("ACTIVE EDITS (live locks):")
    for l in live:
        print(f"  ● {l.get('agent')}  [{l.get('core_feature')}]  {l.get('purpose')}")
        for fp in l.get("files", []):
            print(f"      {fp}")
    stale = [l for l in locks if l not in live and l.get("status") == "editing"]
    if stale:
        print(f"(+{len(stale)} stale lock(s) ignored — heartbeat past ttl)")
    return 0


def cmd_release(agent):
    path = os.path.join(LOCKS, safe(agent) + ".json")
    feat = ""
    if os.path.exists(path):
        try:
            feat = json.load(open(path)).get("core_feature", "")
        except Exception:
            pass
        os.remove(path)
    log_event({"ts": now_iso(), "event": "release", "agent": agent, "core_feature": feat})
    print(f"✓ released locks for {agent}")
    return 0


def main(argv):
    if len(argv) < 2:
        print("usage: coord.py {claim|check|release} ...", file=sys.stderr)
        return 1
    cmd = argv[1]
    remote = bool(COORD_URL)
    if cmd == "claim":
        if len(argv) < 4:
            print("usage: coord.py claim <agent> <comma-files> [core_feature] [purpose]", file=sys.stderr)
            return 1
        a = (argv[2], argv[3], argv[4] if len(argv) > 4 else "", argv[5] if len(argv) > 5 else "")
        return remote_claim(*a) if remote else cmd_claim(*a)
    if cmd == "check":
        t = argv[2] if len(argv) > 2 else None
        return remote_check(t) if remote else cmd_check(t)
    if cmd == "release":
        if len(argv) < 3:
            print("usage: coord.py release <agent>", file=sys.stderr)
            return 1
        return remote_release(argv[2]) if remote else cmd_release(argv[2])
    # --- task-dispatch verbs (remote ledger) ---
    if cmd == "tasks":
        status = assignee = wave = None
        rest = argv[2:]
        if rest and rest[0] == "mine":
            assignee = _agent()
        else:
            for x in rest:
                if x.startswith("status="):
                    status = x.split("=", 1)[1]
                elif x.startswith("wave="):
                    wave = x.split("=", 1)[1]
                elif x.startswith("assignee="):
                    assignee = x.split("=", 1)[1]
        return remote_tasks(status, assignee, wave)
    if cmd == "task-start":
        return remote_task_start(argv[2]) if len(argv) > 2 else 1
    if cmd == "task-review":
        rc = remote_task_status(argv[2], "review", updated_by=_agent(),
                                notes=(argv[3] if len(argv) > 3 else None))
        if rc == 0 and COORD_URL and _agent():
            remote_release(_agent())  # editing done -> free the scope files for others
        return rc
    if cmd == "task-block":
        rc = remote_task_status(argv[2], "blocked", updated_by=_agent(),
                                notes=(argv[3] if len(argv) > 3 else None))
        if rc == 0 and COORD_URL and _agent():
            remote_release(_agent())  # paused -> free the files (scope still tracked on the task)
        return rc
    if cmd == "task-show":
        return remote_task_show(argv[2]) if len(argv) > 2 else 1
    if cmd == "task-approve":
        return remote_task_approve(argv[2]) if len(argv) > 2 else 1
    if cmd == "task-conflicts":
        return remote_task_conflicts(argv[2]) if len(argv) > 2 else 1
    if cmd == "heartbeat":
        return remote_heartbeat(argv[2] if len(argv) > 2 else _agent())
    if cmd == "task-assign":
        if len(argv) < 5:
            print("usage: coord.py task-assign <id> <agent> <verify_rounds> [verify_notes]", file=sys.stderr)
            return 1
        return remote_task_assign(argv[2], argv[3], argv[4], argv[5] if len(argv) > 5 else "")
    if cmd == "task-pass":
        return remote_task_status(argv[2], "done", reviewed_by=(_agent() or "dispatcher"),
                                  review_notes=(argv[3] if len(argv) > 3 else None))
    if cmd == "task-bounce":
        if len(argv) < 4:
            print("usage: coord.py task-bounce <id> <review_notes>", file=sys.stderr)
            return 1
        return remote_task_status(argv[2], "assigned", reviewed_by=(_agent() or "dispatcher"),
                                  review_notes=argv[3])
    if cmd == "task-park":
        rc = remote_task_status(argv[2], "needs_owner", reviewed_by=(_agent() or "dispatcher"),
                                review_notes=(argv[3] if len(argv) > 3 else None))
        if rc == 0 and COORD_URL and _agent():
            remote_release(_agent())  # parked & moving on -> free the files (scope tracked on task)
        return rc
    if cmd == "task-load":
        return remote_task_load(argv[2]) if len(argv) > 2 else 1
    print(f"unknown subcommand: {cmd}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
