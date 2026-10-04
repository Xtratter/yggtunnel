package main

/*
#include <jni.h>
#include <stdlib.h>
#include <string.h>

static char* jstr(JNIEnv* env, jstring s) {
	if (s == NULL) return NULL;
	const char* c = (*env)->GetStringUTFChars(env, s, NULL);
	char* r = c ? strdup(c) : NULL;
	if (c) (*env)->ReleaseStringUTFChars(env, s, c);
	return r;
}
static jstring newStr(JNIEnv* env, const char* s) { return (*env)->NewStringUTF(env, s); }
*/
import "C"

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// JNI entry points for io.github.xtratter.yggtunnel.Native. Strings cross the
// boundary as JSON / plain text; errors come back as "error: ..." strings.

func goStr(env *C.JNIEnv, s C.jstring) string {
	c := C.jstr(env, s)
	if c == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(c))
	return C.GoString(c)
}

func jStr(env *C.JNIEnv, s string) C.jstring {
	c := C.CString(s)
	defer C.free(unsafe.Pointer(c))
	return C.newStr(env, c)
}

//export Java_io_github_xtratter_yggtunnel_Native_generateConfig
func Java_io_github_xtratter_yggtunnel_Native_generateConfig(env *C.JNIEnv, cls C.jclass) C.jstring {
	s, err := GenerateConfig()
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, s)
}

//export Java_io_github_xtratter_yggtunnel_Native_start
func Java_io_github_xtratter_yggtunnel_Native_start(env *C.JNIEnv, cls C.jclass, config C.jstring, peers C.jstring, keep C.jint, pinned C.jstring, serverOnly C.jboolean) C.jstring {
	var list []string
	for _, p := range strings.Fields(goStr(env, peers)) {
		list = append(list, p)
	}
	addr, err := node.StartWith(goStr(env, config), list, int(keep), strings.Fields(goStr(env, pinned)), serverOnly != 0)
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, addr)
}

//export Java_io_github_xtratter_yggtunnel_Native_mtu
func Java_io_github_xtratter_yggtunnel_Native_mtu(env *C.JNIEnv, cls C.jclass) C.jint {
	return C.jint(node.MTU())
}

//export Java_io_github_xtratter_yggtunnel_Native_attachTun
func Java_io_github_xtratter_yggtunnel_Native_attachTun(env *C.JNIEnv, cls C.jclass, fd C.jint) C.jstring {
	if err := node.AttachTun(int(fd)); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "")
}

//export Java_io_github_xtratter_yggtunnel_Native_stop
func Java_io_github_xtratter_yggtunnel_Native_stop(env *C.JNIEnv, cls C.jclass) {
	node.Stop()
}

//export Java_io_github_xtratter_yggtunnel_Native_retryPeers
func Java_io_github_xtratter_yggtunnel_Native_retryPeers(env *C.JNIEnv, cls C.jclass) {
	node.RetryPeers()
}

//export Java_io_github_xtratter_yggtunnel_Native_status
func Java_io_github_xtratter_yggtunnel_Native_status(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, node.Status())
}

//export Java_io_github_xtratter_yggtunnel_Native_log
func Java_io_github_xtratter_yggtunnel_Native_log(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, logSink.String())
}

func main() {}

//export Java_io_github_xtratter_yggtunnel_Native_wgKeyPair
func Java_io_github_xtratter_yggtunnel_Native_wgKeyPair(env *C.JNIEnv, cls C.jclass) C.jstring {
	s, err := WgKeyPair()
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, s)
}

//export Java_io_github_xtratter_yggtunnel_Native_setupStart
func Java_io_github_xtratter_yggtunnel_Native_setupStart(env *C.JNIEnv, cls C.jclass, params C.jstring) C.jstring {
	if err := setup.Start(goStr(env, params)); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "")
}

//export Java_io_github_xtratter_yggtunnel_Native_setupStatus
func Java_io_github_xtratter_yggtunnel_Native_setupStatus(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, setup.Status())
}

//export Java_io_github_xtratter_yggtunnel_Native_attachTunnel
func Java_io_github_xtratter_yggtunnel_Native_attachTunnel(env *C.JNIEnv, cls C.jclass, fd C.jint, config C.jstring) C.jstring {
	var cfg TunnelConfig
	if err := json.Unmarshal([]byte(goStr(env, config)), &cfg); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	if err := node.AttachTunnel(int(fd), cfg); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "")
}

//export Java_io_github_xtratter_yggtunnel_Native_qr
func Java_io_github_xtratter_yggtunnel_Native_qr(env *C.JNIEnv, cls C.jclass, text C.jstring) C.jstring {
	s, err := QRMatrix(goStr(env, text))
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, s)
}

