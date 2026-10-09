#!/bin/bash
# YggTunnel speed test service: a tiny UDP sender on the server's Yggdrasil address only (port 21446;
# nothing listens on the public interfaces). The phone asks for N bytes with a secret token, the server
# sends them in 1200-byte datagrams; the phone measures what arrives. A daily cap guards the traffic.
# ACTION=install (default) — install or update, keeps the token; ACTION=remove — remove it.
# Last line: YGGTUNNEL_RESULT {"port":…,"token":"…","capMB":…,"v":2} (v — the protocol).
# For a local test: SPEED_DIR, SPEED_NO_SYSTEMD=1, SPEED_ADDR override the paths, systemd and address.
set -euo pipefail
PORT=21446
CAP_MB=${SPEED_CAP_MB:-1024}
DIR=${SPEED_DIR:-/etc/yggtunnel}
BIN=${SPEED_BIN:-/usr/local/sbin/yggtunnel-speed}
UNIT=/etc/systemd/system/yggtunnel-speed.service
FW=/usr/local/sbin/yggtunnel-fw
RULE="-p udp --dport $PORT -j ACCEPT"
[ -n "${SPEED_NO_SYSTEMD:-}" ] || [ "$(id -u)" = 0 ] || { echo "error: root required"; exit 1; }

if [ "${ACTION:-install}" = remove ]; then
    systemctl disable --now yggtunnel-speed 2>/dev/null || true
    rm -f "$UNIT" "$BIN" "$DIR/speed.env"; systemctl daemon-reload
    ip6tables -D YGGTUNNEL-IN $RULE 2>/dev/null || true
    [ -f $FW ] && sed -i "/--dport $PORT -j ACCEPT/d" $FW
    echo "Speed test removed"
    echo 'YGGTUNNEL_RESULT {"removed":true}'
    exit 0
fi

ADDR=${SPEED_ADDR:-$(yggdrasil -useconffile /etc/yggdrasil/yggdrasil.conf -address 2>/dev/null || true)}
[ -n "$ADDR" ] || { echo "error: Yggdrasil is not set up on this server (set the server up first)"; exit 1; }
echo "Yggdrasil address: $ADDR"
mkdir -p "$DIR"; chmod 700 "$DIR"
if [ ! -s "$DIR/speed.env" ] || ! grep -q '^SPEED_TOKEN=[0-9a-f]\{32\}$' "$DIR/speed.env"; then
    (umask 077; echo "SPEED_TOKEN=$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')" > "$DIR/speed.env")
fi
TOKEN=$(sed -n 's/^SPEED_TOKEN=//p' "$DIR/speed.env")
{ echo "SPEED_TOKEN=$TOKEN"; echo "SPEED_ADDR=$ADDR"; echo "SPEED_PORT=$PORT"; echo "SPEED_CAP_MB=$CAP_MB"; } > "$DIR/speed.env.new"
chmod 600 "$DIR/speed.env.new"; mv "$DIR/speed.env.new" "$DIR/speed.env"

cat > "$BIN" <<'PY'
#!/usr/bin/env python3
# YggTunnel speed test sender (see speed.sh), protocol 2 — rate-controlled by the phone's reports:
#   request b"YTS2" + token(16) + id(4) + size(4)
#   data    b"YTD2" + id + seq(4) + count(4) + padding (1200 bytes)
#   report  b"YTA2" + id + bytes(4) + packets(4) + highest seq(4), every 100 ms from the phone
#   end     b"YTF2" + id + count (5 times); refusal (busy / daily cap) b"YTB2" + id
# The rate starts at 8 Mbit/s and doubles per report while under 3% is lost; on loss it falls to what
# arrived. Blasting at full speed (protocol 1) only measured the drops in Yggdrasil's queues.
import hmac, os, socket, struct, threading, time
TOKEN = bytes.fromhex(os.environ["SPEED_TOKEN"])
ADDR, PORT = os.environ["SPEED_ADDR"], int(os.environ.get("SPEED_PORT", "21446"))
CAP = int(os.environ.get("SPEED_CAP_MB", "1024")) * 1_000_000
MAX_SIZE, PKT, MAX_RUNNING = 8_000_000, 1200, 8
START, MIN_RATE, MAX_RATE = 1_000_000, 125_000, 25_000_000  # bytes/s: 8 Mbit/s, 1, 200
sock = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, 4 << 20)
sock.bind((ADDR, PORT))
lock = threading.Lock()
state = {"day": time.strftime("%Y%m%d"), "sent": 0, "running": 0}
reports = {}  # (peer ip, id) → the latest report (time, bytes, packets, highest)

