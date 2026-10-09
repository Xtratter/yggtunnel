#!/bin/bash
# YggTunnel device admin on a set-up server: list / add / rename / remove WireGuard devices,
# and their profiles: the devices' private keys are kept here (wgygg.keys, root only), so any
# admin phone can show a device's QR code again. Changes apply live (wg set), no restarts.
# /etc/wireguard/wgygg.peers: "<public key> <n> [name]"; wgygg.keys: "<public key> <private key>".
# Input (env): ACTION=list|add|rename|remove|store-key|export, CLIENT_PUB, CLIENT_PRIV, NAME_B64 (base64 UTF-8 name).
# Last line: YGGTUNNEL_RESULT {"devices":[…]}.
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "error: root required"; exit 1; }
[ -s /etc/wireguard/wgygg.conf ] || { echo "error: the server is not set up yet"; exit 1; }
exec python3 - <<'PY'
import base64, json, os, subprocess, sys, time

WG = "/etc/wireguard"; PEERS = WG + "/wgygg.peers"; CONF = WG + "/wgygg.conf"; KEYS = WG + "/wgygg.keys"
act = os.environ.get("ACTION", "list")
pub = os.environ.get("CLIENT_PUB", "")
priv = os.environ.get("CLIENT_PRIV", "")
name = " ".join(base64.b64decode(os.environ.get("NAME_B64", "")).decode("utf-8", "replace").split())[:40]

conf = open(CONF).read()
v6 = "fd66:66::1/64" in conf
devs = []
for line in open(PEERS).read().splitlines():
    f = line.split(None, 2)
    if len(f) >= 2:
        devs.append({"pub": f[0], "n": int(f[1]), "name": f[2] if len(f) > 2 else ""})
by = {d["pub"]: d for d in devs}

keys = {}
if os.path.exists(KEYS):
    for line in open(KEYS).read().splitlines():
        f = line.split()
        if len(f) == 2:
            keys[f[0]] = f[1]

def save_keys():
    fd = os.open(KEYS + ".new", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        for k, v in keys.items():
            f.write(f"{k} {v}\n")
    os.replace(KEYS + ".new", KEYS)

def check_priv():
    """The private key must belong to CLIENT_PUB."""
    r = subprocess.run(["wg", "pubkey"], input=priv, capture_output=True, text=True)
    if r.returncode or r.stdout.strip() != pub:
        sys.exit("error: the private key does not match the device")

def ips(n):
    return [f"10.66.66.{n}/32"] + ([f"fd66:66::{n}/128"] if v6 else [])

def wg(*args):
    r = subprocess.run(["wg", *args], capture_output=True, text=True)
    if r.returncode:
        sys.exit("error: wg: " + (r.stderr or r.stdout).strip())

def save():
    with open(PEERS + ".new", "w") as f:
        for d in devs:
            f.write(f"{d['pub']} {d['n']} {d['name']}".rstrip() + "\n")
    os.replace(PEERS + ".new", PEERS)
    iface = conf.split("\n[Peer]")[0].rstrip("\n") + "\n"
    body = iface + "".join(f"\n[Peer]\nPublicKey = {d['pub']}\nAllowedIPs = {', '.join(ips(d['n']))}\n" for d in devs)
    fd = os.open(CONF + ".new", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        f.write(body)
    os.replace(CONF + ".new", CONF)

result = {}
if act in ("add", "rename", "remove", "store-key", "export") and not pub:
    sys.exit("error: no device key")
if act == "add":
    if pub in by:
        if name:
            by[pub]["name"] = name; save()
        print(f"Device already known: {by[pub]['n']}")
    else:
        n = max([1] + [d["n"] for d in devs]) + 1
        if n > 254:
            sys.exit("error: no free addresses")
        # live first: if wg refuses the key, nothing is written
        wg("set", "wgygg", "peer", pub, "allowed-ips", ",".join(ips(n)))
        if priv:
            check_priv()
        devs.append({"pub": pub, "n": n, "name": name}); save()
        print(f"Added device {n}: {name}")
    if priv:
        keys[pub] = priv; save_keys()
elif act == "rename":
    if pub not in by:
        sys.exit("error: no such device")
    by[pub]["name"] = name; save()
    print(f"Renamed device {by[pub]['n']}: {name}")
elif act == "remove":
    if pub in by:
        wg("set", "wgygg", "peer", pub, "remove")
        devs = [d for d in devs if d["pub"] != pub]; save()
        print("Removed")
    if keys.pop(pub, None):
        save_keys()
elif act == "store-key":
    if pub not in by:
        sys.exit("error: no such device")
    check_priv()
    keys[pub] = priv; save_keys()
    print(f"Profile saved for device {by[pub]['n']}")
elif act == "export":
    if pub not in keys:
        sys.exit("error: no profile for this device on the server")
    result["privateKey"] = keys[pub]

dump = subprocess.run(["wg", "show", "wgygg", "dump"], capture_output=True, text=True).stdout.splitlines()[1:]
stats = {f[0]: f for f in (l.split("\t") for l in dump) if len(f) >= 7}
now = int(time.time())
out = []
for d in devs:
    f = stats.get(d["pub"])
    hs = int(f[4]) if f else 0
    out.append({"pub": d["pub"], "n": d["n"], "name": d["name"], "ip4": f"10.66.66.{d['n']}", "hasKey": d["pub"] in keys,
                "handshakeAgo": now - hs if hs else -1,
                "rx": int(f[5]) if f else 0, "tx": int(f[6]) if f else 0})
result["devices"] = out
print("YGGTUNNEL_RESULT " + json.dumps(result, ensure_ascii=False))
PY
