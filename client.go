package packetforward

import (
	"context"
	"io"
	"math"
	"net"
	"time"

	"github.com/getlantern/framed"
	"github.com/getlantern/golog"
	"github.com/getlantern/idletiming"
	"github.com/getlantern/uuid"
)

var log = golog.LoggerFor("packetforward")

const (
	maxDialDelay = 1 * time.Second
)

type DialFunc func(ctx context.Context) (net.Conn, error)

type forwarder struct {
	id                    string
	downstream            io.Writer
	mtu                   int
	idleTimeout           time.Duration
	dialServer            DialFunc
	upstreamConn          net.Conn
	upstream              io.ReadWriteCloser
	copyToDownstreamError chan error
}

func Client(downstream io.Writer, mtu int, idleTimeout time.Duration, dialServer DialFunc) io.WriteCloser {
	id := uuid.New().String()
	return &forwarder{
		id:                    id,
		downstream:            downstream,
		mtu:                   mtu,
		idleTimeout:           idleTimeout,
		dialServer:            dialServer,
		copyToDownstreamError: make(chan error, 1),
	}
}

func (f *forwarder) Write(b []byte) (int, error) {
	writeErr := f.writeToUpstream(b)
	if writeErr != nil {
		return 0, writeErr
	}
	return len(b), nil
}

func (f *forwarder) Close() error {
	f.closeUpstream()
	return nil
}

func (f *forwarder) copyToDownstream(upstreamConn net.Conn, upstream io.ReadWriteCloser) {
	b := make([]byte, f.mtu)
	for {
		n, readErr := upstream.Read(b)
		if n > 0 {
			_, writeErr := f.downstream.Write(b[:n])
			if writeErr != nil {
				upstream.Close()
				f.copyToDownstreamError <- writeErr
				return
			}
		}
		if readErr != nil {
			upstream.Close()
			f.copyToDownstreamError <- readErr
			return
		}
	}
}

func (f *forwarder) writeToUpstream(b []byte) error {
	// Keep trying to transmit the client packet
	attempts := float64(-100000)
	sleepTime := 50 * time.Millisecond
	maxSleepTime := f.idleTimeout

	firstDial := true
	for {
		if attempts > -1 {
			sleepTime := time.Duration(math.Pow(2, attempts)) * sleepTime
			if sleepTime > maxSleepTime {
				sleepTime = maxSleepTime
			}
			time.Sleep(sleepTime)
		}
		attempts++

		if f.upstreamConn == nil {
			if !firstDial {
				// wait for copying to downstream to finish
				<-f.copyToDownstreamError
			} else {
				firstDial = false
			}

			ctx, cancel := context.WithTimeout(context.Background(), f.idleTimeout)
			upstreamConn, dialErr := f.dialServer(ctx)
			cancel()
			if dialErr != nil {
				log.Errorf("Error dialing upstream, will retry: %v", dialErr)
				continue
			}
			f.upstreamConn = idletiming.Conn(upstreamConn, f.idleTimeout, nil)
			rwc := framed.NewReadWriteCloser(f.upstreamConn)
			rwc.EnableBigFrames()
			f.upstream = rwc
			if _, err := f.upstream.Write([]byte(f.id)); err != nil {
				log.Errorf("Error sending client ID to upstream, will retry: %v", err)
				continue
			}
			go f.copyToDownstream(f.upstreamConn, f.upstream)
		}

		attempts = -1

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
