#!/usr/bin/env bash
# Builds the native core (Yggdrasil + JNI) into app/src/main/jniLibs/<abi>/libygg.so.
#   go/build.sh [abi ...]   — default: arm64-v8a in Termux, all three ABIs elsewhere
# In Termux the Termux clang is used (no NDK needed; arm64 only); elsewhere the NDK from
# ANDROID_NDK_HOME / ANDROID_NDK_LATEST_HOME. YGG_ABIS="a b" overrides the default.
set -euo pipefail
cd "$(dirname "$0")"
OUT=../app/src/main/jniLibs
if [ $# -gt 0 ]; then ABIS=("$@")
elif [ -n "${YGG_ABIS:-}" ]; then read -ra ABIS <<< "$YGG_ABIS"
elif [ "$(uname -o)" = Android ]; then ABIS=(arm64-v8a)
else ABIS=(arm64-v8a armeabi-v7a x86_64)
fi
API=26
for abi in "${ABIS[@]}"; do
    case $abi in
        arm64-v8a)   arch=arm64; triple=aarch64-linux-android ;;
        armeabi-v7a) arch=arm;   triple=armv7a-linux-androideabi ;;
        x86_64)      arch=amd64; triple=x86_64-linux-android ;;
        *) echo "unknown ABI $abi" >&2; exit 1 ;;
    esac
    extra=()
    if [ "$(uname -o)" = Android ]; then
        [ "$abi" = arm64-v8a ] || { echo "Termux builds only arm64-v8a" >&2; exit 1; }
        cc=clang
        # Termux clang has no Android jni.h on its path; the JDK one has the same ABI.
        jdk=$(dirname "$(dirname "$(readlink -f "$(command -v javac)")")")
        extra=(-I"$jdk/include" -I"$jdk/include/linux")
    else
        ndk=${ANDROID_NDK_HOME:-${ANDROID_NDK_LATEST_HOME:?set ANDROID_NDK_HOME}}
        cc=$(echo "$ndk"/toolchains/llvm/prebuilt/*/bin/$triple$API-clang)
    fi
    mkdir -p "$OUT/$abi"
    # -checklinkname=0: wlynxg/anet (network interfaces on Android) uses go:linkname.
    # max-page-size=16384: Android 15+ devices with 16 KB memory pages need 16 KB-aligned libraries.
    CGO_ENABLED=1 GOOS=android GOARCH=$arch GOARM=7 CC=$cc CGO_CFLAGS="${extra[*]} -O2" \
        go build -trimpath -buildvcs=false -buildmode=c-shared \
        -ldflags="-s -w -checklinkname=0 -extldflags=-Wl,-z,max-page-size=16384" \
        -o "$OUT/$abi/libygg.so" .
    rm -f "$OUT/$abi/libygg.h"
    echo "built $OUT/$abi/libygg.so"
done
