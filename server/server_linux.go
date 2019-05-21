// server provides the server end of packetforward functionality. The server reads
// IP packets from the client's connection, forwards these to the final origin using
// gonat and writes response packets back to the client.
package server

import (
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getlantern/eventual"
	"github.com/getlantern/framed"
	"github.com/getlantern/golog"
	"github.com/getlantern/gonat"
	"github.com/getlantern/idletiming"
)

var log = golog.LoggerFor("packetforward")

const (
	// DefaultBufferPoolSize is 1MB
	DefaultBufferPoolSize = 1000000
)

const (
	maxListenDelay = 1 * time.Second

	baseIODelay = 10 * time.Millisecond
	maxIODelay  = 1 * time.Second
)

type Opts struct {
	gonat.Opts

	// BufferPoolSize is the size of the buffer pool in bytes. If not specified, defaults to 1 MB
	BufferPoolSize int
}

type Server interface {
	Serve(l net.Listener) error
}

type server struct {
	opts      *Opts
	clients   map[string]*client
	clientsMx sync.Mutex
}

// NewServer constructs a new unstarted packetforward Server. The server can be started by
// calling Serve().
func NewServer(opts *Opts) (Server, error) {
	if opts.BufferPoolSize <= 0 {
		opts.BufferPoolSize = DefaultBufferPoolSize
	}

	// Apply defaults
	err := opts.ApplyDefaults()
	if err != nil {
		return nil, err
	}

	opts.BufferPool = framed.NewHeaderPreservingBufferPool(opts.BufferPoolSize, gonat.MaximumIPPacketSize)

	s := &server{
		opts:    opts,
		clients: make(map[string]*client),
	}
	go s.printStats()
	return s, nil
}

// Serve serves new packetforward client connections inbound on the given Listener.
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
	framedConn.EnableBigFrames()
	framedConn.DisableThreadSafety()
	framedConn.EnableBuffering(gonat.MaximumIPPacketSize)

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
		efc := eventual.NewValue()
		efc.Set(framedConn)
		c = &client{
			id:         id,
			s:          s,
			framedConn: efc,
		}

		gn, err := gonat.NewServer(c, &s.opts.Opts)
		if err != nil {
			log.Errorf("Unable to open gonat: %v", err)
			return
		}
		go func() {
			if serveErr := gn.Serve(); serveErr != nil {
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
	s.clients = make(map[string]*client)
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
	framedConn eventual.Value
	lastActive int64
	mx         sync.RWMutex
}

func (c *client) getFramedConn(timeout time.Duration) *framed.ReadWriteCloser {
	_framedConn, ok := c.framedConn.Get(timeout)
	if !ok {
		return nil
	}
	return _framedConn.(*framed.ReadWriteCloser)
}

func (c *client) attach(framedConn io.ReadWriteCloser) {
	oldFramedConn := c.getFramedConn(0)
	if oldFramedConn != nil {
		go oldFramedConn.Close()
	}
	c.framedConn.Set(framedConn)
}

func (c *client) Read(b []byte) (int, error) {
	i := float64(0)
	for {
		conn := c.getFramedConn(c.s.opts.IdleTimeout)
		if conn == nil {
			return c.idled(io.EOF)
		}
		n, err := conn.Read(b)
		if err == nil {
			c.markActive()
			return n, err
		}
		if c.idle() {
			return c.idled(io.EOF)
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
		conn := c.getFramedConn(c.s.opts.IdleTimeout)
		if conn == nil {
			return c.idled(io.EOF)
		}
		n, err := conn.WriteAtomic(b)
		if err == nil {
			c.markActive()
			return n, err
		}
		if c.idle() {
			return c.idled(idletiming.ErrIdled)
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

func (c *client) idled(err error) (int, error) {
	c.s.forgetClient(c.id)
	return 0, io.EOF
}

func (c *client) markActive() {
	atomic.StoreInt64(&c.lastActive, time.Now().UnixNano())
}

func (c *client) idle() bool {
	return time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&c.lastActive)) > c.s.opts.IdleTimeout
}
