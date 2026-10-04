#!/bin/bash
# YggTunnel: the wss wrapper — Yggdrasil peering inside HTTPS to a site on this server.
# Yggdrasil listens on ws://127.0.0.1:21444 (local only); the site's nginx proxies one secret
# path to it. To the network it is ordinary HTTPS to the site.
# telemt on 443 relays non-MTProto traffic to the site with limits made for web pages (5 s idle,
# 60 s, 5 MB) that cut a tunnel; with WRAP_TELEMT=1 they are raised (else the app is asked).
# 0.8.1 put a WebSocket→TCP bridge in between by mistake (telemt was the cause); setup removes it.
#
# ACTION=setup  — find the HTTPS site in nginx (behind 443 directly or behind a proxy on 443
#                 such as telemt/xray/haproxy) and add the location; if 443 and 80 are free and
#                 WRAP_DOMAIN + WRAP_NEW=1 are given, create a site with a Let's Encrypt certificate.
# ACTION=remove — take the wrapper out again.
# Input (env): WRAP_DOMAIN (which site, when there are several / for a new site), WRAP_NEW=1,
#              WRAP_IP (the server address the app uses — for the final check from outside).
# Every changed file is backed up to /var/backups/yggtunnel/<time>/; any failure restores them.
# Running again keeps the same path.
# Last line: YGGTUNNEL_RESULT {"wssPeer": …} or {"need": "domain"|"new-domain", …}.
# Note: with pipefail never end a pipe early (head, grep -q, awk exit) — SIGPIPE (141).
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "error: root required"; exit 1; }
[ -s /etc/yggdrasil/yggdrasil.conf ] || { echo "error: the server is not set up yet (no Yggdrasil)"; exit 1; }
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_SUSPEND=1 NEEDRESTART_MODE=a
exec python3 - <<'PY'
import json, os, re, secrets, shutil, socket, subprocess, sys, time

ACTION = os.environ.get("ACTION", "setup")
DOMAIN = os.environ.get("WRAP_DOMAIN", "").strip().lower().rstrip(".")
NEW = os.environ.get("WRAP_NEW") == "1"
APP_IP = os.environ.get("WRAP_IP", "").strip("[]")
# paths and commands can be overridden for tests
NGINX = os.environ.get("WRAP_NGINX", "nginx").split()
INC = os.environ.get("WRAP_INCLUDE", "/etc/nginx/yggtunnel.conf")
YCONF = os.environ.get("WRAP_YCONF", "/etc/yggdrasil/yggdrasil.conf")
LIVE = os.environ.get("WRAP_LIVE", "1") == "1"   # 0 in tests: no systemctl/apt/certbot/curl
# backups go to their own folder: next to the file nginx could load them too (sites-enabled/*)
BACKUPS = os.environ.get("WRAP_BACKUPS", "/var/backups/yggtunnel")
WS = "ws://127.0.0.1:21444"           # Yggdrasil's own local WebSocket listener
OLD_WS = "tcp://127.0.0.1:21445"      # 0.8.1: behind the bridge, replaced
TELEMT_FIX = os.environ.get("WRAP_TELEMT") == "1"
TELEMT_CONF = os.environ.get("WRAP_TELEMT_CONF", "/etc/telemt/telemt.toml")
TELEMT_KEYS = {"mask_relay_idle_timeout_ms": 300000, "mask_relay_timeout_ms": 86400000, "mask_relay_max_bytes": 0}
BRIDGE = os.environ.get("WRAP_BRIDGE", "/usr/local/lib/yggtunnel/wsbridge.py")
UNIT = os.environ.get("WRAP_UNIT", "/etc/systemd/system/yggtunnel-wsbridge.service")
STAMP = time.strftime("%Y%m%d-%H%M%S")
backups = []  # (original path, backup path or None if the file did not exist)

def say(s): print("== " + s, flush=True)
def fail(s):
    restore()
    print("error: " + s, flush=True); sys.exit(1)
def run(cmd, check=True, **kw):
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, **kw)
    except FileNotFoundError:
        if check: fail(f"{cmd[0]} is not installed")
        return subprocess.CompletedProcess(cmd, 127, "", "")
    if check and r.returncode:
        fail(f"{' '.join(cmd)}: {(r.stderr or r.stdout).strip()[-600:]}")
    return r
