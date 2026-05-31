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
<title>HUAKAI 控制台 · 一页全局</title><style>
body{margin:0;background:#0f1419;color:#e6edf3;font:14px/1.6 system-ui,"PingFang SC","Microsoft YaHei",sans-serif}
.w{max-width:1180px;margin:0 auto;padding:16px}
h1{font-size:19px;margin:0 0 2px}.sub{color:#8b98a9;font-size:12px;margin-bottom:10px}
a{color:#9ecbff}
.tok{display:flex;gap:8px;margin-bottom:12px}
.tok input{flex:1;background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:7px 10px}
.tok button{background:#143d22;border:1px solid #2d6a3f;color:#7ee29a;border-radius:6px;padding:7px 14px;cursor:pointer}
.card{background:#171d26;border:1px solid #2a3340;border-radius:10px;padding:12px 14px;margin-bottom:14px}
.card h2{font-size:14px;margin:0 0 8px;color:#cdd9e5}
.row{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.b{font-size:11px;padding:1px 8px;border-radius:11px;border:1px solid}
.on{color:#7ee29a;border-color:#2d6a3f}.off{color:#ff8b8b;border-color:#6b2b2b}.warn{color:#f0c674;border-color:#6b5a2b}
.btn{background:#1e2630;border:1px solid #2a3340;color:#e6edf3;border-radius:6px;padding:5px 12px;cursor:pointer;font-size:12px}
.btn.stop{color:#ff8b8b;border-color:#6b2b2b}.btn.go{color:#7ee29a;border-color:#2d6a3f}
.ev{font-family:ui-monospace,monospace;font-size:11.5px;max-height:280px;overflow:auto;background:#0c1116;border:1px solid #232c36;border-radius:7px;padding:8px}
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
</style></head><body><div class=w>
<h1>HUAKAI 控制台</h1>
<div class=sub>一页看全:调度守护进程 · 任务账本 · 谁在编辑 · 每 5 秒刷新 · <a href="/tree">功能树</a></div>
<div class=tok><input id=t type=password placeholder="贴 COORD_TOKEN(只存本会话,不进 URL)"><button id=go>进入</button></div>
<div id=app></div>
<div class=sub id=ts></div></div>
<script>
var T=sessionStorage.getItem('COORD_TOKEN')||'';
if(T)document.getElementById('t').value=T;
document.getElementById('go').addEventListener('click',function(){T=document.getElementById('t').value.trim();sessionStorage.setItem('COORD_TOKEN',T);tick();});
function esc(s){return (s==null?'':''+s).replace(/[&<>"']/g,function(c){return{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];});}
function hdr(){return {Authorization:'Bearer '+T};}
function ago(s){if(s==null)return '?';if(s<60)return s+'秒前';if(s<3600)return Math.floor(s/60)+'分前';return Math.floor(s/3600)+'时前';}
var STAT=['needs_owner','in_progress','review','assigned','blocked','todo','done'];
function chk(r){if(r.status===401)throw new Error('token 不对(401)');if(!r.ok)throw new Error('HTTP '+r.status);return r.json();}
function control(action){
 fetch('/dispatcher/control',{method:'POST',headers:Object.assign({'Content-Type':'application/json'},hdr()),body:JSON.stringify({action:action})})
  .then(chk).then(function(){tick();}).catch(function(e){alert('操作失败:'+e.message);});
}
function approve(id){
 var ot=prompt('批准高危任务 '+id+' → done。\\n输入 Owner 专用令牌(COORD_OWNER_TOKEN):');
 if(!ot)return;
 fetch('/tasks/status',{method:'POST',headers:Object.assign({'Content-Type':'application/json'},hdr()),
   body:JSON.stringify({id:id,status:'done',owner_token:ot,updated_by:'owner-console'})})
  .then(function(r){return r.json();}).then(function(d){if(d.error)alert('未批准:'+d.error);else tick();})
  .catch(function(e){alert('失败:'+e.message);});
}
document.addEventListener('click',function(e){
 var el=e.target; if(!el.getAttribute)return;
 var a=el.getAttribute('data-act'); if(a){control(a);return;}
 var ap=el.getAttribute('data-approve'); if(ap){approve(ap);return;}
});
function tick(){
 if(!T){document.getElementById('app').innerHTML='<div class=err>先贴 token。</div>';return;}
 Promise.all([
  fetch('/dispatcher/status',{headers:hdr()}).then(chk),
  fetch('/tasks',{headers:hdr()}).then(chk),
  fetch('/board',{headers:hdr()}).then(chk)
 ]).then(function(res){
  render(res[0],res[1].tasks||[],res[2].locks||[]);
  document.getElementById('ts').textContent='更新于 '+new Date().toLocaleTimeString();
 }).catch(function(e){document.getElementById('app').innerHTML='<div class=err>'+esc(e.message)+'</div>';});
}
function render(ds,tasks,locks){
 var html='';
 var paused=(ds.control==='pause');
 var ags=(ds.agents||[]);
 var dstat=ags.length?ags.map(function(a){
   var cls=a.fresh?'on':'off';var lbl=a.fresh?(a.state||'?'):'失联';
   return '<div class=row style="margin-bottom:4px"><span class="b '+cls+'">'+esc(a.agent)+' · '+esc(lbl)+'</span>'+
     '<span class=sub>心跳 '+ago(a.age_sec)+'</span>'+(a.detail?'<span class=sub>· '+esc(a.detail)+'</span>':'')+'</div>';
 }).join(''):'<div class=sub>(还没有守护进程上报。daemon 重启后会每轮上报。)</div>';
 var ctlbtn=paused?'<button class="btn go" data-act="run">▶ 恢复调度</button>':'<button class="btn stop" data-act="pause">⏸ 暂停调度</button>';
 html+='<div class=card><h2>🛰 调度守护进程 '+(paused?'<span class="b warn">已暂停</span>':'<span class="b on">运行中</span>')+'</h2>'+
   dstat+'<div class=row style="margin-top:8px">'+ctlbtn+
   '<span class=sub>暂停=进程保活但不再审/合并 · 彻底停用:systemctl --user stop huakai-dispatcher</span></div></div>';
 var ev=(ds.events||[]);
 html+='<div class=card><h2>📜 最近动作(调度日志,免命令)</h2><div class=ev>'+
   (ev.length?ev.map(function(e){return '<div class=e><span class=t>'+esc((e.ts||'').replace('T',' ').slice(5,19))+'</span> <span class=k>['+esc(e.kind)+']</span> '+esc(e.text);}).join(''):'<div class=sub>(暂无)</div>')+
   '</div></div>';
 var cnt={};tasks.forEach(function(t){cnt[t.status]=(cnt[t.status]||0)+1;});
 var pills='<span class=pill>共 '+tasks.length+'</span>'+STAT.map(function(s){return cnt[s]?'<span class="pill s-'+s+'">'+s+' '+cnt[s]+'</span>':'';}).join('');
 var groups={};tasks.forEach(function(t){var k=t.assignee||'(未分配)';(groups[k]=groups[k]||[]).push(t);});
 var keys=Object.keys(groups).sort();
 var board=keys.map(function(k){return '<div class=grp><h3>'+esc(k)+' · '+groups[k].length+'</h3>'+groups[k].map(card).join('')+'</div>';}).join('');
 html+='<div class=card><h2>📋 任务账本</h2>'+pills+'<div style="margin-top:8px">'+(board||'<div class=sub>(还没有任务)</div>')+'</div></div>';
 var lk=locks.filter(function(l){return l.files&&l.files.length;});
 html+='<div class=card><h2>🔧 谁在编辑(实时文件锁)</h2>'+
   (lk.length?lk.map(function(l){return '<div class=lock><span class=who>'+esc(l.agent)+'</span> · '+esc(l.core_feature||'')+' · <span class=files>'+esc((l.files||[]).join(', '))+'</span></div>';}).join(''):'<div class=sub>(当前无人在编辑)</div>')+'</div>';
 document.getElementById('app').innerHTML=html;
}
function card(t){
 var v=(t.verify_rounds>=3)?'<span class="b on">✓3</span>':'<span class="b warn">'+(t.verify_rounds||0)+'/3</span>';
 var risk=t.risk?'<span class="b off">'+esc(t.risk)+'</span>':'';
 var ap=(t.status==='needs_owner')?'<div style="margin-top:6px"><button class="btn go" data-approve="'+esc(t.id)+'">✅ 批准(Owner 令牌)</button></div>':'';
 return '<div class=task><div class=row><span class=id>'+esc(t.id)+'</span><span style="flex:1">'+esc(t.title)+'</span>'+
  (t.wave?'<span class="b s-todo">'+esc(t.wave)+'</span>':'')+'<span class="b s-'+esc(t.status)+'">'+esc(t.status)+'</span>'+v+risk+'</div>'+
  (t.review_notes?'<div class=meta>↩ '+esc(t.review_notes)+(t.reviewed_by?' ('+esc(t.reviewed_by)+')':'')+'</div>':'')+
  (t.notes?'<div class=meta>📝 '+esc(t.notes)+'</div>':'')+
  ((t.scope_files&&t.scope_files.length)?'<div class=files>'+esc(t.scope_files.join(', '))+'</div>':'')+ap+'</div>';
}
setInterval(tick,5000); tick();
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
