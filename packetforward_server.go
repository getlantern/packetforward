package packetforward

import (
	"net"
	"time"

	"github.com/getlantern/framed"
	"github.com/getlantern/gotun"
)

const (
	maxListenDelay = 1 * time.Second
)

func Serve(l net.Listener, opts *tun.BridgeOpts) error {
	tempDelay := time.Duration(0)
	for {
		conn, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				// delay code based on net/http.Server
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > maxListenDelay {
					tempDelay = maxListenDelay
				}
				log.Debugf("Error accepting connection: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return log.Errorf("Error accepting: %v", err)
		}
		tempDelay = 0
		go handle(conn, opts)
	}
}

func handle(conn net.Conn, opts *tun.BridgeOpts) {
	br := tun.NewBridge(framed.NewReadWriteCloser(conn), opts)
	err := br.Serve()
	if err != nil {
		log.Errorf("Error handling packets: %v", err)
	}
}
