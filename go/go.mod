module github.com/Xtratter/yggtunnel/go

go 1.27.1

require (
	github.com/gologme/log v1.3.0
	github.com/yggdrasil-network/yggdrasil-go v0.5.14
	golang.org/x/crypto v0.53.0
	golang.org/x/sys v0.46.0
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446
	rsc.io/qr v0.2.0
)

require (
	github.com/Arceliar/ironwood v0.0.0-20260613025018-d50055b11f5e // indirect
	github.com/Arceliar/phony v0.0.0-20220903101357-530938a4b13d // indirect
	github.com/bits-and-blooms/bitset v1.24.5 // indirect
	github.com/bits-and-blooms/bloom/v3 v3.7.1 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/hjson/hjson-go/v4 v4.6.0 // indirect
	github.com/quic-go/quic-go v0.60.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c // indirect
)

replace github.com/Arceliar/ironwood => ./third_party/ironwood
