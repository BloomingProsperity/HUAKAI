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


class H(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

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
        if not self._auth():
            return
        c = db()
        try:
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
