package server

import (
	"time"
)

func (s *server) printStats() {
	for {
		time.Sleep(s.opts.StatsInterval)
		s.clientsMx.Lock()
		numClients := len(s.clients)
		s.clientsMx.Unlock()
		log.Debugf("Number of Clients: %d", numClients)
	}
}
