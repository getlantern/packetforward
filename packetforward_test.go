package packetforward

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getlantern/fdcount"
	"github.com/getlantern/gotun"
	"github.com/getlantern/ipproxy"
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
	ip := "127.0.0.1"

	// Create a packetforward server
	pfl, err := net.Listen("tcp", "localhost:0")
	if !assert.NoError(t, err) {
		return
	}
	defer pfl.Close()

	d := &net.Dialer{}
	s := NewServer(&ipproxy.Opts{
		IdleTimeout:   idleTimeout,
		StatsInterval: 250 * time.Millisecond,
		DialTCP: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Send everything to local echo server
			_, port, _ := net.SplitHostPort(addr)
			return d.DialContext(ctx, network, ip+":"+port)
		},
		DialUDP: func(ctx context.Context, network, addr string) (*net.UDPConn, error) {
			// Send everything to local echo server
			_, port, _ := net.SplitHostPort(addr)
			conn, dialErr := net.Dial(network, ip+":"+port)
			if dialErr != nil {
				return nil, dialErr
			}
			return conn.(*net.UDPConn), nil
		},
	})
	go s.Serve(pfl)

	// Open a TUN device
	dev, err := tun.OpenTunDevice("tun0", "10.0.0.10", "10.0.0.9", "255.255.255.0")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		dev.Close()
	}()

	// Forward packets from TUN device
	go Client(dev, 1400, clientIdleTimeout, func(ctx context.Context) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", pfl.Addr().String())
	})

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

	log.Debugf("Dialing echo server at: %v", echoAddr)
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
	assert.Equal(t, "helloudp", string(b))

	log.Debug("Here!")
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
