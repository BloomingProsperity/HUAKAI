#!/usr/bin/env python3
"""Multi-AI parallel-edit coordination core (see .coordination/README.md).

Per-agent lock files in locks/<agent>.json avoid the meta-collision of a single
shared status file. Subcommands: claim | check | release.
"""
import sys, os, json, re, datetime, urllib.request, urllib.error, urllib.parse, ssl

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
    code, obj = _http("POST", "/claim", {"agent": agent, "files": files,
                                         "core_feature": feature, "purpose": purpose})
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
    print(f"unknown subcommand: {cmd}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