// pingResult: round trip in ms ("12.3") or "error: …".
func pingResult(env *C.JNIEnv, d time.Duration, err error) C.jstring {
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64))
}

//export Java_io_github_xtratter_yggtunnel_Native_pingYgg
func Java_io_github_xtratter_yggtunnel_Native_pingYgg(env *C.JNIEnv, cls C.jclass, dst C.jstring, timeoutMs C.jint) C.jstring {
	d, err := node.PingYgg(goStr(env, dst), time.Duration(timeoutMs)*time.Millisecond)
	return pingResult(env, d, err)
}

//export Java_io_github_xtratter_yggtunnel_Native_pingInet
func Java_io_github_xtratter_yggtunnel_Native_pingInet(env *C.JNIEnv, cls C.jclass, dst C.jstring, timeoutMs C.jint) C.jstring {
	d, err := node.PingInet(goStr(env, dst), time.Duration(timeoutMs)*time.Millisecond)
	return pingResult(env, d, err)
}

//export Java_io_github_xtratter_yggtunnel_Native_peerTestStart
func Java_io_github_xtratter_yggtunnel_Native_peerTestStart(env *C.JNIEnv, cls C.jclass, params C.jstring) C.jstring {
	var p PeerTestParams
	if err := json.Unmarshal([]byte(goStr(env, params)), &p); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	if err := PeerTestStart(p); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "")
}

//export Java_io_github_xtratter_yggtunnel_Native_peerTestStatus
func Java_io_github_xtratter_yggtunnel_Native_peerTestStatus(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, PeerTestStatus())
}

//export Java_io_github_xtratter_yggtunnel_Native_peerTestStop
func Java_io_github_xtratter_yggtunnel_Native_peerTestStop(env *C.JNIEnv, cls C.jclass) {
	PeerTestStop()
}

//export Java_io_github_xtratter_yggtunnel_Native_diagStart
func Java_io_github_xtratter_yggtunnel_Native_diagStart(env *C.JNIEnv, cls C.jclass, seconds C.jint) C.jstring {
	if err := DiagStart(int(seconds)); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "ok")
}

//export Java_io_github_xtratter_yggtunnel_Native_diagStop
func Java_io_github_xtratter_yggtunnel_Native_diagStop(env *C.JNIEnv, cls C.jclass) { DiagStop() }

//export Java_io_github_xtratter_yggtunnel_Native_diagStatus
func Java_io_github_xtratter_yggtunnel_Native_diagStatus(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, DiagStatus())
}

//export Java_io_github_xtratter_yggtunnel_Native_diagText
func Java_io_github_xtratter_yggtunnel_Native_diagText(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, DiagText())
}

//export Java_io_github_xtratter_yggtunnel_Native_diagSetCC
func Java_io_github_xtratter_yggtunnel_Native_diagSetCC(env *C.JNIEnv, cls C.jclass, name C.jstring) C.jstring {
	n, err := DiagSetCC(goStr(env, name))
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, strconv.Itoa(n))
}

//export Java_io_github_xtratter_yggtunnel_Native_setLinkLowat
func Java_io_github_xtratter_yggtunnel_Native_setLinkLowat(env *C.JNIEnv, cls C.jclass, bytes C.jint) {
	SetLinkLowat(int(bytes))
}

//export Java_io_github_xtratter_yggtunnel_Native_linkLowat
func Java_io_github_xtratter_yggtunnel_Native_linkLowat(env *C.JNIEnv, cls C.jclass) C.jint {
	return C.jint(LinkLowat())
}

//export Java_io_github_xtratter_yggtunnel_Native_goStacks
func Java_io_github_xtratter_yggtunnel_Native_goStacks(env *C.JNIEnv, cls C.jclass) C.jstring {
	return jStr(env, GoStacks())
}

//export Java_io_github_xtratter_yggtunnel_Native_redirectStderr
func Java_io_github_xtratter_yggtunnel_Native_redirectStderr(env *C.JNIEnv, cls C.jclass, path C.jstring) C.jstring {
	if err := RedirectStderr(goStr(env, path)); err != nil {
		return jStr(env, "error: "+err.Error())
	}
	return jStr(env, "ok")
}

//export Java_io_github_xtratter_yggtunnel_Native_speedYgg
func Java_io_github_xtratter_yggtunnel_Native_speedYgg(env *C.JNIEnv, cls C.jclass, target C.jstring, port C.jint, token C.jstring, size C.jint) C.jstring {
	r, err := node.SpeedYgg(goStr(env, target), int(port), goStr(env, token), int(size))
	if err != nil {
		return jStr(env, "error: "+err.Error())
	}
	b, _ := json.Marshal(r)
	return jStr(env, string(b))
}
