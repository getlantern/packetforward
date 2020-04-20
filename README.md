packetforward [![GoDoc](https://godoc.org/github.com/getlantern/packetforward?status.png)](http://godoc.org/github.com/getlantern/packetforward)&nbsp;[![Build Status](https://drone.lantern.io/api/badges/getlantern/packetforward/status.svg)](https://drone.lantern.io/getlantern/packetforward)&nbsp;[![Coverage Status](https://coveralls.io/repos/github/getlantern/packetforward/badge.svg?branch=init)](https://coveralls.io/github/getlantern/packetforward)
==========

A library for forwarding packets.

## Dependencies

This library uses Go modules. When running commands like `go test` in this repository, make sure the GO111MODULE environment variable is set to 'on'. See the [go command documentation](https://golang.org/cmd/go/#hdr-Preliminary_module_support) for more details. If you are running Go 1.13 or later, this should not be necessary as the Go tool will support modules by default.

## Testing

Tests in this package require root access. The easiest way to test is to compile the tests with `go test -c` and run the output binary using the `sudo` command.

Be careful if you choose to run the Go tool with the sudo command (e.g. `sudo go test`). This can cause issues if the tool attempts to download missing dependencies. Namely, the Go tool may not be able to download anything as Git will likely be using a different SSH keypair (or no keypair at all). Worse, the Go tool may create folders in $GOPATH/pkg/mod/cache owned by the root user. This can disrupt future use of the Go tool, even outside of this repository.

## Demo

This repository includes a demo client and server in `demo/client` and `demo/server`.

The server forwards TCP and UDP packets to hardcoded IP addresses configured with the `-tcpdest` and `-udpdest` flags.

The client opens a TUN device.

For example:

```
sudo iptables -d OUTPUT -p tcp -m conntrack --ctstate ESTABLISHED --ctdir ORIGINAL --tcp-flags RST RST -j DROP
cd demo/server
go build && sudo ./server
```

```
cd demo/client
go build && sudo ./client
```

```
curl http://10.0.0.1/1GB.zip > /dev/null
```