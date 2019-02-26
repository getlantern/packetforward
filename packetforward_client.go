package packetforward

import (
	"github.com/getlantern/errors"
	"github.com/getlantern/framed"
	"io"
	"math"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const (
	maxDialDelay = 1 * time.Second

	ioTimeout = 3 * time.Second
)

type forwarder struct {
	downstream              io.ReadWriteCloser
	packetForwardServerAddr string
	mtu                     int
	dialer                  proxy.Dialer
	upstreamConn            net.Conn
	upstream                io.ReadWriteCloser
	stopErr                 atomic.Value
}

func To(socksProxyAddr string, packetForwardServerAddr string, downstream io.ReadWriteCloser, mtu int) error {
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddr, nil, &net.Dialer{})
	if err != nil {
		return errors.New("Unable to create SOCKS5 dialer: %v", err)
	}

	f := &forwarder{downstream: downstream, packetForwardServerAddr: packetForwardServerAddr, mtu: mtu, dialer: dialer}
	return f.copyFromDownstream()
}

func (f *forwarder) copyFromDownstream() error {
	b := make([]byte, f.mtu)
	for {
		n, err := f.downstream.Read(b)
		if err != nil {
			if err != io.EOF {
				err = log.Errorf("Unexpected error reading from downstream: %v", err)
			}
			f.closeUpstream()
			return err
		}

		err = f.writeToUpstream(b[:n])
		if err != nil {
			return err
		}
	}
}

func (f *forwarder) copyToDownstream(upstreamConn net.Conn, upstream io.ReadWriteCloser) {
	b := make([]byte, f.mtu)
	for {
		// Don't block for too long waiting for data from upstream. If this times
		// out, we'll just re-dial later
		upstreamConn.SetReadDeadline(time.Now().Add(ioTimeout))
		n, err := upstream.Read(b)
		if err != nil {
			upstream.Close()
			return
		}

		_, err = f.downstream.Write(b[:n])
		if err != nil {
			f.stopErr.Store(err)
			upstream.Close()
			return
		}
	}
}

func (f *forwarder) writeToUpstream(b []byte) error {
	// Keep trying to transmit the client packet
	attempts := float64(-100000)
	sleepTime := 50 * time.Millisecond
	maxSleepTime := ioTimeout

	for {
		if attempts > -1 {
			sleepTime := time.Duration(math.Pow(2, attempts)) * sleepTime
			if sleepTime > maxSleepTime {
				sleepTime = maxSleepTime
			}
			log.Debugf("Sleeping %v", sleepTime)
			time.Sleep(sleepTime)
		}
		attempts++

		if f.upstreamConn == nil {
			_se := f.stopErr.Load()
			if _se != nil {
				se := _se.(error)
				if se != io.EOF {
					se = log.Errorf("Unexpected error reading packet: %v", se)
				}
				return se
			}

			var dialErr error
			f.upstreamConn, dialErr = f.dialer.Dial("tcp", f.packetForwardServerAddr)
			if dialErr != nil {
				log.Errorf("Error dialing SOCKS proxy: %v, will retry", dialErr)
				continue
			}

			f.upstream = framed.NewReadWriteCloser(f.upstreamConn)
			go f.copyToDownstream(f.upstreamConn, f.upstream)
		}

		_, writeErr := f.upstream.Write(b)
		if writeErr != nil {
			f.closeUpstream()
			log.Errorf("Unexpected error writing to upstream: %v", writeErr)
			continue
		}

		return nil
	}
}

func (f *forwarder) closeUpstream() {
	if f.upstream != nil {
		f.upstream.Close()
		f.upstream = nil
		f.upstreamConn = nil
	}
}
