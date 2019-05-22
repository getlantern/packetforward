module github.com/getlantern/packetforward

go 1.12

require (
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7
	github.com/getlantern/eventual v0.0.0-20180125201821-84b02499361b
	github.com/getlantern/fdcount v0.0.0-20170105153814-6a6cb5839bc5
	github.com/getlantern/framed v0.0.0-20190520183551-20677fb6c61f
	github.com/getlantern/golog v0.0.0-20170508214112-cca714f7feb5
	github.com/getlantern/gonat v0.0.0-20190522181311-cca6a8111e43
	github.com/getlantern/gotun v0.0.0-20190521133812-51bfa687e26b
	github.com/getlantern/idletiming v0.0.0-20190331182121-9540d1aeda2b
	github.com/getlantern/ipproxy v0.0.0-20190502203022-c6564ee6fba1
	github.com/getlantern/uuid v1.1.2-0.20190507182000-5c9436b8c718
	github.com/google/btree v1.0.0 // indirect
	github.com/google/netstack v0.0.0-20190505230633-4391e4a763ab // indirect
	github.com/stretchr/testify v1.3.0
)

replace github.com/google/netstack => github.com/getlantern/netstack v0.0.0-20190313202628-8999826b821d
