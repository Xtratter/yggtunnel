#!/bin/bash
# YggTunnel server setup — run as root by the app over SSH; safe to run again.
# Input (env): CLIENT_PUB — the phone's WireGuard public key, WG_PORT, YGG_PORT (public tls/quic listener),
#              YGG_PEERS — space-separated public peers for the server, SSH_PORT — stays open over Yggdrasil.
# Last line of output: YGGTUNNEL_RESULT {json} — read by the app.
set -euo pipefail
# Note: with pipefail never end a pipe early (head, grep -q, awk exit) — the writer dies of SIGPIPE (141).
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_SUSPEND=1 NEEDRESTART_MODE=a
say() { echo "== $*"; }
[ "$(id -u)" = 0 ] || { echo "error: root required (log in as root or allow passwordless sudo)"; exit 1; }
: "${CLIENT_PUB:?}" "${WG_PORT:?}" "${YGG_PORT:?}" "${YGG_PEERS:?}" "${SSH_PORT:?}"
. /etc/os-release
say "System: $PRETTY_NAME, $(nproc) CPU, $(free -m | awk '/^Mem/{print $2}') MB RAM"

say "Installing yggdrasil, wireguard-tools, iptables"
apt-get update -qq
apt-get install -y -qq yggdrasil wireguard-tools iptables python3 >/dev/null
yggdrasil -version 2>&1 | sed -n 2p   # sed reads everything: no SIGPIPE under pipefail

say "Configuring Yggdrasil"
# Yggdrasil is IPv6-only: switch IPv6 on if the VPS has it off (persists in sysctl.d)
if [ "$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null || echo 0)" = 1 ]; then
    echo "IPv6 is switched off on the server — switching it on (needed by Yggdrasil)"
    printf 'net.ipv6.conf.all.disable_ipv6 = 0\nnet.ipv6.conf.default.disable_ipv6 = 0\n' > /etc/sysctl.d/90-yggtunnel.conf
    sysctl -q -p /etc/sysctl.d/90-yggtunnel.conf || true
fi
CONF=/etc/yggdrasil/yggdrasil.conf
mkdir -p /etc/yggdrasil
[ -s "$CONF" ] || (umask 077; yggdrasil -genconf > "$CONF")
yggdrasil -useconffile "$CONF" -normaliseconf -json > /tmp/ygg.json
say "Choosing Yggdrasil peers for the server"
# The fastest reliable public peers as seen from the server itself (publicpeers.neilalexander.dev,
# measured by TCP connect time); the app's list is the fallback.
PICKED=$(python3 - <<'PY' || true
import json, socket, time, urllib.parse, urllib.request
from concurrent.futures import ThreadPoolExecutor
try:
    # the site refuses Python's default User-Agent (403)
    req = urllib.request.Request("https://publicpeers.neilalexander.dev/publicnodes.json", headers={"User-Agent": "YggTunnel"})
    data = json.load(urllib.request.urlopen(req, timeout=15))
except Exception as e:
    raise SystemExit(0)
cands = []
for country, peers in data.items():
    for uri, p in peers.items():
        st = p.get("states", "")
        if not p.get("up") or not uri.startswith("tls://") or not st or st.count("*") / len(st) < 0.9:
            continue
        cands.append(uri)
def rtt(uri):
    u = urllib.parse.urlparse(uri)
    try:
        t = time.monotonic()
        socket.create_connection((u.hostname, u.port), timeout=2).close()
        return time.monotonic() - t
    except Exception:
        return None
with ThreadPoolExecutor(32) as pool:
    timed = sorted((r, u) for u, r in zip(cands, pool.map(rtt, cands)) if r is not None)
print(" ".join(u for _, u in timed[:4]))
PY
)
if [ -n "$PICKED" ]; then YGG_PEERS=$PICKED; fi
echo "Server peers: $YGG_PEERS"
python3 - "$YGG_PORT" $YGG_PEERS <<'PY'
import json, sys
port, peers = sys.argv[1], sys.argv[2:]
c = json.load(open("/tmp/ygg.json"))
c["Peers"] = peers
# keep local listeners (the wss wrapper: ws://127.0.0.1:21444; tcp://127.0.0.1:21445 from 0.8.1)
c["Listen"] = [f"tls://0.0.0.0:{port}", f"quic://0.0.0.0:{port}"] + [l for l in c.get("Listen", []) if l.startswith(("ws://127.0.0.1:", "tcp://127.0.0.1:"))]
c["IfName"] = "ygg0"
c["MulticastInterfaces"] = []
c["NodeInfoPrivacy"] = True
# the Ubuntu/Debian unit runs as user yggdrasil and may write only to /run/yggdrasil
c["AdminListen"] = "unix:///run/yggdrasil/yggdrasil.sock"
json.dump(c, open("/etc/yggdrasil/yggdrasil.conf", "w"), indent=2)
PY
rm -f /tmp/ygg.json
# the service runs as user yggdrasil (Debian package): it must be able to read the config
if getent group yggdrasil >/dev/null; then chown root:yggdrasil "$CONF"; chmod 640 "$CONF"; else chmod 600 "$CONF"; fi
YGG_ADDR=$(yggdrasil -useconffile "$CONF" -address)
echo "Yggdrasil address: $YGG_ADDR"

