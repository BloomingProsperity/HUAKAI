#!/usr/bin/env python3
"""HUAKAI multi-AI edit-coordination service (cross-machine).

One always-on instance on ONE server; the local machine + the other server talk to
it over an encrypted private link (Tailscale/WireGuard) or TLS, with a shared bearer
token. SQLite gives TRUE atomic claims (BEGIN IMMEDIATE serializes concurrent claims)
— stronger than the local-file advisory mode.

Zero third-party deps (Python 3 stdlib only). Endpoints (all require
Authorization: Bearer <COORD_TOKEN>):

  GET  /board                      -> {"locks":[...]}            list live locks
  GET  /check?file=<path>          -> {"file":..,"conflicts":[...]}
  POST /claim    {agent,files[],core_feature,purpose}
                                   -> 200 {"ok":true,...} | 409 {"ok":false,"conflicts":[...]}
  POST /heartbeat{agent}           -> refresh ttl
  POST /release  {agent}           -> drop the agent's lock
  GET  /healthz                    -> ok (no auth)
  GET  /  /view                    -> board viewer HTML (no auth; data via token)
  GET  /tree                       -> feature-tree dashboard HTML (no auth; data via token)
  GET  /tree/data                  -> feature-tree.json (token-gated)
  GET  /dispatch                   -> task-dispatch board HTML (no auth; data via token)
  GET  /tasks[?status=&assignee=&wave=] -> {"tasks":[...]}            (token-gated)
  POST /tasks    {id,title,...,verify_rounds,assignee,status}   upsert/assign
                                   -> 422 unless verify_rounds>=3 to assign
  POST /tasks/status {id,status,notes?,review_notes?,reviewed_by?}  transition
  POST /dispatcher/output {agent,lines:[str,...]}  append AI stdout to live terminal
                                   -> {"ok":true,"last_id":N}
  GET  /dispatcher/output?agent=&since=<id>  incremental tail for the live terminal
                                   -> {"lines":[{id,ts,line}],"last_id":N}

Env: COORD_TOKEN (required, shared secret), COORD_DB (default coord.db),
     COORD_BIND (default 127.0.0.1), COORD_PORT (default 8787), COORD_TTL (default 1800).
"""
import os, json, sqlite3, time, datetime, threading, ssl, posixpath
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs


def norm_files(files):
    """Normalize file paths so the SAME file claimed under different spellings
    ('backend/x.go' vs './backend/x.go' vs 'backend//x.go' vs trailing-slash)
    collides. Agents MUST pass repo-relative paths; we also strip a leading '/'
    so an accidental absolute-ish path still collapses toward the relative form."""
    out = set()
    for f in files or []:
        f = (f or "").strip().replace("\\", "/")
        if f.startswith("./"):
            f = f[2:]
        f = posixpath.normpath(f).lstrip("/")
        if f and f != ".":
            out.add(f)
    return sorted(out)