def result(obj):
    if obj.get("wssPeer"):  # for the server's own description (store.sh → /etc/yggtunnel/server.json)
        try:
            os.makedirs("/etc/yggtunnel", mode=0o700, exist_ok=True)
            with open("/etc/yggtunnel/wrapper.url", "w") as f: f.write(obj["wssPeer"] + "\n")
        except OSError:
            pass
    print("YGGTUNNEL_RESULT " + json.dumps(obj, ensure_ascii=False), flush=True); sys.exit(0)

def backup(path):
    if any(p == path for p, _ in backups): return
    if os.path.exists(path):
        d = os.path.join(BACKUPS, STAMP); os.makedirs(d, mode=0o700, exist_ok=True)
        b = os.path.join(d, path.strip("/").replace("/", "%"))
        shutil.copy2(path, b); backups.append((path, b)); print(f"Backup: {b}")
    else:
        backups.append((path, None))
def restore():
    for path, b in reversed(backups):
        if b: shutil.copy2(b, path)
        elif os.path.exists(path): os.remove(path)
    if backups:
        print("Restored the previous files")
        if LIVE: subprocess.run(["systemctl", "reload", "nginx"]); subprocess.run(["systemctl", "restart", "yggdrasil"])
    backups.clear()
def write(path, text):
    if os.path.exists(path) and open(path).read() == text: return
    backup(path)
    mode = os.stat(path).st_mode & 0o777 if os.path.exists(path) else 0o644
    with open(path, "w") as f: f.write(text)
    os.chmod(path, mode)

# ---- nginx config: the files nginx really loads (nginx -T), and their server blocks ----
def nginx_files():
    r = run(NGINX + ["-T"], check=False)
    if r.returncode: return None
    files, cur, buf = {}, None, []
    for line in r.stdout.splitlines(keepends=True):
        m = re.match(r"# configuration file (.+):\s*$", line)
        if m:
            if cur: files[cur] = "".join(buf)
            cur, buf = m.group(1), []
        elif cur: buf.append(line)
    if cur: files[cur] = "".join(buf)
    return {os.path.realpath(f): open(os.path.realpath(f)).read() for f in files if os.path.exists(f)}

def blocks(text):
    """server { … } blocks: (index right after '{', body text, parent block name)."""
    out, stack, word, i, n = [], [], "", 0, len(text)
    while i < n:
        c = text[i]
        if c == "#":
            while i < n and text[i] != "\n": i += 1
            continue
        if c in "\"'":
            q = c; i += 1
            while i < n and text[i] != q: i += 2 if text[i] == "\\" else 1
            i += 1; continue
        if c == "{":
            name = word.split()[0] if word.split() else ""
            stack.append((name, i + 1)); word = ""
        elif c == "}":
            if stack:
                name, start = stack.pop()
                if name == "server":
                    out.append((start, text[start:i], stack[-1][0] if stack else ""))
            word = ""
        elif c == ";": word = ""
        else: word += c
        i += 1
    return out

def directives(body, name):
    """Values of top-level directives `name …;` in a block body (nested blocks skipped)."""
    vals, depth = [], 0
    for m in re.finditer(r"(?m)#[^\n]*|[{};]|^\s*(\w+)\s+([^;{}]*);", body):
        t = m.group(0)
        if t.startswith("#"): continue
        if t == "{": depth += 1
        elif t == "}": depth -= 1
        elif depth == 0 and m.group(1) == name: vals.append(m.group(2).strip())
    return vals

def is_domain(s):
    return "." in s and "*" not in s and not s.startswith("~") and not re.fullmatch(r"[\d.]+|\[?[0-9a-f:]+\]?", s)

def ssl_sites(files):
    sites = []
    for path, text in files.items():
        for start, body, parent in blocks(text):
            if parent == "stream": continue
            listens = directives(body, "listen")
            if not any(re.search(r"\bssl\b", l) for l in listens) or not directives(body, "ssl_certificate"): continue
            names = [x for v in directives(body, "server_name") for x in v.split() if is_domain(x)]
            if names:
                sites.append({"file": path, "start": start, "names": names, "listens": listens, "has": "yggtunnel" in body})
    return sites

def port_owner(port):
    if os.environ.get(f"WRAP_OWNER{port}") is not None:  # tests
        return os.environ[f"WRAP_OWNER{port}"] or None
    r = run(["ss", "-Htlnp", f"sport = :{port}"], check=False)
    m = re.search(r'users:\(\("([^"]+)"', r.stdout)
    return (m.group(1) if m else "?") if r.stdout.strip() else None

