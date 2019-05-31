package server

import (
	"time"
)

func (s *server) printStats() {
	defer close(s.closed)
	defer s.opts.Opts.StatsTracker.Close()

	ticker := time.NewTicker(s.opts.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.close:
			return
		case <-ticker.C:
			s.clientsMx.Lock()
			numClients := len(s.clients)
			s.clientsMx.Unlock()
			log.Debugf("Number of Clients: %d", numClients)
		}
	}
}
