package packetforward

import (
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/framed"
	"github.com/getlantern/idletiming"
	"github.com/getlantern/ipproxy"
)

const (
	maxListenDelay = 1 * time.Second

	baseIODelay = 10 * time.Millisecond
	maxIODelay  = 1 * time.Second
)

type Server interface {
	Serve(l net.Listener) error
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

func (s *server) Serve(l net.Listener) error {
	defer s.forgetClients()

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

	s.clientsMx.Lock()
	c := s.clients[id]
	if c == nil {
		c = &client{
			id:         id,
			s:          s,
			framedConn: framedConn,
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
	} else {
		c.attach(framedConn)
	}
	s.clientsMx.Unlock()
}

func (s *server) forgetClients() {
	s.clientsMx.Lock()
	for id := range s.clients {
		delete(s.clients, id)
	}
	s.clientsMx.Unlock()
}

func (s *server) forgetClient(id string) {
	s.clientsMx.Lock()
	delete(s.clients, id)
	s.clientsMx.Unlock()
}

type client struct {
	id         string
	s          *server
	framedConn io.ReadWriteCloser
	lastActive int64
	mx         sync.RWMutex
}

func (c *client) getFramedConn() io.ReadWriteCloser {
	c.mx.RLock()
	framedConn := c.framedConn
	c.mx.RUnlock()
	return framedConn
}

func (c *client) attach(framedConn io.ReadWriteCloser) {
	c.mx.Lock()
	if c.framedConn != nil {
		go c.framedConn.Close()
	}
	c.framedConn = framedConn
}

func (c *client) Read(b []byte) (int, error) {
	i := float64(0)
	for {
		n, err := c.getFramedConn().Read(b)
		if err == nil {
			c.markActive()
			return n, err
		}
		if c.idle() {
			c.s.forgetClient(c.id)
			return 0, io.EOF
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
			c.markActive()
			return n, err
		}
		if c.idle() {
			return 0, idletiming.ErrIdled
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

func (c *client) markActive() {
	atomic.StoreInt64(&c.lastActive, time.Now().UnixNano())
}

func (c *client) idle() bool {
	return time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&c.lastActive)) > c.s.opts.IdleTimeout
}
