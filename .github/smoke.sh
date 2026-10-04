#!/usr/bin/env bash
# Smoke test on an emulator: the app must start and stay alive — fresh, and with filled-in settings
# (dark theme, hidden addresses, a server profile with a wss wrapper) so every card is drawn.
# 0.10 crashed on start; this would have caught it. Usage: .github/smoke.sh app-debug.apk
set -euo pipefail
APK=$1
PKG=io.github.xtratter.yggtunnel
fail() { echo "SMOKE FAIL: $*"; adb logcat -d -b crash | tail -40; adb shell run-as $PKG cat files/crash.txt 2>/dev/null || true; exit 1; }

adb install -r "$APK"

run() { # $1 — scenario name
    adb shell am force-stop $PKG
    adb logcat -c
    adb shell am start -W -n $PKG/.MainActivity >/dev/null
    sleep 15
    adb shell pidof $PKG >/dev/null || fail "$1: the app is not running"
    if adb logcat -d -b crash | grep -q "$PKG"; then fail "$1: crash in logcat"; fi
    if adb shell run-as $PKG ls files/crash.txt >/dev/null 2>&1; then fail "$1: CrashLog saved a crash"; fi
    adb exec-out screencap -p > "smoke-$1.png" || true
    echo "smoke ok: $1"
}

run fresh

# filled-in settings, written straight into the debug build's preferences
adb shell am force-stop $PKG
cat > prefs.xml <<'XML'
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <string name="theme">DARK</string>
    <boolean name="hide_addresses" value="true" />
    <boolean name="translucent" value="false" />
    <int name="keep_peers" value="4" />
    <boolean name="battery_asked" value="true" />
    <string name="peers">tls://203.0.113.10:21443
wss://vpn.example.org:443/secretpath
tls://peer.example.net:443
quic://198.51.100.7:8364</string>
    <string name="server">{"host":"203.0.113.10","port":2222,"user":"root","key":"-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----","passphrase":"","hostKey":"SHA256:test","wssPeer":"wss://vpn.example.org:443/secretpath","result":{"yggAddress":"201:1234:5678:9abc:def0:1234:5678:9abc","wgPublicKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","wgPort":51820,"clientIp4":"10.66.66.2","clientIp6":"fd66:66::2","yggPort":21443,"peersUp":4,"ipv6":false}}</string>
</map>
XML
adb push prefs.xml /data/local/tmp/prefs.xml >/dev/null
adb shell "run-as $PKG sh -c 'mkdir -p shared_prefs && cp /data/local/tmp/prefs.xml shared_prefs/prefs.xml'"
run filled

# secrets are encrypted with the Keystore: the plain server profile written above must be re-sealed
adb shell "run-as $PKG cat shared_prefs/prefs.xml" > prefs-after.xml
if grep -q "BEGIN OPENSSH" prefs-after.xml; then fail "the SSH key is still stored in plain text"; fi
# 0.12: the single "server" profile becomes the first entry of "servers"
grep -q '<string name="servers">enc1:' prefs-after.xml || fail "the server list is not encrypted"
if grep -q '<string name="server">' prefs-after.xml; then fail "the old server profile was not migrated"; fi
# the node key and WireGuard keys appear only after the first connect: if present, they must be sealed too
for k in config wg_keys local_devices; do
    if grep -q "<string name=\"$k\">" prefs-after.xml && ! grep -q "<string name=\"$k\">enc1:" prefs-after.xml; then
        fail "$k is stored in plain text"
    fi
done
echo "smoke ok: secrets encrypted"
