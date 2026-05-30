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
        if not self._auth():
            return
        c = db()
        try:
            if u.path == "/tree/data":
                return self._serve_file(TREE_JSON, "application/json; charset=utf-8")
            if u.path == "/board":
                return self._send(200, {"locks": live_locks(c)})
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
