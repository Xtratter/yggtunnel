#!/bin/bash
# YggTunnel server store: what the server keeps for the app, in plain JSON under /etc/yggtunnel (root only).
#   ACTION=manifest — (re)write /etc/yggtunnel/server.json, the description of this server: Yggdrasil address
#                     and listeners, WireGuard port, key and devices, the wss wrapper, the speed test, IPv6.
#                     WSS_PEER (optional) — the wrapper address the app knows, when the server has none yet.
#   ACTION=catalog  — the public peer list (publicpeers.neilalexander.dev), fetched by the server for a phone
#                     that cannot reach it; compact: {country: {uri: {"up": …, "r": reliability}}}.
#   ACTION=put      — save a phone's settings: NAME, CFG_B64 (base64 JSON) → clients/<NAME>.json
#   ACTION=list     — the saved phone settings: name, date, size
#   ACTION=get      — one of them: NAME
#   ACTION=desec    — a name for the wrapper's site at deSEC (desec.io), done from the server (it always has
#                     internet): DESEC_TOKEN, DESEC_OP=list (the account's domains) | claim (DESEC_NAME —
#                     a new name.dedyn.io or a name under one of the account's domains; its A record → DESEC_IP,
#                     else this server's own IPv4). DESEC_API overrides the API (tests).
#   ACTION=diag     — keep a phone's diagnostics log: NAME, DATA_B64 → diag/<NAME>-<time>.log (the last 20)
# Last line: YGGTUNNEL_RESULT {…}. STORE_DIR overrides the directory (local tests).
set -euo pipefail
DIR=${STORE_DIR:-/etc/yggtunnel}
[ -n "${STORE_DIR:-}" ] || [ "$(id -u)" = 0 ] || { echo "error: root required"; exit 1; }
mkdir -p "$DIR/clients"; chmod 700 "$DIR" "$DIR/clients"
export DIR
exec python3 - <<'PY'
import base64, datetime, json, os, re, subprocess, sys, urllib.request

DIR = os.environ["DIR"]
ACTION = os.environ.get("ACTION", "manifest")

def result(obj):
    print("YGGTUNNEL_RESULT " + json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)
    sys.exit(0)

def fail(s):
    print("error: " + s, flush=True); sys.exit(1)

def run(*cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=20).stdout
    except Exception:
        return ""