def secret_path():
    if os.path.exists(INC):
        m = re.search(r"location\s*=\s*/(\S+)\s*\{", open(INC).read())
        if m: return m.group(1)
    return secrets.token_urlsafe(18).replace("-", "x").replace("_", "y")

def include_conf(path):
    return f"""# YggTunnel: Yggdrasil peering over WebSocket (wss) through this site.
# Added by the YggTunnel app (Server → Wrapper through the site); removed by its Remove.
location = /{path} {{
    proxy_pass http://127.0.0.1:21444;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_read_timeout 1d;
    proxy_send_timeout 1d;
    proxy_buffering off;
    access_log off;
}}
"""

def nginx_ok():
    r = run(NGINX + ["-t"], check=False)
    if r.returncode: fail("nginx -t: " + (r.stderr or r.stdout).strip()[-600:])

def ygg_listen(add):
    c = json.load(open(YCONF))
    old = c.get("Listen", [])
    new = [l for l in old if l not in (WS, OLD_WS)] + ([WS] if add else [])
    if new == old: return False
    c["Listen"] = new
    write(YCONF, json.dumps(c, indent=2))
    return True

def remove_bridge():
    """0.8.1 left a WebSocket→TCP bridge (yggtunnel-wsbridge); not needed — take it away."""
    if not any(os.path.exists(p) for p in (BRIDGE, UNIT)): return False
    if LIVE: run(["systemctl", "disable", "--now", "yggtunnel-wsbridge"], check=False)
    for p in (UNIT, BRIDGE):
        if os.path.exists(p): backup(p); os.remove(p)
    if LIVE: run(["systemctl", "daemon-reload"], check=False)
    print("Removed the old WebSocket bridge (yggtunnel-wsbridge)")
    return True

def telemt_missing():
    """telemt's masking limits that cut long-lived connections, if still at their defaults."""
    try:
        import tomllib
        c = tomllib.load(open(TELEMT_CONF, "rb")).get("censorship", {})
    except Exception:
        return None  # no readable telemt config: nothing to say
    return [k for k, v in TELEMT_KEYS.items() if c.get(k) != v]

def telemt_fix(keys):
    s = open(TELEMT_CONF).read()
    if "[censorship]\n" not in s: s += "\n[censorship]\n"
    lines = "".join(f"{k} = {TELEMT_KEYS[k]}\n" for k in keys)
    s = re.sub(r"(?m)^(%s)\s*=.*\n" % "|".join(keys), "", s)
    s = s.replace("[censorship]\n", "[censorship]\n# YggTunnel: long-lived connections to the site for the wss wrapper\n" + lines, 1)
    write(TELEMT_CONF, s)
    if LIVE:
        run(["systemctl", "restart", "telemt"])
        for _ in range(20):
            if port_owner(443) == "telemt": break
            time.sleep(1)
        else: fail("telemt did not come back after the change — restored")
    say("telemt: masking limits raised (" + ", ".join(keys) + ")")

def handshake(domain, path):
    """The WebSocket upgrade through the public address (as the phone will do it), 101 = OK."""
    ips = []
    for h in [APP_IP]:
        try:
            if h: ips.append(socket.getaddrinfo(h, 443, socket.AF_INET)[0][4][0])
        except OSError: pass
    ips.append("127.0.0.1")
    for ip in dict.fromkeys(ips):
        time.sleep(2)  # some proxies on 443 (telemt) limit quick repeated connections
        r = run(["curl", "-s", "-o", "/dev/null", "-m", "5", "-w", "%{http_code}", "--http1.1",
                 "-H", "Connection: Upgrade", "-H", "Upgrade: websocket", "-H", "Sec-WebSocket-Version: 13",
                 "-H", "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==", "-H", "Sec-WebSocket-Protocol: ygg-ws",
                 "--resolve", f"{domain}:443:{ip}", f"https://{domain}:443/{path}"], check=False)
        print(f"Handshake via {ip}: {r.stdout or r.returncode}")
        if r.stdout == "101": return True
    return False

def apply_live(domain, path, ygg_changed):
    """Reload / restart only what changed (a restart of Yggdrasil drops the tunnels for a moment), then check."""
    if not LIVE: return
    if any(p not in (YCONF, BRIDGE, UNIT, TELEMT_CONF) for p, _ in backups): run(["systemctl", "reload", "nginx"])
    if ygg_changed: run(["systemctl", "restart", "yggdrasil"])
    for _ in range(10):
        if port_owner(21444) == "yggdrasil": break
        time.sleep(1)
    else: fail("Yggdrasil does not listen on 127.0.0.1:21444")
    if not handshake(domain, path):
        fail(f"https://{domain}/… does not reach Yggdrasil from outside — the change is rolled back")