say "Configuring WireGuard"
modprobe wireguard 2>/dev/null || true
if ! ip link add wgygg-test type wireguard 2>/dev/null; then
    echo "error: the kernel has no WireGuard (a container VPS: OpenVZ/LXC?)"; exit 1
fi
ip link del wgygg-test
# IPv6 may be switched off on the VPS: then the WireGuard interface gets IPv4 only
V6ON=true; [ "$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null || echo 1)" = 1 ] && V6ON=false
echo "IPv6 on the server: $V6ON"
WG=/etc/wireguard; mkdir -p $WG; chmod 700 $WG
[ -s $WG/wgygg.key ] || (umask 077; wg genkey > $WG/wgygg.key)
SERVER_PUB=$(wg pubkey < $WG/wgygg.key)
PEERS=$WG/wgygg.peers; touch $PEERS
# One line per device: "<public key> <n> [name]" → 10.66.66.<n>, fd66:66::<n> (names: devices.sh)
N=$(awk -v k="$CLIENT_PUB" '$1==k{print $2}' $PEERS)
if [ -z "$N" ]; then
    N=$(awk 'BEGIN{m=1} $2>m{m=$2} END{print m+1}' $PEERS)
    echo "$CLIENT_PUB $N ${CLIENT_NAME:-}" >> $PEERS
fi
WAN=$(ip -4 route show default | awk 'NR==1{print $5}')
echo "Internet interface: $WAN"
cat > /usr/local/sbin/yggtunnel-fw <<FW
#!/bin/bash
# Firewall for YggTunnel: NAT for WireGuard clients; on the Yggdrasil interface only WireGuard, SSH and ping.
A=\${1:-up}
for ipt in iptables ip6tables; do
    \$ipt -t nat -D POSTROUTING -o $WAN -j YGGTUNNEL-NAT 2>/dev/null || true
    \$ipt -t nat -F YGGTUNNEL-NAT 2>/dev/null || true; \$ipt -t nat -X YGGTUNNEL-NAT 2>/dev/null || true
    \$ipt -D FORWARD -j YGGTUNNEL-FWD 2>/dev/null || true
    \$ipt -F YGGTUNNEL-FWD 2>/dev/null || true; \$ipt -X YGGTUNNEL-FWD 2>/dev/null || true
done
ip6tables -D INPUT -i ygg0 -j YGGTUNNEL-IN 2>/dev/null || true
ip6tables -F YGGTUNNEL-IN 2>/dev/null || true; ip6tables -X YGGTUNNEL-IN 2>/dev/null || true
[ "\$A" = up ] || exit 0
sysctl -q -w net.ipv4.ip_forward=1 net.ipv6.conf.all.forwarding=1
iptables -t nat -N YGGTUNNEL-NAT; iptables -t nat -A YGGTUNNEL-NAT -s 10.66.66.0/24 -j MASQUERADE
iptables -t nat -A POSTROUTING -o $WAN -j YGGTUNNEL-NAT
ip6tables -t nat -N YGGTUNNEL-NAT; ip6tables -t nat -A YGGTUNNEL-NAT -s fd66:66::/64 -j MASQUERADE
ip6tables -t nat -A POSTROUTING -o $WAN -j YGGTUNNEL-NAT
for ipt in iptables ip6tables; do
    \$ipt -N YGGTUNNEL-FWD
    \$ipt -A YGGTUNNEL-FWD -i wgygg -j ACCEPT
    \$ipt -A YGGTUNNEL-FWD -o wgygg -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
    \$ipt -I FORWARD -j YGGTUNNEL-FWD
