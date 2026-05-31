#!/usr/bin/env python3
"""Stream any AI's combined stdout/stderr to the HUAKAI console live terminal.

So the Owner can SEE what a machine is doing in /console 「机器」tab (select the
agent). Tees stdin -> stdout (keeps local echo) and batch-POSTs new lines to
/dispatcher/output {agent, lines:[...]}.

Setup on the machine whose output you want visible (e.g. local-codex on Windows):
    # export the same coord creds the dispatcher/worker uses (from client.env):
    #   COORD_URL, COORD_TOKEN, COORD_CACERT
    codex exec goal "..." 2>&1 | python3 push-stdout.py local-codex
    # (any command works:  <your-ai-cmd> 2>&1 | python3 push-stdout.py <agent-name>)

Cross-platform (pure stdlib). Best-effort: network failures never kill the pipe.
"""
import os, sys, json, ssl, urllib.request, time, threading

AGENT = sys.argv[1] if len(sys.argv) > 1 else "local-codex"
url = os.environ["COORD_URL"]; tok = os.environ["COORD_TOKEN"]; ca = os.environ.get("COORD_CACERT", "")
ctx = ssl.create_default_context(cafile=ca) if ca else ssl.create_default_context()
op = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ctx))

buf = []; lock = threading.Lock()

def flush():
    with lock:
        if not buf:
            return
        lines = buf[:]; buf.clear()
    try:
        body = json.dumps({"agent": AGENT, "lines": lines}).encode()
        op.open(urllib.request.Request(url + "/dispatcher/output", data=body,
                headers={"Authorization": "Bearer " + tok, "Content-Type": "application/json"}), timeout=12)
    except Exception:
        pass  # best-effort; the pipe keeps flowing regardless

def ticker():
    while True:
        time.sleep(1.5); flush()

threading.Thread(target=ticker, daemon=True).start()
try:
    for line in sys.stdin:
        sys.stdout.write(line); sys.stdout.flush()      # keep local echo
        with lock:
            buf.append(line.rstrip("\n")[:2000])         # cap line length
except KeyboardInterrupt:
    pass
flush()