def send(peer, rid, size):
    key = (peer[0], rid)
    count = (size + PKT - 1) // PKT
    pad = bytes(PKT - 16)
    rate, growing = START, True
    now = time.monotonic()
    nxt, evaluate, deadline = now, now + 0.1, now + 20
    prev, quiet_since = None, now
    try:
        seq = 0
        while seq < count and now < deadline:
            if now >= nxt:
                try:
                    sock.sendto(b"YTD2" + rid + struct.pack(">II", seq, count) + pad, peer)
                    seq += 1
                except OSError:
                    pass
                nxt = max(nxt + PKT / rate, now - 0.01)
            if now >= evaluate:
                evaluate = now + 0.1
                r = reports.get(key)
                if r and prev and r[3] > prev[3]:
                    dt = max(r[0] - prev[0], 1e-3)
                    lost = 1 - (r[2] - prev[2]) / (r[3] - prev[3])
                    got = (r[1] - prev[1]) / dt
                    if lost < 0.03:
                        rate = min(MAX_RATE, rate * (2 if growing else 1.1))
                    else:
                        growing = False
                        rate = max(MIN_RATE, got * 0.9)
                    quiet_since = now
                elif now - quiet_since > 1:  # nothing arrives: slow down
                    rate, quiet_since = max(MIN_RATE, rate / 2), now
                if r: prev = r
            wait = min(nxt, evaluate) - time.monotonic()
            if wait > 0.0005:
                time.sleep(wait)
            now = time.monotonic()
        for _ in range(5):
            time.sleep(0.05)
            sock.sendto(b"YTF2" + rid + struct.pack(">I", seq), peer)
    finally:
        with lock:
            state["running"] -= 1
            reports.pop(key, None)

while True:
    data, peer = sock.recvfrom(64)
    if len(data) == 20 and data[:4] == b"YTA2":
        key = (peer[0], data[4:8])
        if key in reports:
            reports[key] = (time.monotonic(),) + struct.unpack(">III", data[8:20])
        continue
    if len(data) != 28 or data[:4] != b"YTS2" or not hmac.compare_digest(data[4:20], TOKEN):
        continue
    rid, size = data[20:24], min(struct.unpack(">I", data[24:28])[0], MAX_SIZE)
    with lock:
        today = time.strftime("%Y%m%d")
        if state["day"] != today:
            state["day"], state["sent"] = today, 0
        if (peer[0], rid) in reports:
            continue  # a repeated request for a test already running
        if state["running"] >= MAX_RUNNING or state["sent"] + size > CAP:
            sock.sendto(b"YTB2" + rid, peer)  # busy or over the daily cap
            continue
        state["running"] += 1
        state["sent"] += size
        reports[(peer[0], rid)] = None
    threading.Thread(target=send, args=(peer, rid, size), daemon=True).start()
PY
chmod 755 "$BIN"

if [ -n "${SPEED_NO_SYSTEMD:-}" ]; then
    echo "YGGTUNNEL_RESULT {\"port\":$PORT,\"token\":\"$TOKEN\",\"capMB\":$CAP_MB,\"v\":2}"
    exit 0
fi
cat > "$UNIT" <<UNIT
[Unit]
Description=YggTunnel speed test (UDP on the Yggdrasil address only)
After=yggdrasil.service
Requires=yggdrasil.service

[Service]
EnvironmentFile=$DIR/speed.env
ExecStart=$BIN
User=nobody
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable -q yggtunnel-speed
systemctl restart yggtunnel-speed
# the Yggdrasil interface only lets WireGuard, SSH and ping in: open the port there, now and in yggtunnel-fw
ip6tables -C YGGTUNNEL-IN $RULE 2>/dev/null || ip6tables -I YGGTUNNEL-IN 1 $RULE
if [ -f $FW ] && ! grep -q -- "--dport $PORT -j ACCEPT" $FW; then
    sed -i "s|^ip6tables -A YGGTUNNEL-IN -p udp --dport \([0-9]*\) -j ACCEPT$|&\nip6tables -A YGGTUNNEL-IN $RULE|" $FW
fi
for i in 1 2 3 4 5 6 7 8 9 10; do
    ss -Hulnp | grep ":$PORT " >/dev/null && break  # not grep -q: with pipefail an early exit is SIGPIPE
    sleep 1
done
if ! ss -Hulnp | grep ":$PORT " >/dev/null; then
    journalctl -u yggtunnel-speed -n 20 --no-pager -o cat || true
    echo "error: the speed test service did not start"; exit 1
fi
echo "Speed test listening on [$ADDR]:$PORT, daily cap $CAP_MB MB"
echo "YGGTUNNEL_RESULT {\"port\":$PORT,\"token\":\"$TOKEN\",\"capMB\":$CAP_MB,\"v\":2}"