done
ip6tables -N YGGTUNNEL-IN
ip6tables -A YGGTUNNEL-IN -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
ip6tables -A YGGTUNNEL-IN -p ipv6-icmp -j ACCEPT
ip6tables -A YGGTUNNEL-IN -p udp --dport $WG_PORT -j ACCEPT
ip6tables -A YGGTUNNEL-IN -p udp --dport 21446 -j ACCEPT
ip6tables -A YGGTUNNEL-IN -p tcp --dport $SSH_PORT -j ACCEPT
ip6tables -A YGGTUNNEL-IN -j DROP
ip6tables -I INPUT -i ygg0 -j YGGTUNNEL-IN
FW
chmod 755 /usr/local/sbin/yggtunnel-fw
# A separate unit, not wg-quick PostUp: Ubuntu's AppArmor profile for wg-quick forbids running
# other programs (PostUp failed with "Permission denied", exit 126). Rules that name wgygg/ygg0
# work before the interfaces exist, so the order does not matter.
cat > /etc/systemd/system/yggtunnel-fw.service <<UNIT
[Unit]
Description=YggTunnel firewall (NAT for WireGuard, Yggdrasil interface filter)
After=network.target ufw.service
Before=wg-quick@wgygg.service yggdrasil.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/yggtunnel-fw up
ExecStop=/usr/local/sbin/yggtunnel-fw down

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
{
    echo "[Interface]"
    if $V6ON; then echo "Address = 10.66.66.1/24, fd66:66::1/64"; else echo "Address = 10.66.66.1/24"; fi
    echo "ListenPort = $WG_PORT"
    echo "PrivateKey = $(cat $WG/wgygg.key)"
    echo "MTU = 1280"
    while read -r k n _; do
        echo; echo "[Peer]"; echo "PublicKey = $k"
        if $V6ON; then echo "AllowedIPs = 10.66.66.$n/32, fd66:66::$n/128"; else echo "AllowedIPs = 10.66.66.$n/32"; fi
    done < $PEERS
} > $WG/wgygg.conf
chmod 600 $WG/wgygg.conf

if command -v ufw >/dev/null && ufw status | grep "Status: active" >/dev/null; then
    say "Opening ports in ufw"
    ufw allow "$YGG_PORT/tcp" >/dev/null; ufw allow "$YGG_PORT/udp" >/dev/null; ufw allow "$WG_PORT/udp" >/dev/null
    ufw route allow in on wgygg >/dev/null
fi

say "Starting services"
systemctl enable -q yggtunnel-fw yggdrasil wg-quick@wgygg
systemctl restart yggtunnel-fw || true
systemctl is-active -q yggtunnel-fw || { journalctl -u yggtunnel-fw -n 30 --no-pager -o cat; echo "error: firewall rules failed"; exit 1; }
systemctl restart yggdrasil || true
for _ in $(seq 1 10); do ip -6 addr show dev ygg0 2>/dev/null | grep "$YGG_ADDR" >/dev/null && break; sleep 1; done
systemctl is-active -q yggdrasil || { journalctl -u yggdrasil -n 30 --no-pager -o cat; echo "error: yggdrasil did not start"; exit 1; }
systemctl restart wg-quick@wgygg || true
systemctl is-active -q wg-quick@wgygg || { journalctl -u wg-quick@wgygg -n 30 --no-pager -o cat; echo "error: WireGuard did not start"; exit 1; }

say "Waiting for Yggdrasil peers"
UP=0
for _ in $(seq 1 20); do
    UP=$(yggdrasilctl -endpoint=unix:///run/yggdrasil/yggdrasil.sock -json getPeers 2>/dev/null | python3 -c 'import json,sys; print(sum(1 for p in json.load(sys.stdin).get("peers",[]) if p.get("up")))' 2>/dev/null || echo 0)
    [ "$UP" -gt 0 ] && break; sleep 1
done
echo "Peers up: $UP"
V6=false; $V6ON && [ -n "$(ip -6 route show default)" ] && V6=true
echo "IPv6 internet on the server: $V6"
say "Done"
echo "YGGTUNNEL_RESULT {\"yggAddress\":\"$YGG_ADDR\",\"wgPublicKey\":\"$SERVER_PUB\",\"wgPort\":$WG_PORT,\"clientIp4\":\"10.66.66.$N\",\"clientIp6\":\"fd66:66::$N\",\"yggPort\":$YGG_PORT,\"peersUp\":$UP,\"ipv6\":$V6}"
