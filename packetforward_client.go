package packetforward

import (
	"context"
	"io"
	"math"
	"net"
	"sync/atomic"
	"time"

	"github.com/getlantern/framed"
)

const (
	maxDialDelay = 1 * time.Second

	ioTimeout = 30 * time.Second
)

type DialFunc func(ctx context.Context) (net.Conn, error)

type forwarder struct {
	downstream   io.ReadWriteCloser
	mtu          int
	dialServer   DialFunc
	upstreamConn net.Conn
	upstream     io.ReadWriteCloser
	stopErr      atomic.Value
}

func Client(downstream io.ReadWriteCloser, mtu int, dialServer DialFunc) error {
	f := &forwarder{downstream: downstream, mtu: mtu, dialServer: dialServer}
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
		n, readErr := upstream.Read(b)
		if n > 0 {
			_, writeErr := f.downstream.Write(b[:n])
			if writeErr != nil {
				f.stopErr.Store(writeErr)
				upstream.Close()
				return
			}
		}
		if readErr != nil {
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

			ctx, cancel := context.WithTimeout(context.Background(), ioTimeout)
			var dialErr error
			f.upstreamConn, dialErr = f.dialServer(ctx)
			cancel()
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