# ---- remove --------------------------------------------------------------------
if ACTION == "remove":
    files = nginx_files() or {}
    for path, text in files.items():
        if "yggtunnel.conf" in text:
            write(path, re.sub(r"[ \t]*include\s+\S*yggtunnel\.conf;[^\n]*\n", "", text))
    if os.path.exists(INC): backup(INC); os.remove(INC)
    if files: nginx_ok()
    changed = ygg_listen(False)
    remove_bridge()
    if LIVE:
        if files: run(["systemctl", "reload", "nginx"])
        if changed: run(["systemctl", "restart", "yggdrasil"])
        run(["systemctl", "daemon-reload"], check=False)
    say("Wrapper removed")
    result({"removed": True})

# ---- setup ---------------------------------------------------------------------

def syn_burst_443():
    """The smallest --hashlimit-burst of an iptables rule limiting new connections to port 443 per address
    (mtpr-synfix and the like), or None. With burst 1 the phone's wss reconnects were refused (02.10)."""
    out = run([os.environ.get("WRAP_IPTABLES", "iptables"), "-S"], check=False).stdout
    bursts = [int(m) for l in out.splitlines() if "--dport 443" in l and "hashlimit" in l
              for m in re.findall(r"--hashlimit-burst (\d+)", l)]
    return min(bursts) if bursts else None

owner443 = port_owner(443)
say(f"Port 443: {owner443 or 'free'}")
burst = syn_burst_443()
if burst is not None and burst < 5:
    say(f"Warning: a firewall rule lets only {burst} new connection(s) to port 443 through per address at once "
        f"(--hashlimit-burst {burst}, e.g. mtpr-synfix); the phone's wss reconnects may be refused. "
        "Raise it to 10 in that service's script and restart it")
files = nginx_files()
if files is None and shutil.which(NGINX[0]):
    fail("nginx is installed but its configuration has errors (nginx -T) — fix it first")
sites = ssl_sites(files or {})
if sites:
    print("HTTPS sites in nginx: " + ", ".join(f"{s['names'][0]} ({' / '.join(s['listens'])})" for s in sites))

if sites and not NEW:
    if owner443 and owner443 != "nginx":
        say(f"443 belongs to {owner443}; using the nginx site behind it")
    if owner443 == "telemt":
        missing = telemt_missing()
        if missing and not TELEMT_FIX:
            result({"need": "telemt", "keys": missing})
    pick = [s for s in sites if DOMAIN in s["names"]] if DOMAIN else sites
    if DOMAIN and not pick: fail(f"no HTTPS site {DOMAIN} in nginx")
    names = sorted({s["names"][0] for s in pick})
    if len(names) > 1:
        result({"need": "domain", "candidates": names})
    domain = DOMAIN or names[0]
    path = secret_path()
    say(f"Site: {domain}")
    write(INC, include_conf(path))
    # insert into every block of this site, last block first so earlier offsets stay valid
    for f in sorted({s["file"] for s in pick if domain in s["names"] and not s["has"]}):
        text = files[f]
        for start in sorted((s["start"] for s in pick if s["file"] == f and domain in s["names"] and not s["has"]), reverse=True):
            text = text[:start] + f"\n    include {INC};  # YggTunnel wss peering" + text[start:]
        if open(f).read() != files[f]: fail(f"{f} changed while working")
        write(f, text)
        print(f"Include added to {f}")
    nginx_ok()
    remove_bridge()
    yc = ygg_listen(True)
    apply_live(domain, path, yc)
    if owner443 == "telemt" and TELEMT_FIX and telemt_missing():
        telemt_fix(telemt_missing())
    say("Wrapper is ready" if backups else "Wrapper was already set up — nothing changed")
    result({"wssPeer": f"wss://{domain}:443/{path}", "domain": domain, "mode": "existing"})

# no HTTPS site in nginx: a new one, only on a clean 443/80
if owner443:
    fail(f"443 is taken by {owner443} and there is no HTTPS site in nginx behind it — "
         "set the wrapper up by hand (a WebSocket location to ws://127.0.0.1:21444 in your web server)")
