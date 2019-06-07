package server

import (
	"net"

	"github.com/getlantern/gonat"
)

type Opts struct {
	gonat.Opts

	// BufferPoolSize is the size of the buffer pool in bytes. If not specified, defaults to 1 MB
	BufferPoolSize int
}

type Server interface {
	Serve(l net.Listener) error

	// Close closes this server and associated resources.
	Close() error
}
