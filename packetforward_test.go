package packetforward

import (
	"context"
	"io"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/getlantern/gonat"
	"github.com/getlantern/packetforward/server"
)

const (
	idleTimeout       = 100 * time.Millisecond
	clientIdleTimeout = 1 * time.Second
	tunGW             = "10.0.0.9"
)

var (
	serverTCPConnections int64
)

// Note - this test has to be run with root permissions to allow setting up the
// TUN device.
func TestEndToEnd(t *testing.T) {
	gonat.RunTest(t, "tun0", "10.0.0.10", tunGW, "255.255.255.0", 1500, func(ifAddr string, dev io.ReadWriter, origEchoAddr gonat.Addr, finishedCh chan interface{}) (func() error, error) {
		// Create a packetforward server
		pfl, err := net.Listen("tcp", ifAddr+":0")
		if err != nil {
			return nil, err
		}
		log.Debugf("Packetforward listening at %v", pfl.Addr())

		d := &net.Dialer{}
		s, err := server.NewServer(&server.Opts{
			Opts: gonat.Opts{
				IdleTimeout:   idleTimeout,
				StatsInterval: 250 * time.Millisecond,
				OnOutbound: func(pkt *gonat.IPPacket) {
					// Send everything to local echo server
					pkt.SetDest(origEchoAddr)
				},
				OnInbound: func(pkt *gonat.IPPacket, downFT gonat.FiveTuple) {
					pkt.SetSource(gonat.Addr{tunGW, downFT.Dst.Port})
				},
			},
		})
		if err != nil {
			return nil, err
		}
		go func() {
			s.Serve(pfl)
			close(finishedCh)
		}()

		// Forward packets from TUN device
		writer := Client(dev, clientIdleTimeout, func(ctx context.Context) (net.Conn, error) {
			conn, err := d.DialContext(ctx, "tcp", pfl.Addr().String())
			if conn != nil {
				conn = &autoCloseConn{Conn: conn}
			}
			return conn, err
		})
		go func() {
			b := make([]byte, gonat.MaximumIPPacketSize)
			for {
				n, err := dev.Read(b)
				log.Debugf("Read %d: %v", n, err)
				if n > 0 {
					log.Debugf("Writing %d", n)
					writer.Write(b[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		return func() error {
			writer.Close()
			s.Close()
			return pfl.Close()
		}, nil
	})
}

var writes = 0

type autoCloseConn struct {
	net.Conn
}

func (c *autoCloseConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	log.Debugf("%d / %d: %v", n, len(b), err)
	if writes > 2 && rand.Float64() < 0.20 {
		// Randomly close the connection 20% of the time, but not on the first two writes (client id)
		c.Close()
	}
	writes++
	return n, err
}
