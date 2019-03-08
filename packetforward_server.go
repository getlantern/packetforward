package packetforward

import (
	"io"
	"math"
	"net"
	"sync"
	"time"

	"github.com/getlantern/eventual"
	"github.com/getlantern/framed"
	"github.com/getlantern/ipproxy"
)

const (
	maxListenDelay = 1 * time.Second

	baseIODelay = 10 * time.Millisecond
	maxIODelay  = 1 * time.Second
)

type Server interface {
	Serve(l net.Listener) error

	Close() error
}

type server struct {
	opts      *ipproxy.Opts
	clients   map[string]*client
	clientsMx sync.Mutex
}

func NewServer(opts *ipproxy.Opts) Server {
	opts = opts.ApplyDefaults()
	opts.MTU -= framed.FrameHeaderLength // leave room for framed header

	return &server{
		opts:    opts,
		clients: make(map[string]*client),
	}
}

func (s *server) Close() error {
	// TODO: implement
	return nil
}

func (s *server) Serve(l net.Listener) error {
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
		s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	// use framed protocol
	framedConn := framed.NewReadWriteCloser(conn)

	// Read client ID
	b := make([]byte, 36)
	_, err := framedConn.Read(b)
	if err != nil {
		log.Errorf("Unable to read client ID: %v", err)
		return
	}
	id := string(b)
	log.Debugf("Read client id: %v", id)

	s.clientsMx.Lock()
	c := s.clients[id]
	if c == nil {
		log.Debug("new client")
		c = &client{
			framedConn: eventual.NewValue(),
		}

		ipp, err := ipproxy.New(c, s.opts)
		if err != nil {
			log.Errorf("Unable to open ipproxy: %v", err)
			return
		}
		go func() {
			if serveErr := ipp.Serve(); serveErr != nil {
				log.Errorf("Error handling packets: %v", serveErr)
			}
		}()
		s.clients[id] = c
	}
	s.clientsMx.Unlock()

	c.attach(framedConn)
}

type client struct {
	framedConn eventual.Value
}

func (c *client) getFramedConn() io.ReadWriteCloser {
	framedConn, _ := c.framedConn.Get(eventual.Forever)
	return framedConn.(io.ReadWriteCloser)
}

func (c *client) attach(framedConn io.ReadWriteCloser) {
	log.Debug("Attaching")
	oldFramedConn, _ := c.framedConn.Get(0)
	if oldFramedConn != nil {
		log.Debug("Closing old connection")
		oldFramedConn.(io.Closer).Close()
	}
	c.framedConn.Set(framedConn)
	log.Debug("Attached")
}

func (c *client) Read(b []byte) (int, error) {
	i := float64(0)
	for {
		n, err := c.getFramedConn().Read(b)
		if err == nil {
			return n, err
		}
		// ignore errors and retry, because clients can reconnect
		sleepTime := time.Duration(math.Pow(2, i)) * baseIODelay
		if sleepTime > maxIODelay {
			sleepTime = maxIODelay
		}
		time.Sleep(sleepTime)
		i++
	}
}

func (c *client) Write(b []byte) (int, error) {
	i := float64(0)
	for {
		n, err := c.getFramedConn().Write(b)
		if err == nil {
			return n, err
		}
		// ignore errors and retry, because clients can reconnect
		sleepTime := time.Duration(math.Pow(2, i)) * baseIODelay
		if sleepTime > maxIODelay {
			sleepTime = maxIODelay
		}
		time.Sleep(sleepTime)
		i++
	}
}