TOKEN = os.environ.get("COORD_TOKEN", "")
# Separate Owner-only secret. All machines share COORD_TOKEN, so it cannot prove
# "the Owner" — high-risk approval (needs_owner -> done) requires THIS instead.
# Unset = fail-closed: parked high-risk tasks cannot be closed until the Owner sets it.
OWNER_TOKEN = os.environ.get("COORD_OWNER_TOKEN", "")
DB = os.environ.get("COORD_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "coord.db"))
BIND = os.environ.get("COORD_BIND", "127.0.0.1")
PORT = int(os.environ.get("COORD_PORT", "8787"))
TTL = int(os.environ.get("COORD_TTL", "1800"))
# Optional TLS: set both to serve HTTPS on a public bind (cross-machine mode).
# Unset = plain HTTP (fine for 127.0.0.1 / private-network binds).
TLS_CERT = os.environ.get("COORD_TLS_CERT", "")
TLS_KEY = os.environ.get("COORD_TLS_KEY", "")
# Feature-tree dashboard hosting. Defaults sit in a tree/ dir next to this script
# (on the box: /opt/huakai-coord/tree/), so deploying needs only a file copy — no
# env/config edit. Override via env if the files live elsewhere. Missing files →
# /tree and /tree/data return 404 cleanly. GET /tree serves the HTML shell (public,
# carries no project data); GET /tree/data serves the JSON (token-gated — it reveals
# which audit findings are still open, so it stays behind the bearer token like /board).
_HERE = os.path.dirname(os.path.abspath(__file__))
TREE_HTML = os.environ.get("COORD_TREE_HTML", os.path.join(_HERE, "tree", "feature-tree.html"))
TREE_JSON = os.environ.get("COORD_TREE_JSON", os.path.join(_HERE, "tree", "feature-tree.json"))
_lock = threading.Lock()


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def db():
    c = sqlite3.connect(DB, timeout=10)
    c.execute("""CREATE TABLE IF NOT EXISTS locks(
        agent TEXT PRIMARY KEY, files TEXT, core_feature TEXT, purpose TEXT,
        started_at TEXT, heartbeat_at TEXT, ttl INTEGER, epoch REAL)""")
    # Task-dispatch ledger: the PM/commander (Claude) writes allocations here; the
    # three machines pull their assigned work. An allocation may NOT be assigned
    # until it has passed 3 self-refute + self-verify rounds (verify_rounds>=3) —
    # enforced in upsert_task(), per Owner rule "分配的人必须 review 三遍".
    c.execute("""CREATE TABLE IF NOT EXISTS tasks(
        id TEXT PRIMARY KEY, title TEXT, detail TEXT, wave TEXT, feature TEXT,
        spec_refs TEXT, acceptance TEXT,
        scope_files TEXT, assignee TEXT, status TEXT, priority INTEGER, risk TEXT,
        verify_rounds INTEGER, verify_notes TEXT, notes TEXT,
        review_notes TEXT, reviewed_by TEXT,
        updated_by TEXT, created_at TEXT, updated_at TEXT)""")
    # Dispatcher daemon self-report (drives the /console panel so the Owner never
    # needs a shell to see what the 24/7 dispatcher is doing): one row per dispatcher
    # agent (state + heartbeat), a ring buffer of recent actions, and a control flag.
    c.execute("""CREATE TABLE IF NOT EXISTS dispatcher(
        agent TEXT PRIMARY KEY, state TEXT, detail TEXT,
        heartbeat_at TEXT, epoch REAL, updated_at TEXT)""")
    c.execute("""CREATE TABLE IF NOT EXISTS dispatcher_events(
        id INTEGER PRIMARY KEY AUTOINCREMENT, agent TEXT, ts TEXT, kind TEXT, text TEXT)""")
    c.execute("""CREATE TABLE IF NOT EXISTS dispatcher_control(
        k TEXT PRIMARY KEY, v TEXT, set_at TEXT)""")
    # Live AI terminal stream: each worker tees its AI's stdout/stderr here line-by-line
    # so the /console 机器 tab can render a VSCode-style live terminal per machine. This
    # is a global ring buffer (trimmed to the most recent ~800 rows) the panel polls
    # incrementally via GET /dispatcher/output?agent=&since=<lastId> (append-only, never
    # rebuilt). Separate from dispatcher_events (which is a curated low-volume action log).
    c.execute("""CREATE TABLE IF NOT EXISTS dispatcher_output(
        id INTEGER PRIMARY KEY AUTOINCREMENT, agent TEXT, ts TEXT, line TEXT)""")
    return c


def prune(c):
    cutoff = time.time()
    c.execute("DELETE FROM locks WHERE (? - epoch) > ttl", (cutoff,))


def live_locks(c):
    prune(c)
    rows = c.execute("SELECT agent,files,core_feature,purpose,started_at,heartbeat_at,ttl FROM locks").fetchall()
    out = []
    for a, f, cf, p, s, h, t in rows:
        out.append({"agent": a, "files": json.loads(f or "[]"), "core_feature": cf,
                    "purpose": p, "started_at": s, "heartbeat_at": h, "ttl_seconds": t})
    return out


def claim(agent, files, feature, purpose):
    files = norm_files(files)
    with _lock:
        c = db()
        try:
            c.execute("BEGIN IMMEDIATE")  # serialize concurrent claims => true atomicity
            prune(c)
            conflicts = []
            for a, f in c.execute("SELECT agent,files FROM locks WHERE agent != ?", (agent,)).fetchall():
                overlap = sorted(set(files) & set(json.loads(f or "[]")))
                if overlap:
                    conflicts.append({"agent": a, "files": overlap})
            if conflicts:
                c.execute("ROLLBACK")
                return 409, {"ok": False, "conflicts": conflicts,
                             "note": "do NOT overwrite; pick other work / wait / coordinate"}
            row = c.execute("SELECT started_at FROM locks WHERE agent=?", (agent,)).fetchone()
            started = row[0] if row else now()
            c.execute("REPLACE INTO locks VALUES(?,?,?,?,?,?,?,?)",
                      (agent, json.dumps(files), feature, purpose, started, now(), TTL, time.time()))
            c.execute("COMMIT")
            return 200, {"ok": True, "agent": agent, "files": files,
                         "core_feature": feature, "purpose": purpose}
        finally:
            c.close()


def heartbeat(agent):
    with _lock:
        c = db()
        try:
            n = c.execute("UPDATE locks SET heartbeat_at=?, epoch=? WHERE agent=?",
                          (now(), time.time(), agent)).rowcount
            c.commit()
            return 200, {"ok": True, "refreshed": n}
        finally:
            c.close()


def release(agent):
    with _lock:
        c = db()
        try:
            c.execute("DELETE FROM locks WHERE agent=?", (agent,))
            c.commit()
            return 200, {"ok": True, "released": agent}
        finally:
            c.close()


# Loop state machine: todo -> (PM assigns, needs verify_rounds>=3) -> assigned ->
# in_progress -> review (worker done) -> [dispatcher audits] -> done | back to
# assigned (bounce w/ review_notes) | needs_owner (high-risk parked for Owner).
TASK_STATUSES = ("todo", "assigned", "in_progress", "review", "done", "blocked", "needs_owner")
# Legal state-machine transitions (enforced on /tasks/status so a worker can't skip
# the review step or reopen finished work by accident).
ALLOWED_TX = {
    "todo": {"assigned", "blocked", "needs_owner"},
    "assigned": {"in_progress", "blocked", "needs_owner", "todo"},
    "in_progress": {"review", "blocked", "needs_owner", "assigned", "todo"},
    "review": {"done", "assigned", "needs_owner", "blocked"},
    "needs_owner": {"done", "assigned", "in_progress", "blocked", "todo"},
    "blocked": {"assigned", "in_progress", "needs_owner", "todo"},
    "done": {"assigned", "needs_owner"},
}
_TASK_COLS = ["id", "title", "detail", "wave", "feature", "spec_refs", "acceptance",
              "scope_files", "assignee", "status", "priority", "risk", "verify_rounds",
              "verify_notes", "notes", "review_notes", "reviewed_by",
              "updated_by", "created_at", "updated_at"]


def _task_to_dict(r):
    d = dict(zip(_TASK_COLS, r))
    d["scope_files"] = json.loads(d.get("scope_files") or "[]")
    d["spec_refs"] = json.loads(d.get("spec_refs") or "[]")
    return d


def list_tasks(c, status=None, assignee=None, wave=None):
    q = "SELECT " + ",".join(_TASK_COLS) + " FROM tasks"
    conds, args = [], []
    if status:
        conds.append("status=?"); args.append(status)
    if assignee:
        conds.append("assignee=?"); args.append(assignee)
    if wave:
        conds.append("wave=?"); args.append(wave)
    if conds:
        q += " WHERE " + " AND ".join(conds)
    q += " ORDER BY priority ASC, id ASC"
    return [_task_to_dict(r) for r in c.execute(q, args).fetchall()]


def upsert_task(b):
    """Create or merge-update a task (fields absent from the body keep their stored
    value, so a partial update never clobbers other fields). The 3-round self-verify
    gate lives HERE: a task cannot carry an assignee or move past 'todo' unless
    verify_rounds>=3 — Owner rule '分配的人必须 review 三遍'."""
    tid = (b.get("id") or "").strip()
    if not tid:
        return 400, {"error": "task id required"}
    with _lock:
        c = db()
        try:
            row = c.execute("SELECT " + ",".join(_TASK_COLS) + " FROM tasks WHERE id=?", (tid,)).fetchone()
            cur = _task_to_dict(row) if row else {}

            def take(k, dflt):
                return b[k] if k in b else cur.get(k, dflt)

            status = (take("status", "todo") or "todo").strip()
            if status not in TASK_STATUSES:
                return 400, {"error": "bad status", "allowed": list(TASK_STATUSES)}
            # For an EXISTING task, route status changes through the same state machine
            # as /tasks/status so upsert/load can't be a backdoor around the review step.
            cur_status = cur.get("status")
            if cur_status and status != cur_status and status not in ALLOWED_TX.get(cur_status, set()):
                return 409, {"error": f"illegal transition {cur_status} -> {status}",
                             "allowed_from_here": sorted(ALLOWED_TX.get(cur_status, set()))}
            assignee = (take("assignee", "") or "").strip()
            try:
                vr = int(take("verify_rounds", 0) or 0)
            except (TypeError, ValueError):
                return 400, {"error": "verify_rounds must be an integer"}
            needs_verify = bool(assignee) or status in ("assigned", "in_progress", "review", "done")
            if needs_verify and vr < 3:
                return 422, {"error": "allocation needs 3 self-refute + self-verify rounds before it can be assigned",
                             "verify_rounds": vr,
                             "rule": "分配的人必须 review 三遍(自我反驳 + 自我验证)才能派活"}
            stored_risk = (cur.get("risk") or "").strip()
            incoming_risk = (take("risk", "") or "").strip()
            owner_ok = bool(OWNER_TOKEN) and b.get("owner_token", "") == OWNER_TOKEN
            # risk is STICKY: once set, only the Owner token may lower/clear it. This
            # stops a worker from clearing risk to slip a high-risk task past the gate.
            risk = incoming_risk if (owner_ok or not stored_risk) else stored_risk
            # A risk task can never be closed via upsert — must go through the
            # /tasks/status needs_owner + owner-token path.
            if status == "done" and risk:
                return 423, {"error": f"high-risk task (risk={risk}) cannot be closed via upsert; needs Owner approval via /tasks/status",
                             "rule": "高危任务只能经 needs_owner + Owner 令牌走 /tasks/status 关闭"}
            try:
                priority = int(take("priority", 100) or 100)
            except (TypeError, ValueError):
                priority = 100
            files = norm_files(take("scope_files", []))
            spec_refs = take("spec_refs", [])
            if isinstance(spec_refs, str):
                spec_refs = [s.strip() for s in spec_refs.split(",") if s.strip()]
            created = cur.get("created_at") or now()
            c.execute("REPLACE INTO tasks VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", (
                tid, take("title", ""), take("detail", ""), take("wave", ""),
                take("feature", ""), json.dumps(spec_refs), take("acceptance", ""),
                json.dumps(files), assignee, status, priority, risk,
                vr, take("verify_notes", ""), take("notes", ""),
                take("review_notes", ""), take("reviewed_by", ""),
                b.get("updated_by", ""), created, now()))
            c.commit()
            return 200, {"ok": True, "id": tid, "status": status,
                         "assignee": assignee, "verify_rounds": vr}
        finally:
            c.close()


def update_task_status(b):
    """Transition a task (worker progress OR dispatcher review verdict). Cannot
    create tasks or change assignee/verify_rounds. Sets any of notes / review_notes
    / reviewed_by / updated_by that are present in the body, plus the new status."""
    tid = (b.get("id") or "").strip()
    status = (b.get("status") or "").strip()
    if not tid or status not in TASK_STATUSES:
        return 400, {"error": "id + valid status required", "allowed": list(TASK_STATUSES)}
    with _lock:
        c = db()
        try:
            row = c.execute("SELECT status, verify_rounds, risk FROM tasks WHERE id=?", (tid,)).fetchone()
            if not row:
                return 404, {"error": "no such task", "id": tid}
            cur_status, vr, risk = (row[0] or "todo"), (row[1] or 0), (row[2] or "").strip()
            # State-machine: only legal transitions (no skipping review, no silent reopen).
            if status != cur_status and status not in ALLOWED_TX.get(cur_status, set()):
                return 409, {"error": f"illegal transition {cur_status} -> {status}",
                             "allowed_from_here": sorted(ALLOWED_TX.get(cur_status, set()))}
            # Same 3-round gate as upsert_task: cannot progress (assigned/in_progress/
            # review/done) unless 3 self-verify rounds passed. Park/block/reset always ok.
            if status in ("assigned", "in_progress", "review", "done") and vr < 3:
                return 422, {"error": "task has not passed 3 self-refute + self-verify rounds; cannot progress it",
                             "verify_rounds": vr,
                             "rule": "分配/推进前必须 3 轮自反验证(verify_rounds>=3)"}
            # High-risk Owner gate (two locks): (1) a risk task can only reach 'done'
            # via needs_owner; (2) the needs_owner -> done approval requires the separate
            # Owner token — workers/dispatcher share COORD_TOKEN and must NOT self-approve.
            if status == "done":
                if risk and cur_status != "needs_owner":
                    return 423, {"error": f"high-risk task (risk={risk}) needs Owner approval: park to needs_owner first",
                                 "rule": "高危必须先 park 成 needs_owner,再由 Owner 批准"}
                if cur_status == "needs_owner":
                    if not OWNER_TOKEN or b.get("owner_token", "") != OWNER_TOKEN:
                        return 403, {"error": "only the Owner can approve a parked task to done (needs COORD_OWNER_TOKEN)",
                                     "rule": "needs_owner -> done 必须 Owner 令牌;worker/dispatcher 不能自批"}
            sets, args = ["status=?"], [status]
            for f in ("notes", "review_notes", "reviewed_by", "updated_by"):
                if f in b:
                    sets.append(f + "=?"); args.append(b[f])
            sets.append("updated_at=?"); args.append(now())
            args.append(tid)
            c.execute("UPDATE tasks SET " + ",".join(sets) + " WHERE id=?", args)
            c.commit()
            return 200, {"ok": True, "id": tid, "status": status}
        finally:
            c.close()


# --- dispatcher daemon status / control (drives the /console panel) ---
def record_dispatcher(b):
    agent = (b.get("agent") or "").strip()
    if not agent:
        return 400, {"error": "agent required"}
    c = db()
    try:
        c.execute("REPLACE INTO dispatcher(agent,state,detail,heartbeat_at,epoch,updated_at) VALUES(?,?,?,?,?,?)",
                  (agent, (b.get("state") or "unknown")[:40], (b.get("detail") or "")[:500],
                   now(), time.time(), now()))
        ev = b.get("event")
        if isinstance(ev, dict) and (ev.get("text") or ev.get("kind")):
            c.execute("INSERT INTO dispatcher_events(agent,ts,kind,text) VALUES(?,?,?,?)",
                      (agent, now(), (ev.get("kind") or "log")[:40], (ev.get("text") or "")[:4000]))
            c.execute("DELETE FROM dispatcher_events WHERE id NOT IN "
                      "(SELECT id FROM dispatcher_events ORDER BY id DESC LIMIT 150)")
        c.commit()
        row = c.execute("SELECT v FROM dispatcher_control WHERE k='desired'").fetchone()
        return 200, {"ok": True, "control": (row[0] if row else "run")}
    finally:
        c.close()


def dispatcher_view(c):
    agents = []
    for a, st, dt, hb, ep, up in c.execute(
            "SELECT agent,state,detail,heartbeat_at,epoch,updated_at FROM dispatcher ORDER BY agent").fetchall():
        age = int(time.time() - ep) if ep else None
        agents.append({"agent": a, "state": st, "detail": dt, "heartbeat_at": hb,
                       "age_sec": age, "fresh": bool(ep) and (time.time() - ep) < 240})
    events = [{"agent": ag, "ts": t, "kind": k, "text": x} for ag, t, k, x in c.execute(
        "SELECT agent,ts,kind,text FROM dispatcher_events ORDER BY id DESC LIMIT 60").fetchall()]
    row = c.execute("SELECT v FROM dispatcher_control WHERE k='desired'").fetchone()
    return {"agents": agents, "events": events, "control": (row[0] if row else "run")}


def set_dispatcher_control(b):
    action = (b.get("action") or "").strip()
    if action not in ("run", "pause"):
        return 400, {"error": "action must be run|pause"}
    c = db()
    try:
        c.execute("REPLACE INTO dispatcher_control(k,v,set_at) VALUES('desired',?,?)", (action, now()))
        c.commit()
    finally:
        c.close()
    return 200, {"ok": True, "control": action}


# --- live AI terminal stream (per-machine VSCode-style chat in the /console 机器 tab) ---
# Symmetric to dispatcher events but high-volume + raw: a worker tees its AI's combined
# stdout/stderr here, the panel polls it incrementally and APPENDS new rows so a long run
# scrolls like a real terminal. Buffer is global-trimmed so it can never grow unbounded.
_OUTPUT_KEEP = 800   # most-recent rows kept across ALL agents (global ring buffer)


def record_output(b):
    agent = (b.get("agent") or "").strip()
    if not agent:
        return 400, {"error": "agent required"}
    lines = b.get("lines")
    if not isinstance(lines, list):
        return 400, {"error": "lines must be a list of strings"}
    c = db()
    try:
        last_id = None
        # Each line is appended with its own timestamp; line[:2000] caps a runaway
        # single line so one giant blob cannot blow the row size / buffer budget.
        for ln in lines:
            cur = c.execute("INSERT INTO dispatcher_output(agent,ts,line) VALUES(?,?,?)",
                            (agent, now(), (str(ln) if ln is not None else "")[:2000]))
            last_id = cur.lastrowid
        # Trim to the most recent ~800 rows globally (ring buffer) so unbounded AI
        # output never bloats the DB; the panel only ever needs the recent tail.
        c.execute("DELETE FROM dispatcher_output WHERE id NOT IN "
                  "(SELECT id FROM dispatcher_output ORDER BY id DESC LIMIT ?)", (_OUTPUT_KEEP,))
        c.commit()
        if last_id is None:
            row = c.execute("SELECT MAX(id) FROM dispatcher_output").fetchone()
            last_id = row[0] if row and row[0] is not None else 0
        return 200, {"ok": True, "last_id": last_id}
    finally:
        c.close()


def output_view(c, agent, since):
    # Incremental tail: only rows newer than the caller's lastId for this agent, oldest
    # first so the panel can append in order. Capped per poll so a burst can't ship a
    # megabyte at once (the panel just catches up over the next polls).
    rows = c.execute(
        "SELECT id,ts,line FROM dispatcher_output WHERE agent=? AND id>? ORDER BY id ASC LIMIT 400",
        (agent, since)).fetchall()
    lines = [{"id": i, "ts": t, "line": x} for (i, t, x) in rows]
    last_id = lines[-1]["id"] if lines else since
    return {"lines": lines, "last_id": last_id}


VIEW_HTML = """<!DOCTYPE html>
<html lang=zh><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>HUAKAI 协调看板</title><style>
body{margin:0;background:#0f1419;color:#e6edf3;font:14px/1.6 system-ui,"PingFang SC","Microsoft YaHei",sans-serif}
.w{max-width:900px;margin:0 auto;padding:18px}
h1{font-size:18px;margin:0 0 2px}.sub{color:#8b98a9;font-size:12px;margin-bottom:12px}
.tok{display:flex;gap:8px;margin-bottom:14px}
.tok input{flex:1;background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:7px 10px}
.tok button{background:#143d22;border:1px solid #2d6a3f;color:#7ee29a;border-radius:6px;padding:7px 14px;cursor:pointer}
.card{background:#171d26;border:1px solid #2a3340;border-radius:9px;padding:12px;margin-bottom:8px}
.ag{font-weight:600;font-size:15px}.ft{color:#4da3ff;font-size:12px;margin-left:8px}
.pp{color:#8b98a9;font-size:12px}.fl{font-family:ui-monospace,monospace;font-size:12px;color:#cfd8e3;margin-top:4px}
.age{float:right;color:#8b98a9;font-size:11px}.empty{color:#8b98a9;padding:24px;text-align:center}
.err{color:#e5534b}.st{color:#8b98a9;font-size:11px;margin-top:10px}
</style></head><body><div class=w>
<h1>HUAKAI 协调看板 · 谁在编什么</h1>
<div class=sub>三台机器实时编辑状态 · 每 4 秒自动刷新 · 只读</div>
<div class=tok><input id=t type=password placeholder="粘贴 COORD_TOKEN 后回车"><button onclick=save()>看</button></div>
<div id=out class=empty>输入 token 开始</div><div id=st class=st></div>
</div><script>
var T=sessionStorage.getItem('ct')||'';
if(T)document.getElementById('t').value=T;
document.getElementById('t').addEventListener('keydown',function(e){if(e.key==='Enter')save();});
function save(){T=document.getElementById('t').value.trim();sessionStorage.setItem('ct',T);tick();}
function esc(s){return (s==null?'':''+s).replace(/[&<>]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;'}[c];});}
function ago(iso){try{var d=(Date.now()-new Date(iso).getTime())/1000;return d<60?Math.round(d)+'s':Math.round(d/60)+'m';}catch(e){return '';}}
function tick(){ if(!T)return;
 fetch('/board',{headers:{Authorization:'Bearer '+T}}).then(function(r){
  if(r.status===401){document.getElementById('out').className='empty err';document.getElementById('out').textContent='token 错误';return null;}
  return r.json();
 }).then(function(d){ if(!d)return;
  var L=d.locks||[], o=document.getElementById('out');
  if(!L.length){o.className='empty';o.textContent='(当前无人在编)';}
  else{o.className='';o.innerHTML=L.map(function(l){return '<div class=card><span class=age>'+ago(l.heartbeat_at)+' 前</span>'+
   '<div><span class=ag>'+esc(l.agent)+'</span><span class=ft>'+esc(l.core_feature||'')+'</span></div>'+
   '<div class=pp>'+esc(l.purpose||'')+'</div><div class=fl>'+(l.files||[]).map(esc).join('<br>')+'</div></div>';}).join('');}
  document.getElementById('st').textContent='更新于 '+new Date().toLocaleTimeString();
 }).catch(function(e){document.getElementById('out').className='empty err';
  document.getElementById('out').innerHTML='连不上服务。先在浏览器打开 https://45.8.114.249:8443/healthz 接受一次自签证书,再回来。';});
}
setInterval(tick,4000); tick();
</script></body></html>"""


DISPATCH_HTML = """<!DOCTYPE html>
<html lang=zh><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>HUAKAI 调度看板 · 谁分到什么</title><style>
body{margin:0;background:#0f1419;color:#e6edf3;font:14px/1.6 system-ui,"PingFang SC","Microsoft YaHei",sans-serif}
.w{max-width:1100px;margin:0 auto;padding:18px}
h1{font-size:18px;margin:0 0 2px}.sub{color:#8b98a9;font-size:12px;margin-bottom:12px}
.tok{display:flex;gap:8px;margin-bottom:14px}
.tok input{flex:1;background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:7px 10px}
.tok button{background:#143d22;border:1px solid #2d6a3f;color:#7ee29a;border-radius:6px;padding:7px 14px;cursor:pointer}
.sum{display:flex;flex-wrap:wrap;gap:8px;margin-bottom:14px}
.pill{background:#171d26;border:1px solid #2a3340;border-radius:20px;padding:3px 11px;font-size:12px}
.grp{margin-bottom:18px}.grp h2{font-size:14px;margin:0 0 6px;color:#cdd9e5}
.task{background:#171d26;border:1px solid #2a3340;border-radius:9px;padding:10px 12px;margin-bottom:7px}
.task .top{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.id{font-weight:600;color:#9ecbff}.ttl{flex:1;min-width:160px}
.b{font-size:11px;padding:1px 8px;border-radius:11px;border:1px solid}
.s-todo{color:#8b98a9;border-color:#3a4350}.s-assigned{color:#9ecbff;border-color:#2b4a6b}
.s-in_progress{color:#f0c674;border-color:#6b5a2b}.s-review{color:#c9a7f0;border-color:#5a3a6b}
.s-done{color:#7ee29a;border-color:#2d6a3f}.s-blocked{color:#ff8b8b;border-color:#6b2b2b}
.v-ok{color:#7ee29a;border-color:#2d6a3f}.v-no{color:#f0c674;border-color:#6b5a2b}
.risk{color:#ff8b8b;border-color:#6b2b2b}.wave{color:#8b98a9;border-color:#3a4350}
.meta{color:#7d8794;font-size:12px;margin-top:4px}.files{color:#6b7480;font-size:11px;font-family:ui-monospace,monospace}
.err{color:#ff8b8b}small{color:#8b98a9}
</style></head><body><div class=w>
<h1>HUAKAI 调度看板 · 谁分到什么</h1>
<div class=sub>三台机器的任务分配 · 每 4 秒刷新 · 只读 · 派活必须 3 轮自验(✓3)</div>
<div class=tok><input id=t type=password placeholder="贴 COORD_TOKEN(只存本会话,不进 URL)"><button onclick=save()>看</button></div>
<div id=sum class=sum></div><div id=out></div>
<div class=sub id=ts></div></div>
<script>
var T=sessionStorage.getItem('COORD_TOKEN')||'';
if(T)document.getElementById('t').value=T;
function save(){T=document.getElementById('t').value.trim();sessionStorage.setItem('COORD_TOKEN',T);tick();}
function esc(s){return (s==null?'':''+s).replace(/[&<>"]/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c];});}
var STAT=['in_progress','review','assigned','blocked','todo','done'];
function tick(){
 if(!T){document.getElementById('out').innerHTML='<div class=err>先贴 token。</div>';return;}
 fetch('/tasks',{headers:{Authorization:'Bearer '+T}}).then(function(r){
  if(r.status===401)throw new Error('token 不对(401)');if(!r.ok)throw new Error('HTTP '+r.status);return r.json();
 }).then(function(d){
  var ts=d.tasks||[];
  var cnt={};ts.forEach(function(t){cnt[t.status]=(cnt[t.status]||0)+1;});
  document.getElementById('sum').innerHTML='<span class=pill>共 '+ts.length+'</span>'+
   STAT.map(function(s){return cnt[s]?'<span class="pill s-'+s+'">'+s+' '+cnt[s]+'</span>':'';}).join('');
  var groups={};ts.forEach(function(t){var k=t.assignee||'(未分配)';(groups[k]=groups[k]||[]).push(t);});
  var keys=Object.keys(groups).sort();
  document.getElementById('out').innerHTML=keys.length?keys.map(function(k){
   return '<div class=grp><h2>'+esc(k)+' · '+groups[k].length+'</h2>'+groups[k].map(card).join('')+'</div>';
  }).join(''):'<div class=sub>(还没有任务。等总指挥派活。)</div>';
  document.getElementById('ts').textContent='更新于 '+new Date().toLocaleTimeString();
 }).catch(function(e){document.getElementById('out').innerHTML='<div class=err>'+esc(e.message)+'</div>';});
}
function card(t){
 var v=(t.verify_rounds>=3)?'<span class="b v-ok">✓3 自验</span>':'<span class="b v-no">⚠ '+(t.verify_rounds||0)+'/3 未足验</span>';
 var risk=t.risk?'<span class="b risk">'+esc(t.risk)+'</span>':'';
 var wave=t.wave?'<span class="b wave">'+esc(t.wave)+'</span>':'';
 var files=(t.scope_files&&t.scope_files.length)?'<div class=files>'+esc(t.scope_files.join(', '))+'</div>':'';
 return '<div class=task><div class=top><span class=id>'+esc(t.id)+'</span>'+
  '<span class=ttl>'+esc(t.title)+'</span>'+wave+'<span class="b s-'+esc(t.status)+'">'+esc(t.status)+'</span>'+v+risk+'</div>'+
  (t.detail?'<div class=meta>'+esc(t.detail)+'</div>':'')+
  (t.review_notes?'<div class=meta>↩ 审核/Owner:'+esc(t.review_notes)+(t.reviewed_by?' ('+esc(t.reviewed_by)+')':'')+'</div>':'')+
  (t.notes?'<div class=meta>📝 '+esc(t.notes)+'</div>':'')+files+'</div>';
}
setInterval(tick,4000); tick();
</script></body></html>"""


CONSOLE_HTML = """<!DOCTYPE html>
<html lang=zh><head><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">
<title>HUAKAI 控制台</title><style>
*{box-sizing:border-box}
body{margin:0;background:#0f1419;color:#e6edf3;font:14px/1.6 system-ui,"PingFang SC","Microsoft YaHei",sans-serif}
.w{max-width:1240px;margin:0 auto;padding:14px 16px 40px}
a{color:#9ecbff;text-decoration:none}
.bar{display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin-bottom:4px}
h1{font-size:18px;margin:0;white-space:nowrap}
.tok{display:flex;gap:6px;flex:1;min-width:220px;max-width:420px}
.tok input{flex:1;background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:6px 10px;font-size:13px}
.tok button{background:#143d22;border:1px solid #2d6a3f;color:#7ee29a;border-radius:6px;padding:6px 12px;cursor:pointer;font-size:13px}
.sub{color:#8b98a9;font-size:12px}
.tabs{display:flex;gap:4px;flex-wrap:wrap;border-bottom:1px solid #2a3340;margin:10px 0 14px}
.tab{background:none;border:none;border-bottom:2px solid transparent;color:#8b98a9;padding:8px 14px;cursor:pointer;font-size:14px;border-radius:6px 6px 0 0}
.tab:hover{color:#cdd9e5;background:#171d26}
.tab.active{color:#7ee29a;border-bottom-color:#2d6a3f;background:#141a22}
.tab .badge{background:#6b2b2b;color:#ff8b8b;border-radius:10px;font-size:11px;padding:0 7px;margin-left:6px}
.card{background:#171d26;border:1px solid #2a3340;border-radius:10px;padding:12px 14px;margin-bottom:12px}
.card h2{font-size:14px;margin:0 0 8px;color:#cdd9e5}
.row{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.b{font-size:11px;padding:1px 8px;border-radius:11px;border:1px solid;white-space:nowrap}
.on{color:#7ee29a;border-color:#2d6a3f}.off{color:#ff8b8b;border-color:#6b2b2b}.warn{color:#f0c674;border-color:#6b5a2b}
.btn{background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:5px 12px;cursor:pointer;font-size:12px}
.btn.stop{color:#ff8b8b;border-color:#6b2b2b}.btn.go{color:#7ee29a;border-color:#2d6a3f}
.ev{font-family:ui-monospace,monospace;font-size:11.5px;max-height:480px;overflow:auto;background:#0c1116;border:1px solid #232c36;border-radius:7px;padding:8px}
.ev .e{padding:3px 0;border-bottom:1px solid #1a222c;white-space:pre-wrap}
.ev .t{color:#6b7480}.ev .k{color:#9ecbff}
.pill{background:#171d26;border:1px solid #2a3340;border-radius:20px;padding:3px 11px;font-size:12px;display:inline-block;margin:0 5px 5px 0}
.s-todo{color:#8b98a9;border-color:#3a4350}.s-assigned{color:#9ecbff;border-color:#2b4a6b}
.s-in_progress{color:#f0c674;border-color:#6b5a2b}.s-review{color:#c9a7f0;border-color:#5a3a6b}
.s-done{color:#7ee29a;border-color:#2d6a3f}.s-blocked{color:#ff8b8b;border-color:#6b2b2b}.s-needs_owner{color:#ff5f5f;border-color:#6b2b2b}
.task{background:#141a22;border:1px solid #232c36;border-radius:8px;padding:8px 10px;margin-bottom:6px}
.id{font-weight:600;color:#9ecbff}.meta{color:#7d8794;font-size:12px;margin-top:3px}
.files{color:#6b7480;font-size:11px;font-family:ui-monospace,monospace}
.lock{font-size:12px;color:#cdd9e5;margin-bottom:4px}.lock .who{color:#f0c674}
.err{color:#ff8b8b}.grp h3{font-size:13px;color:#cdd9e5;margin:8px 0 5px}
.mgrid{display:grid;grid-template-columns:repeat(auto-fill,minmax(230px,1fr));gap:10px}
.mcard{background:#141a22;border:1px solid #232c36;border-radius:9px;padding:10px 12px;cursor:pointer}
.mcard:hover{border-color:#2d6a3f}.mcard.sel{border-color:#2d6a3f;background:#13241a}
.mcard .nm{font-weight:600;font-size:14px;color:#e6edf3}
.mline{color:#8b98a9;font-size:12px;margin-top:3px}
.term-wrap{margin-top:12px}
.term-head{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:6px}
.term-head .live{color:#7ee29a;font-size:12px}
.dot{display:inline-block;width:8px;height:8px;border-radius:50%;background:#7ee29a;margin-right:5px;animation:bl 1.4s ease-in-out infinite}
@keyframes bl{0%,100%{opacity:1}50%{opacity:.25}}
.term{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;line-height:1.5;height:520px;overflow:auto;background:#05080b;border:1px solid #232c36;border-radius:8px;padding:10px}
.term .ln{white-space:pre-wrap;word-break:break-word;color:#c8d3df}
.term .ts{color:#46525e;margin-right:8px}
.tnote{color:#8b98a9;padding:30px;text-align:center}
</style></head><body><div class=w>
<div class=bar>
 <h1>HUAKAI 控制台</h1>
 <div class=tok><input id=t type=password placeholder="贴 COORD_TOKEN(只存本会话,不进 URL)"><button id=go>进入</button></div>
 <span class=sub id=ts></span>
</div>
<div class=tabs id=tabs></div>
<div id=view></div>
</div>
<script>
var T=sessionStorage.getItem('COORD_TOKEN')||'';
if(T)document.getElementById('t').value=T;
function esc(s){return (s==null?'':''+s).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});}
function hdr(){return {Authorization:'Bearer '+T};}
function ago(s){if(s==null)return '?';if(s<60)return s+'秒前';if(s<3600)return Math.floor(s/60)+'分前';return Math.floor(s/3600)+'时前';}
function chk(r){if(r.status===401)throw new Error('token 不对(401)');if(!r.ok)throw new Error('HTTP '+r.status);return r.json();}
var STAT=['needs_owner','in_progress','review','assigned','blocked','todo','done'];
var TABS=[['overview','总览'],['machines','机器'],['tasks','任务'],['approve','待批'],['logs','日志']];

// ---- shared latest data (filled by poll) ----
var DATA={ds:null,tasks:[],locks:[]}, FRESH={};
var TAB=sessionStorage.getItem('console_tab')||'overview';
if(!TABS.some(function(x){return x[0]===TAB;}))TAB='overview';
var SEL=sessionStorage.getItem('console_machine')||'';   // selected machine for terminal

// ---- anti-clobber re-render: only touch a section when its html changed; pause while
// the pointer is over / focus is inside the content; keep window scroll across renders.
var interacting=false, lastSectionHtml={};
var VIEW=document.getElementById('view');
VIEW.addEventListener('pointerenter',function(){interacting=true;});
VIEW.addEventListener('pointerleave',function(){interacting=false;});
VIEW.addEventListener('focusin',function(){interacting=true;});
VIEW.addEventListener('focusout',function(){setTimeout(function(){interacting=false;},250);});

function setHtml(el,html,key){
 // re-render ONLY if changed; preserve window scroll so a refresh never yanks the page.
 if(lastSectionHtml[key]===html)return;
 lastSectionHtml[key]=html;
 var sx=window.scrollX, sy=window.scrollY;
 el.innerHTML=html;
 window.scrollTo(sx,sy);
}

// ---- token + tabs ----
document.getElementById('go').addEventListener('click',function(){
 T=document.getElementById('t').value.trim();sessionStorage.setItem('COORD_TOKEN',T);
 lastSectionHtml={};poll(true);
});
document.getElementById('t').addEventListener('keydown',function(e){if(e.key==='Enter')document.getElementById('go').click();});

function renderTabs(){
 var pend=DATA.tasks.filter(function(t){return t.status==='needs_owner';}).length;
 var h=TABS.map(function(x){
  var id=x[0],lbl=x[1];
  var badge=(id==='approve'&&pend)?'<span class=badge>'+pend+'</span>':'';
  return '<button class="tab'+(id===TAB?' active':'')+'" data-tab="'+id+'">'+esc(lbl)+badge+'</button>';
 }).join('');
 document.getElementById('tabs').innerHTML=h;
}
document.getElementById('tabs').addEventListener('click',function(e){
 var b=e.target.closest('button[data-tab]');if(!b)return;
 var id=b.getAttribute('data-tab');if(id===TAB)return;
 TAB=id;sessionStorage.setItem('console_tab',TAB);
 lastSectionHtml={};                 // tab switch is deliberate -> force a full render
 stopTerm();                          // leave terminal poll when navigating away
 renderTabs();renderView(true);
 if(TAB==='machines'&&SEL)startTerm(SEL);
});

// ---- delegated actions (work even while interacting, they are user-driven) ----
VIEW.addEventListener('click',function(e){
 var el=e.target;
 var act=el.closest&&el.closest('[data-act]');
 if(act){control(act.getAttribute('data-act'));return;}
 var ap=el.closest&&el.closest('[data-approve]');
 if(ap){approve(ap.getAttribute('data-approve'));return;}
 var mc=el.closest&&el.closest('[data-machine]');
 if(mc){selectMachine(mc.getAttribute('data-machine'));return;}
});
function control(action){
 fetch('/dispatcher/control',{method:'POST',headers:Object.assign({'Content-Type':'application/json'},hdr()),body:JSON.stringify({action:action})})
  .then(chk).then(function(){poll(true);}).catch(function(e){alert('操作失败:'+e.message);});
}
function approve(id){
 var ot=prompt('批准高危任务 '+id+' \\u2192 done。\\n输入 Owner 专用令牌(COORD_OWNER_TOKEN):');
 if(!ot)return;
 fetch('/tasks/status',{method:'POST',headers:Object.assign({'Content-Type':'application/json'},hdr()),
   body:JSON.stringify({id:id,status:'done',owner_token:ot,updated_by:'owner-console'})})
  .then(function(r){return r.json();}).then(function(d){if(d.error)alert('未批准:'+d.error);else poll(true);})
  .catch(function(e){alert('失败:'+e.message);});
}

// ---- polling: status + tasks + locks every 5s (terminal has its own 2s loop) ----
function poll(force){
 if(!T){VIEW.innerHTML='<div class=err>先贴 token 再点进入。</div>';document.getElementById('tabs').innerHTML='';return;}
 Promise.all([
  fetch('/dispatcher/status',{headers:hdr()}).then(chk),
  fetch('/tasks',{headers:hdr()}).then(chk),
  fetch('/board',{headers:hdr()}).then(chk)
 ]).then(function(res){
  DATA.ds=res[0];DATA.tasks=res[1].tasks||[];DATA.locks=res[2].locks||[];
  FRESH={};(DATA.ds.agents||[]).forEach(function(a){FRESH[a.agent]=a;});
  renderTabs();renderView(force);
  document.getElementById('ts').textContent='更新于 '+new Date().toLocaleTimeString()+(interacting?' · 悬停中(列表已暂停刷新)':'');
 }).catch(function(e){VIEW.innerHTML='<div class=err>'+esc(e.message)+'</div>';});
}

function renderView(force){
 // pause LIST re-renders while the user is interacting so clicks land + scroll stays;
 // a tab switch passes force=true. The terminal append path is exempt (separate loop).
 if(!force && interacting) return;
 if(TAB==='overview')return viewOverview();
 if(TAB==='machines')return viewMachines();
 if(TAB==='tasks')return viewTasks();
 if(TAB==='approve')return viewApprove();
 if(TAB==='logs')return viewLogs();
}

function statusPills(){
 var cnt={};DATA.tasks.forEach(function(t){cnt[t.status]=(cnt[t.status]||0)+1;});
 return '<span class=pill>共 '+DATA.tasks.length+'</span>'+STAT.map(function(s){return cnt[s]?'<span class="pill s-'+s+'">'+esc(s)+' '+cnt[s]+'</span>':'';}).join('');
}
function machineLine(a){
 var paused=((DATA.ds||{}).control==='pause'),stale=!a.fresh;
 // 暂停时调度方(server-a/PM)不再跑轮→心跳必然变陈旧,这是预期而非崩溃,所以标「调度暂停」不标「失联」
 var cls=stale?(paused?'warn':'off'):'on';
 var lbl=stale?(paused?'调度暂停':'失联'):(a.state||'?');
 // 陈旧时 detail 是历史快照,不是正在发生的动作 — 加「上次」前缀避免误读
 var det=a.detail?(stale?'上次('+ago(a.age_sec)+'):'+a.detail:a.detail):'';
 return '<span class="b '+cls+'">'+esc(a.agent)+' · '+esc(lbl)+'</span>'+
  ' <span class=sub>心跳 '+ago(a.age_sec)+'</span>'+(det?' <span class=sub>· '+esc(det)+'</span>':'');
}
function ctlBtn(paused){
 return paused?'<button class="btn go" data-act="run">▶ 恢复</button>':'<button class="btn stop" data-act="pause">⏸ 暂停</button>';
}

// ---- 总览 ----
function viewOverview(){
 var ds=DATA.ds||{},paused=(ds.control==='pause'),ags=(ds.agents||[]);
 var pend=DATA.tasks.filter(function(t){return t.status==='needs_owner';}).length;
 var h='<div class=card><h2>📊 任务状态</h2>'+statusPills()+'</div>';
 h+='<div class=card><h2>🖥 机器</h2>'+
   (ags.length?ags.map(function(a){return '<div style="margin-bottom:4px">'+machineLine(a)+'</div>';}).join(''):'<div class=sub>(还没有机器上报心跳)</div>')+'</div>';
 h+='<div class=card><h2>⚙ 调度守护进程</h2><div class=row>'+
   (paused?'<span class="b warn">已暂停</span>':'<span class="b on">运行中</span>')+ctlBtn(paused)+
   (pend?'<span class="b off">待批 '+pend+'</span>':'')+
   '<a class=sub href="/tree">→ 功能树</a></div>'+
   '<div class=sub style="margin-top:6px">暂停=调度进程保活但不再审/合并 · 彻底停用:systemctl --user stop huakai-dispatcher</div></div>';
 setHtml(VIEW,h,'overview');
}

// ---- 机器(卡片 + 选中机器的实时终端)----
function viewMachines(){
 var ds=DATA.ds||{},paused=(ds.control==='pause'),ags=(ds.agents||[]);
 var h='<div class=card><div class=row><h2 style="margin:0;flex:1">🖥 机器 · 点卡片看实时终端</h2>'+
   (paused?'<span class="b warn">调度已暂停</span>':'<span class="b on">调度运行中</span>')+ctlBtn(paused)+'</div>';
 h+='<div class=mgrid style="margin-top:10px">'+
   (ags.length?ags.map(function(a){
     var stale=!a.fresh;
     // 与 machineLine 同逻辑:暂停时陈旧的调度方标「调度暂停」,detail 标「上次」历史快照
     var cls=stale?(paused?'warn':'off'):'on';
     var lbl=stale?(paused?'调度暂停':'失联'):(a.state||'?');
     var det=a.detail?(stale?'上次('+ago(a.age_sec)+'):'+a.detail:a.detail):'';
     return '<div class="mcard'+(a.agent===SEL?' sel':'')+'" data-machine="'+esc(a.agent)+'">'+
       '<div class=nm>'+esc(a.agent)+' <span class="b '+cls+'">'+esc(lbl)+'</span></div>'+
       '<div class=mline>心跳 '+ago(a.age_sec)+'</div>'+
       (det?'<div class=mline>'+esc(det)+'</div>':'')+'</div>';
   }).join(''):'<div class=sub>(还没有机器上报心跳)</div>')+'</div></div>';
 // terminal shell (the term body itself is owned by the terminal loop, not setHtml)
 h+='<div class=term-wrap><div class=term-head>'+
   (SEL?'<span class=live><span class=dot></span>▶ 实时 · '+esc(SEL)+'</span>':'<span class=sub>选一台机器看它的实时 AI 终端</span>')+
   '</div><div class=term id=term>'+(SEL?'':'<div class=tnote>点上面的机器卡片打开它的实时终端</div>')+'</div></div>';
 setHtml(VIEW,h,'machines');
 // after the list re-renders, (re)bind the live terminal element + resume streaming
 if(SEL)startTerm(SEL);
}
function selectMachine(m){
 if(SEL===m)return;
 SEL=m;sessionStorage.setItem('console_machine',m);
 lastSectionHtml['machines']=null;       // force the machines view to re-render selection
 renderView(true);
}

// ---- 任务 ----
function taskCard(t){
 var v=(t.verify_rounds>=3)?'<span class="b on">✓3</span>':'<span class="b warn">'+(t.verify_rounds||0)+'/3</span>';
 var risk=t.risk?'<span class="b off">'+esc(t.risk)+'</span>':'';
 var stuck='';
 if((t.status==='assigned'||t.status==='in_progress')&&t.assignee){
   var w=FRESH[t.assignee];
   if(!w) stuck='<span class="b off">⚠ worker 未上报</span>';
   else if(!w.fresh) stuck='<span class="b off">⚠ 失联</span>';
   else if(t.status==='assigned'&&w.state!=='working') stuck='<span class="b warn">已派·待接</span>';
   else stuck='<span class="b on">✓ 在做</span>';
 }
 return '<div class=task><div class=row><span class=id>'+esc(t.id)+'</span><span style="flex:1">'+esc(t.title)+'</span>'+
  (t.wave?'<span class="b s-todo">'+esc(t.wave)+'</span>':'')+'<span class="b s-'+esc(t.status)+'">'+esc(t.status)+'</span>'+v+risk+stuck+'</div>'+
  (t.review_notes?'<div class=meta>↩ '+esc(t.review_notes)+(t.reviewed_by?' ('+esc(t.reviewed_by)+')':'')+'</div>':'')+
  (t.notes?'<div class=meta>📝 '+esc(t.notes)+'</div>':'')+
  ((t.scope_files&&t.scope_files.length)?'<div class=files>'+esc(t.scope_files.join(', '))+'</div>':'')+'</div>';
}
function viewTasks(){
 var groups={};DATA.tasks.forEach(function(t){var k=t.assignee||'(未分配)';(groups[k]=groups[k]||[]).push(t);});
 var keys=Object.keys(groups).sort();
 var board=keys.map(function(k){return '<div class=grp><h3>'+esc(k)+' · '+groups[k].length+'</h3>'+groups[k].map(taskCard).join('')+'</div>';}).join('');
 var h='<div class=card><h2>📋 任务账本</h2>'+statusPills()+'<div style="margin-top:8px">'+(board||'<div class=sub>(还没有任务)</div>')+'</div></div>';
 setHtml(VIEW,h,'tasks');
}

// ---- 待批(只列 needs_owner)----
function viewApprove(){
 var ps=DATA.tasks.filter(function(t){return t.status==='needs_owner';});
 var h='<div class=card><h2>🔐 待 Owner 批准 · needs_owner</h2>'+
  (ps.length?ps.map(function(t){
    return '<div class=task><div class=row><span class=id>'+esc(t.id)+'</span><span style="flex:1">'+esc(t.title)+'</span>'+
     (t.risk?'<span class="b off">'+esc(t.risk)+'</span>':'')+'<span class="b s-needs_owner">needs_owner</span></div>'+
     (t.detail?'<div class=meta>'+esc(t.detail)+'</div>':'')+
     (t.review_notes?'<div class=meta>↩ '+esc(t.review_notes)+(t.reviewed_by?' ('+esc(t.reviewed_by)+')':'')+'</div>':'')+
     (t.notes?'<div class=meta>📝 '+esc(t.notes)+'</div>':'')+
     ((t.scope_files&&t.scope_files.length)?'<div class=files>'+esc(t.scope_files.join(', '))+'</div>':'')+
     '<div style="margin-top:6px"><button class="btn go" data-approve="'+esc(t.id)+'">✅ 批准(Owner 令牌)</button></div></div>';
  }).join(''):'<div class=sub>(没有待批任务)</div>')+'</div>';
 setHtml(VIEW,h,'approve');
}

// ---- 日志(调度事件流)----
function viewLogs(){
 var ev=((DATA.ds||{}).events||[]);
 var h='<div class=card><h2>📜 调度日志</h2><div class=ev>'+
   (ev.length?ev.map(function(e){return '<div class=e><span class=t>'+esc((e.ts||'').replace('T',' ').slice(5,19))+'</span> <span class=k>['+esc(e.kind)+']</span> '+esc(e.agent?e.agent+': ':'')+esc(e.text);}).join(''):'<div class=sub>(暂无)</div>')+
   '</div></div>';
 setHtml(VIEW,h,'logs');
}

// ===== LIVE TERMINAL: incremental append-only polling of /dispatcher/output =====
var termTimer=null, termAgent='', termLast=0, termEl=null;
function stopTerm(){if(termTimer){clearInterval(termTimer);termTimer=null;}}
function atBottom(el){return (el.scrollHeight-el.scrollTop-el.clientHeight)<24;}
function startTerm(agent){
 var el=document.getElementById('term');if(!el)return;
 // If we re-render the machines list, #term is a NEW element: if same agent, keep the
 // since cursor but the buffer is empty -> reset cursor so we re-pull the recent tail.
 if(termAgent!==agent || el!==termEl){
   termAgent=agent;termLast=0;termEl=el;el.innerHTML='';
 }
 stopTerm();
 pullTerm();                              // immediate first pull
 termTimer=setInterval(pullTerm,2000);
}
function pullTerm(){
 if(!T||!termAgent)return;
 var el=document.getElementById('term');
 if(!el){stopTerm();return;}             // navigated away
 if(el!==termEl){termEl=el;termLast=0;el.innerHTML='';}   // list re-rendered under us
 fetch('/dispatcher/output?agent='+encodeURIComponent(termAgent)+'&since='+termLast,{headers:hdr()})
  .then(chk).then(function(d){
   var lines=d.lines||[];if(!lines.length){termLast=d.last_id||termLast;return;}
   var stick=atBottom(el);              // remember BEFORE appending
   var frag='';
   lines.forEach(function(r){
     frag+='<div class=ln><span class=ts>'+esc((r.ts||'').slice(11,19))+'</span>'+esc(r.line)+'</div>';
   });
   el.insertAdjacentHTML('beforeend',frag);   // APPEND only — never rebuild the buffer
   termLast=d.last_id||termLast;
   // cap the DOM so a very long run can't grow unbounded in the browser
   while(el.childElementCount>1200)el.removeChild(el.firstChild);
   if(stick)el.scrollTop=el.scrollHeight;      // auto-scroll only if user was at bottom
  }).catch(function(){/* best-effort; next poll retries */});
}

// ---- boot ----
renderTabs();
if(T)poll(true); else VIEW.innerHTML='<div class=err>先贴 token 再点进入。</div>';
setInterval(function(){poll(false);},5000);
if(TAB==='machines'&&SEL)startTerm(SEL);
</script></body></html>"""


class H(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_raw(self, code, ctype, body):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _serve_file(self, path, ctype):
        if not path or not os.path.isfile(path):
            return self._send(404, {"error": "feature-tree not deployed on this server"})
        with open(path, "rb") as fh:
            self._send_raw(200, ctype, fh.read())

    def _serve_tree_html(self):
        if not TREE_HTML or not os.path.isfile(TREE_HTML):
            return self._send(404, {"error": "feature-tree not deployed on this server"})
        with open(TREE_HTML, "rb") as fh:
            html = fh.read()
        # Stamp the response as coordinator-served. The page enables its token
        # prompt ONLY when it sees this marker, so the same raw file hosted by any
        # other origin/proxy (which cannot inject it) will never prompt for or POST
        # the COORD_TOKEN. This verifies the coordinator origin, not just the path.
        marker = b"<script>window.__COORD_SERVED__=true;</script>"
        if b"</head>" in html:
            html = html.replace(b"</head>", marker + b"</head>", 1)
        else:
            html = marker + html
        self._send_raw(200, "text/html; charset=utf-8", html)

    def _auth(self):
        if not TOKEN:
            self._send(500, {"error": "server COORD_TOKEN not set"}); return False
        if self.headers.get("Authorization", "") != f"Bearer {TOKEN}":
            self._send(401, {"error": "unauthorized"}); return False
        return True

    def _body(self):
        n = int(self.headers.get("Content-Length", "0") or "0")
        if not n:
            return {}
        try:
            return json.loads(self.rfile.read(n) or b"{}")
        except Exception:
            return {}

    def log_message(self, *a):
        pass  # quiet

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/healthz":
            return self._send(200, {"ok": True, "ts": now()})
        if u.path in ("/", "/view"):
            # Public read-only board viewer (HTML only, no data). The page's JS
            # calls /board with the token the viewer pastes — same-origin, so no
            # CORS and the TLS cert is already accepted for this origin.
            body = VIEW_HTML.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if u.path in ("/tree", "/tree/"):
            # Public HTML shell of the feature-tree dashboard (both slash forms).
            # Carries no project data — its JS prompts for the token (only when it
            # sees the injected coordinator marker) and fetches /tree/data below.
            return self._serve_tree_html()
        if u.path in ("/dispatch", "/dispatch/"):
            # Public read-only task-dispatch board (HTML only). Its JS fetches
            # /tasks with the token the viewer pastes — same-origin, inline, so it
            # is always coordinator-served (no marker needed).
            body = DISPATCH_HTML.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if u.path in ("/console", "/console/"):
            # Unified SaaS control panel (HTML shell only; token pasted in-page). Shows
            # dispatcher daemon state + recent actions + task ledger + live locks, with
            # pause/resume + Owner-approve controls. Its JS fetches the token-gated APIs.
            return self._send_raw(200, "text/html; charset=utf-8", CONSOLE_HTML.encode())
        if not self._auth():
            return
        c = db()
        try:
            if u.path == "/tree/data":
                return self._serve_file(TREE_JSON, "application/json; charset=utf-8")
            if u.path == "/tasks":
                q = parse_qs(u.query)
                return self._send(200, {"tasks": list_tasks(
                    c, status=(q.get("status") or [None])[0],
                    assignee=(q.get("assignee") or [None])[0],
                    wave=(q.get("wave") or [None])[0])})
            if u.path == "/board":
                return self._send(200, {"locks": live_locks(c)})
            if u.path == "/dispatcher/status":
                return self._send(200, dispatcher_view(c))
            if u.path == "/dispatcher/output":
                # Incremental live-terminal tail for one machine. ?agent=<m>&since=<id>.
                q = parse_qs(u.query)
                agent = (q.get("agent") or [""])[0]
                try:
                    since = int((q.get("since") or ["0"])[0] or "0")
                except (TypeError, ValueError):
                    since = 0
                return self._send(200, output_view(c, agent, since))
            if u.path == "/check":
                raw = (parse_qs(u.query).get("file") or [""])[0]
                nf = norm_files([raw])
                f = nf[0] if nf else raw
                hits = [l for l in live_locks(c) if f in l["files"]]
                return self._send(200, {"file": f, "conflicts": hits})
        finally:
            c.close()
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if not self._auth():
            return
        u = urlparse(self.path)
        b = self._body()
        # Task-dispatch routes use updated_by, not the lock 'agent' field.
        if u.path == "/tasks":
            code, obj = upsert_task(b); return self._send(code, obj)
        if u.path == "/tasks/status":
            code, obj = update_task_status(b); return self._send(code, obj)
        if u.path == "/dispatcher/status":
            code, obj = record_dispatcher(b); return self._send(code, obj)
        if u.path == "/dispatcher/output":
            # Worker streams a batch of AI stdout/stderr lines for its machine's live
            # terminal. Placed BEFORE the lock 'agent required' block (like /dispatcher/
            # status) because it carries its own agent field, not the lock 'agent'.
            code, obj = record_output(b); return self._send(code, obj)
        if u.path == "/dispatcher/control":
            code, obj = set_dispatcher_control(b); return self._send(code, obj)
        agent = b.get("agent", "")
        if not agent:
            return self._send(400, {"error": "agent required"})
        if u.path == "/claim":
            code, obj = claim(agent, [x for x in b.get("files", []) if x],
                              b.get("core_feature", ""), b.get("purpose", ""))
            return self._send(code, obj)
        if u.path == "/heartbeat":
            code, obj = heartbeat(agent); return self._send(code, obj)
        if u.path == "/release":
            code, obj = release(agent); return self._send(code, obj)
        self._send(404, {"error": "not found"})


if __name__ == "__main__":
    if not TOKEN:
        raise SystemExit("refusing to start: set COORD_TOKEN (shared secret)")
    db().close()
    httpd = ThreadingHTTPServer((BIND, PORT), H)
    scheme = "http"
    if TLS_CERT and TLS_KEY:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(TLS_CERT, TLS_KEY)
        httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
        scheme = "https"
    print(f"coord-server on {scheme}://{BIND}:{PORT}  db={DB}  ttl={TTL}s")
    httpd.serve_forever()
