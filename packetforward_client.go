package packetforward

import (
	"github.com/getlantern/errors"
	"github.com/getlantern/framed"
	"io"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

const (
	maxDialDelay = 1 * time.Second
)

type forwarder struct {
	downstream io.ReadWriteCloser
	mtu        int
	stopErr    atomic.Value
}

func To(socksProxyAddr string, packetForwardServerAddr string, downstream io.ReadWriteCloser, mtu int) error {
	f := &forwarder{downstream: downstream, mtu: mtu}
	return f.copyFromUpstream(socksProxyAddr, packetForwardServerAddr)
}

func (f *forwarder) copyFromUpstream(socksProxyAddr string, packetForwardServerAddr string) error {
	dialer, err := proxy.SOCKS5("tcp", socksProxyAddr, nil, &net.Dialer{})
	if err != nil {
		return errors.New("Unable to create SOCKS5 dialer: %v", err)
	}

	var upstreamConn net.Conn
	var upstream io.ReadWriteCloser

	closeUpstream := func() {
		if upstream != nil {
			upstream.Close()
			upstream = nil
			upstreamConn = nil
		}
	}

	tempDelay := time.Duration(0)
	b := make([]byte, f.mtu)
	for {
		if upstreamConn == nil {
			_se := f.stopErr.Load()
			if _se != nil {
				se := _se.(error)
				if se != io.EOF {
					return log.Errorf("Unexpected error reading packet: %v", err)
				}
				return nil
			}

			upstreamConn, err = dialer.Dial("tcp", packetForwardServerAddr)
			if err != nil {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > maxDialDelay {
					tempDelay = maxDialDelay
				}
				log.Errorf("Error dialing SOCKS proxy: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}

			tempDelay = 0
			upstream = framed.NewReadWriteCloser(upstreamConn)
			go f.copyToUpstream(upstream)
		}

		n, err := upstream.Read(b)
		if err != nil {
			closeUpstream()
			if err != io.EOF {
				log.Errorf("Unexpected error writing to upstream: %v", err)
			}
			continue
		}

		_, err = f.downstream.Write(b[:n])
		if err != nil {
			closeUpstream()
			return log.Errorf("Unexpected error writing to downstream: %v", err)
		}
	}
}

func (f *forwarder) copyToUpstream(upstream io.ReadWriteCloser) {
	b := make([]byte, f.mtu)
	for {
		n, err := f.downstream.Read(b)
		if err != nil {
			f.stopErr.Store(err)
			upstream.Close()
			return
		}

		_, err = upstream.Write(b[:n])
		if err != nil {
			log.Errorf("Unexpected error writing to upstream: %v", err)
			upstream.Close()
			return
		}
	}
}