if not DOMAIN or not NEW:
    my_v4 = (re.findall(r"src (\S+)", run(["ip", "-4", "route", "get", "1.1.1.1"], check=False).stdout) or [""])[0]
    result({"need": "new-domain", "ip": my_v4})
owner80 = port_owner(80)
if owner80 and owner80 != "nginx":
    fail(f"port 80 is taken by {owner80}: Let's Encrypt needs it for the certificate")
# the domain must point to this server; a name just created at deSEC (WRAP_WAIT_DNS=1) may take a while
my_ips = set(re.findall(r"inet (\d+\.\d+\.\d+\.\d+)", run(["ip", "-4", "addr"], check=False).stdout))
if APP_IP:
    try: my_ips |= {a[4][0] for a in socket.getaddrinfo(APP_IP, 443, socket.AF_INET)}
    except OSError: pass
wait_until = time.time() + (int(os.environ.get("WRAP_WAIT_S", "180")) if os.environ.get("WRAP_WAIT_DNS") == "1" else 0)
dom_ips = set()
while True:
    try: dom_ips = {a[4][0] for a in socket.getaddrinfo(DOMAIN, 443, socket.AF_INET)}
    except OSError: dom_ips = set()
    if dom_ips & my_ips or time.time() >= wait_until: break
    say(f"Waiting for {DOMAIN} to point to this server…")
    time.sleep(10)
if not dom_ips:
    fail(f"{DOMAIN} does not resolve — create the DNS A record first")
if not dom_ips & my_ips:
    fail(f"{DOMAIN} points to {', '.join(sorted(dom_ips))}, not to this server ({', '.join(sorted(my_ips - {'127.0.0.1'}))})")
if LIVE:
    say("Installing nginx and certbot")
    run(["apt-get", "update", "-qq"]); run(["apt-get", "install", "-y", "-qq", "nginx", "certbot"])
    if shutil.which("ufw") and "Status: active" in run(["ufw", "status"], check=False).stdout:
        run(["ufw", "allow", "80/tcp"]); run(["ufw", "allow", "443/tcp"])
web = os.environ.get("WRAP_WEB", "/var/www/yggtunnel")
os.makedirs(web + "/.well-known/acme-challenge", exist_ok=True)
if not os.path.exists(web + "/index.html"):
    open(web + "/index.html", "w").write("<!doctype html><title>Welcome</title><h1>It works.</h1>\n")
etc = os.environ.get("WRAP_ETC", "/etc/nginx")
site_dir = etc + "/sites-available" if os.path.isdir(etc + "/sites-enabled") else etc + "/conf.d"
site = site_dir + ("/yggtunnel-site" if site_dir.endswith("available") else "/yggtunnel-site.conf")
http_block = f"""server {{
    listen 80;
    listen [::]:80;
    server_name {DOMAIN};
    location /.well-known/acme-challenge/ {{ root {web}; }}
    location / {{ return 301 https://$host$request_uri; }}
}}
"""
write(site, "# YggTunnel: a site for the wss wrapper (created by the YggTunnel app)\n" + http_block)
if site_dir.endswith("available"):
    link = etc + "/sites-enabled/yggtunnel-site"
    if not os.path.exists(link): backups.append((link, None)); os.symlink(site, link)
nginx_ok()
if LIVE:
    run(["systemctl", "enable", "--now", "nginx"]); run(["systemctl", "reload", "nginx"])
    say(f"Getting a Let's Encrypt certificate for {DOMAIN}")
    run(["certbot", "certonly", "--webroot", "-w", web, "-d", DOMAIN, "--agree-tos",
         "--register-unsafely-without-email", "--non-interactive", "--deploy-hook", "systemctl reload nginx"])
cert = os.environ.get("WRAP_LE", "/etc/letsencrypt/live") + "/" + DOMAIN
path = secret_path()
write(INC, include_conf(path))
write(site, "# YggTunnel: a site for the wss wrapper (created by the YggTunnel app)\n" + http_block + f"""
server {{
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name {DOMAIN};
    include {INC};  # YggTunnel wss peering
    ssl_certificate     {cert}/fullchain.pem;
    ssl_certificate_key {cert}/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    server_tokens off;
    root {web};
    index index.html;
    location / {{ try_files $uri $uri/ =404; }}
}}
""")
nginx_ok()
yc = ygg_listen(True)
apply_live(DOMAIN, path, yc)
say("Site and wrapper are ready")
result({"wssPeer": f"wss://{DOMAIN}:443/{path}", "domain": DOMAIN, "mode": "new"})
PY
