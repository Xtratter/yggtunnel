#!/bin/bash
# YggTunnel server panel: a read-only snapshot of the server as JSON (ACTION=status, the default),
# or ACTION=upgrade — apt update + upgrade (asked for in the app), with the snapshot after it.
# Last line: YGGTUNNEL_RESULT {…}.
# Note: with pipefail never end a pipe early (head, grep -q, awk exit) — SIGPIPE (141).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_SUSPEND=1 NEEDRESTART_MODE=a
if [ "${ACTION:-status}" = upgrade ]; then
    [ "$(id -u)" = 0 ] || { echo "error: root required"; exit 1; }
    echo "== apt-get update"; apt-get update -qq
    echo "== apt-get upgrade"; apt-get -y -qq -o Dpkg::Options::=--force-confold upgrade
    echo "== Done"
fi
exec python3 - <<'PY'
import json, os, re, shutil, socket, subprocess, time

def run(*cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=20).stdout
    except Exception:
        return ""

def cpu_times():
    try:
        v = list(map(int, open("/proc/stat").readline().split()[1:]))
        return sum(v), v[3] + (v[4] if len(v) > 4 else 0)
    except Exception:  # every figure is optional: a missing one must not cost the whole panel
        return None, None

def wan():
    for line in run("ip", "-4", "route", "show", "default").splitlines():
        m = re.search(r"\bdev (\S+)", line)
        if m: return m.group(1)
    return None

def net(dev):
    try:
        for line in open("/proc/net/dev").read().splitlines()[2:]:
            name, data = line.split(":", 1)
            if name.strip() == dev:
                f = data.split(); return int(f[0]), int(f[8])
    except Exception:
        pass
    return 0, 0

dev = wan()
t1, i1 = cpu_times(); n1 = net(dev) if dev else (0, 0)
time.sleep(1)
t2, i2 = cpu_times(); n2 = net(dev) if dev else (0, 0)

mem = {}
try:
    for line in open("/proc/meminfo"):
        k, v = line.split(":"); mem[k] = int(v.split()[0]) * 1024
except Exception:
    pass
disk = shutil.disk_usage("/")
os_name = ""
try:
    os_name = dict(l.strip().split("=", 1) for l in open("/etc/os-release") if "=" in l).get("PRETTY_NAME", "").strip('"')
except Exception:
    pass

services = {}
for unit in ["yggdrasil", "wg-quick@wgygg", "yggtunnel-fw", "yggtunnel-wsbridge", "yggtunnel-speed", "nginx", "telemt", "ssh"]:
    try:
        exists = subprocess.run(["systemctl", "cat", unit], capture_output=True, timeout=10).returncode == 0
    except Exception:
        exists = False
    if exists:  # only the services this server really has
        services[unit] = run("systemctl", "is-active", unit).strip() or "unknown"

ygg = {}
try:
    peers = json.loads(run("yggdrasilctl", "-endpoint=unix:///run/yggdrasil/yggdrasil.sock", "-json", "getPeers") or "{}").get("peers", [])
    ygg = {"peers": len(peers), "up": sum(1 for p in peers if p.get("up"))}
    self_ = json.loads(run("yggdrasilctl", "-endpoint=unix:///run/yggdrasil/yggdrasil.sock", "-json", "getSelf") or "{}")
    ygg["address"] = self_.get("address", "")
except Exception:
    pass

wg = {}
dump = run("wg", "show", "wgygg", "dump").splitlines()[1:]
now = int(time.time())
if dump:
    hs = [int(l.split("\t")[4]) for l in dump if len(l.split("\t")) > 4]
    wg = {"devices": len(dump), "online": sum(1 for h in hs if h and now - h < 180)}

upgradable = len([l for l in run("apt-get", "-s", "-o", "Debug::NoLocking=1", "upgrade").splitlines() if l.startswith("Inst ")])

out = {
    "hostname": socket.gethostname(), "os": os_name, "kernel": os.uname().release,
    "uptime": float(open("/proc/uptime").read().split()[0]) if os.access("/proc/uptime", os.R_OK) else 0,
    "load": list(os.getloadavg()) if hasattr(os, "getloadavg") else [], "cpus": os.cpu_count(),
    "cpu": round(100 * (1 - (i2 - i1) / max(1, t2 - t1)), 1) if t1 is not None and t2 is not None else None,
    "memTotal": mem.get("MemTotal", 0), "memAvailable": mem.get("MemAvailable", 0),
    "swapTotal": mem.get("SwapTotal", 0), "swapFree": mem.get("SwapFree", 0),
    "diskTotal": disk.total, "diskUsed": disk.used,
    "iface": dev or "", "rx": n2[0], "tx": n2[1], "rxRate": n2[0] - n1[0], "txRate": n2[1] - n1[1],
    "services": services, "ygg": ygg, "wg": wg,
    "upgradable": upgradable, "rebootRequired": os.path.exists("/var/run/reboot-required"),
}
print("YGGTUNNEL_RESULT " + json.dumps(out))
PY
