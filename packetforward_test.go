package packetforward

import (
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getlantern/fdcount"
	"github.com/getlantern/gonat"
	tun "github.com/getlantern/gotun"
	"github.com/getlantern/packetforward/server"

	"github.com/stretchr/testify/assert"
)

const (
	idleTimeout       = 10 * time.Second
	clientIdleTimeout = 100 * time.Second
)

var (
	serverTCPConnections int64
)

// Note - this test has to be run with root permissions to allow setting up the
// TUN device.
func TestEndToEnd(t *testing.T) {
	opts := &gonat.Opts{}
	err := opts.ApplyDefaults()
	if !assert.NoError(t, err) {
		return
	}
	ip := opts.IFAddr

	// Create a packetforward server
	pfl, err := net.Listen("tcp", ip+":0")
	if !assert.NoError(t, err) {
		return
	}
	defer pfl.Close()
	log.Debugf("Packetforward listening at %v", pfl.Addr())

	d := &net.Dialer{}
	s := server.NewServer(&gonat.Opts{
		IdleTimeout: idleTimeout,
		// StatsInterval: 250 * time.Millisecond,
		OnOutbound: func(pkt *gonat.IPPacket) {
			// Send everything to local echo server\
			pkt.SetDest(ip, pkt.FT().Dst.Port)
		},
		OnInbound: func(pkt *gonat.IPPacket, ft gonat.FourTuple) {
			pkt.SetSource("10.0.0.9", ft.Dst.Port)
		},
	})
	go s.Serve(pfl)

	// Open a TUN device
	dev, err := tun.OpenTunDevice("tun0", "10.0.0.10", "10.0.0.9", "255.255.255.0")
	if err != nil {
		if err != nil {
			if strings.HasSuffix(err.Error(), "operation not permitted") {
				t.Log("This test requires root access. Compile, then run with root privileges. See the README for more details.")
			}
			t.Fatal(err)
		}
	}
	defer func() {
		dev.Close()
	}()

	// Forward packets from TUN device
	writer := Client(dev, 1400, clientIdleTimeout, func(ctx context.Context) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", pfl.Addr().String())
	})
	go func() {
		b := make([]byte, 1400)
		for {
			n, err := dev.Read(b)
			if n > 0 {
				writer.Write(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	defer writer.Close()

	closeCh := make(chan interface{})
	echoAddr := tcpEcho(t, closeCh, ip)
	udpEcho(t, closeCh, echoAddr)

	// point at TUN device rather than echo server directly
	_, port, _ := net.SplitHostPort(echoAddr)
	echoAddr = "10.0.0.9:" + port

	b := make([]byte, 8)

	_, connCount, err := fdcount.Matching("TCP")
	if !assert.NoError(t, err, "unable to get initial socket count") {
		return
	}

	log.Debugf("Dialing echo server with UDP at: %v", echoAddr)
	uconn, err := net.Dial("udp", echoAddr)
	if !assert.NoError(t, err, "Unable to get UDP connection to TUN device") {
		return
	}
	defer uconn.Close()

	_, err = uconn.Write([]byte("helloudp"))
	if !assert.NoError(t, err) {
		return
	}

	// Sleep long enough to hit idle timeout so that client will have to reconnect
	// time.Sleep(clientIdleTimeout + 100*time.Millisecond)
	uconn.SetDeadline(time.Now().Add(250 * time.Millisecond))
	_, err = io.ReadFull(uconn, b)
	if !assert.NoError(t, err) {
		return
	}

	log.Debugf("Dialing echo server with TCP at: %v", echoAddr)
	conn, err := net.DialTimeout("tcp", echoAddr, 5*time.Second)
	if !assert.NoError(t, err) {
		return
	}
	defer conn.Close()

	_, err = conn.Write([]byte("hellotcp"))
	if !assert.NoError(t, err) {
		return
	}

	// Sleep long enough to hit idle timeout so that client will have to reconnect
	// time.Sleep(clientIdleTimeout + 100*time.Second)
	_, err = io.ReadFull(conn, b)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "hellotcp", string(b))
	conn.Close()
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, atomic.LoadInt64(&serverTCPConnections), "Server-side TCP connection should have been closed")

	time.Sleep(2 * idleTimeout)
	connCount.AssertDelta(0)
}

func tcpEcho(t *testing.T, closeCh <-chan interface{}, ip string) string {
	l, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-closeCh
		l.Close()
	}()
	log.Debugf("TCP echo server listening at: %v", l.Addr())

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			atomic.AddInt64(&serverTCPConnections, 1)
			go io.Copy(conn, conn)
			atomic.AddInt64(&serverTCPConnections, -1)
		}
	}()

	return l.Addr().String()
}

func udpEcho(t *testing.T, closeCh <-chan interface{}, echoAddr string) {
	conn, err := net.ListenPacket("udp", echoAddr)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-closeCh
		conn.Close()
	}()
	log.Debugf("UDP echo server listening at: %v", conn.LocalAddr())

	go func() {
		b := make([]byte, 20480)
		for {
			n, addr, err := conn.ReadFrom(b)
			if err != nil {
				t.Error(err)
				return
			}
			conn.WriteTo(b[:n], addr)
		}
	}()
}