def write(path, text):
    tmp = path + ".new"
    with open(os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
        f.write(text)
    os.replace(tmp, path)

def name_ok(n):
    return re.fullmatch(r"[A-Za-z0-9._-]{1,64}", n or "") is not None

def manifest():
    m = {"app": "yggtunnel", "updated": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")}
    try:
        c = json.load(open("/etc/yggdrasil/yggdrasil.conf"))
        addr = run("yggdrasil", "-useconffile", "/etc/yggdrasil/yggdrasil.conf", "-address").strip()
        m["yggdrasil"] = {"address": addr, "listen": c.get("Listen", []), "peers": c.get("Peers", [])}
    except Exception:
        pass
    wg = "/etc/wireguard"
    if os.path.exists(f"{wg}/wgygg.conf"):
        conf = open(f"{wg}/wgygg.conf").read()
        port = re.search(r"ListenPort\s*=\s*(\d+)", conf)
        pub = ""
        try:
            pub = subprocess.run(["wg", "pubkey"], input=open(f"{wg}/wgygg.key").read(), capture_output=True, text=True).stdout.strip()
        except Exception:
            pass
        devices = []
        try:
            for line in open(f"{wg}/wgygg.peers"):
                parts = line.split(None, 2)
                if len(parts) >= 2:
                    devices.append({"name": parts[2].strip() if len(parts) > 2 else "", "publicKey": parts[0],
                                    "ip4": f"10.66.66.{parts[1]}", "ip6": f"fd66:66::{parts[1]}"})
        except Exception:
            pass
        m["wireguard"] = {"interface": "wgygg", "port": int(port.group(1)) if port else None, "publicKey": pub,
                          "subnet4": "10.66.66.0/24", "subnet6": "fd66:66::/64", "devices": devices}
    m["ipv6Internet"] = bool(run("ip", "-6", "route", "show", "default").strip())
    m["publicIPv4"] = (re.findall(r"src (\S+)", run("ip", "-4", "route", "get", "1.1.1.1")) or [None])[0]
    url = f"{DIR}/wrapper.url"
    if not os.path.exists(url) and os.environ.get("WSS_PEER", "").startswith("wss://"):
        write(url, os.environ["WSS_PEER"] + "\n")
    if os.path.exists(url):
        m["wrapper"] = {"wssPeer": open(url).read().strip()}
    env = f"{DIR}/speed.env"
    if os.path.exists(env):
        kv = dict(l.strip().split("=", 1) for l in open(env) if "=" in l)
        m["speed"] = {"port": int(kv.get("SPEED_PORT", 21446)), "token": kv.get("SPEED_TOKEN", ""), "capMB": int(kv.get("SPEED_CAP_MB", 1024))}
    m["clients"] = sorted(f[:-5] for f in os.listdir(f"{DIR}/clients") if f.endswith(".json"))
    write(f"{DIR}/server.json", json.dumps(m, indent=2, ensure_ascii=False) + "\n")
    return m

if ACTION == "manifest":
    result(manifest())

if ACTION == "catalog":
    req = urllib.request.Request("https://publicpeers.neilalexander.dev/publicnodes.json", headers={"User-Agent": "YggTunnel"})
    try:
        data = json.load(urllib.request.urlopen(req, timeout=20))
    except Exception as e:
        fail(f"the server could not get the catalog either: {e}")
    out = {}
    for country, peers in data.items():
        for uri, p in peers.items():
            st = p.get("states", "")
            out.setdefault(country, {})[uri] = {"up": bool(p.get("up")), "r": round(st.count("*") / len(st), 3) if st else 0}
    print(f"Catalog: {sum(len(v) for v in out.values())} peers", flush=True)
    result(out)

if ACTION == "put":
    name = os.environ.get("NAME", "")
    if not name_ok(name): fail("bad name")
    try:
        cfg = json.loads(base64.b64decode(os.environ.get("CFG_B64", "")))
    except Exception:
        fail("bad settings data")
    if cfg.get("app") != "yggtunnel": fail("not YggTunnel settings")
    write(f"{DIR}/clients/{name}.json", json.dumps(cfg, indent=2, ensure_ascii=False) + "\n")
    manifest()
    print(f"Saved {DIR}/clients/{name}.json", flush=True)
    result({"saved": name})

if ACTION == "list":
    items = []
    for f in sorted(os.listdir(f"{DIR}/clients")):
        if not f.endswith(".json"): continue
        p = f"{DIR}/clients/{f}"
        try: created = json.load(open(p)).get("created", 0)
        except Exception: created = 0
        items.append({"name": f[:-5], "created": created, "size": os.path.getsize(p)})
    result({"clients": items})

if ACTION == "get":
    name = os.environ.get("NAME", "")
    if not name_ok(name) or not os.path.exists(f"{DIR}/clients/{name}.json"): fail("no such saved settings")
    result({"name": name, "config": json.load(open(f"{DIR}/clients/{name}.json"))})

if ACTION == "desec":
    import urllib.error
    tok = os.environ.get("DESEC_TOKEN", "")
    api = os.environ.get("DESEC_API", "https://desec.io/api/v1")
    if not tok: fail("no deSEC token")

    def call(method, path, body=None):
        req = urllib.request.Request(api + path, method=method,
                                     data=None if body is None else json.dumps(body).encode(),
                                     headers={"Authorization": "Token " + tok, "Content-Type": "application/json",
                                              "User-Agent": "YggTunnel"})
        try:
            with urllib.request.urlopen(req, timeout=20) as r:
                t = r.read()
                return json.loads(t) if t else None
        except urllib.error.HTTPError as e:
            if e.code in (401, 403): fail("deSEC: the token is not valid")
            msg = e.read().decode("utf-8", "replace")[:300]
            fail(f"deSEC: HTTP {e.code} {msg}")
        except Exception as e:
            fail(f"deSEC is not reachable from the server: {e}")

    owned = [d["name"] for d in (call("GET", "/domains/") or [])]
    if os.environ.get("DESEC_OP", "list") == "list":
        result({"domains": owned})
    name = os.environ.get("DESEC_NAME", "").strip().lower().rstrip(".")
    if not re.fullmatch(r"[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+", name):
        fail(f"not a domain name: {name}")
    base = next((d for d in sorted(owned, key=len, reverse=True) if name == d or name.endswith("." + d)), None)
    if base is None:
        if not re.fullmatch(r"[a-z0-9]([a-z0-9-]*[a-z0-9])?\.dedyn\.io", name):
            fail("choose a new name like myvpn.dedyn.io" + (" or one under your domains: " + ", ".join(owned) if owned else ""))
        print(f"Registering {name} at deSEC", flush=True)
        call("POST", "/domains/", {"name": name})
        base = name
    sub = name[:-len(base)].rstrip(".") if name != base else ""
    ip = os.environ.get("DESEC_IP", "").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+\.\d+", ip):
        ip = (re.findall(r"src (\S+)", run("ip", "-4", "route", "get", "1.1.1.1")) or [""])[0]
    if not ip: fail("could not tell this server's IPv4 address")
    ttl = max(int((call("GET", f"/domains/{base}/") or {}).get("minimum_ttl", 3600)), 60)
    print(f"A record: {name} → {ip} (TTL {ttl} s)", flush=True)
    call("PATCH", f"/domains/{base}/rrsets/", [{"subname": sub, "type": "A", "ttl": ttl, "records": [ip]}])
    result({"domain": name, "ip": ip, "ttl": ttl})

if ACTION == "diag":
    name = os.environ.get("NAME", "")
    if not name_ok(name): fail("bad name")
    try:
        text = base64.b64decode(os.environ.get("DATA_B64", "")).decode("utf-8", "replace")
    except Exception:
        fail("bad data")
    d = f"{DIR}/diag"
    os.makedirs(d, mode=0o700, exist_ok=True)
    fname = f"{name}-{datetime.datetime.now().strftime('%Y%m%d-%H%M%S')}.log"
    write(f"{d}/{fname}", text)
    for old in sorted(os.listdir(d))[:-20]:
        os.remove(f"{d}/{old}")
    print(f"Saved {d}/{fname}", flush=True)
    result({"saved": fname})

fail(f"unknown action {ACTION}")
PY
