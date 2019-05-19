package main

import (
	"flag"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getlantern/golog"
	"github.com/getlantern/gonat"
	pserver "github.com/getlantern/packetforward/server"
)

var (
	log = golog.LoggerFor("packetforward-demo-client")
)

var (
	addr      = flag.String("addr", "127.0.0.1:9780", "address of server")
	tunGW     = flag.String("tun-gw", "10.0.0.1", "tun device gateway")
	ifOut     = flag.String("ifout", "", "name of interface to use for outbound connections")
	mtu       = flag.Int("mtu", 65535, "maximum transmission unit")
	tcpDest   = flag.String("tcpdest", "80.249.99.148", "destination to which to connect all TCP traffic")
	udpDest   = flag.String("udpdest", "8.8.8.8", "destination to which to connect all UDP traffic")
	pprofAddr = flag.String("pprofaddr", "", "pprof address to listen on, not activate pprof if empty")
)

func main() {
	flag.Parse()

	if *pprofAddr != "" {
		go func() {
			log.Debugf("Starting pprof page at http://%s/debug/pprof", *pprofAddr)
			srv := &http.Server{
				Addr: *pprofAddr,
			}
			if err := srv.ListenAndServe(); err != nil {
				log.Error(err)
			}
		}()
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	log.Debugf("Listening for packetforward connections at %v", l.Addr().String())

	ch := make(chan os.Signal, 1)
	signal.Notify(ch,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		<-ch
		log.Debug("Closing listener")
		l.Close()
		log.Debug("Closed listener")
	}()

	s := pserver.NewServer(&gonat.Opts{
		IFName:      *ifOut,
		IdleTimeout: 70 * time.Second,
		BufferDepth: 10000,
		OnOutbound: func(pkt *gonat.IPPacket) {
			pkt.SetDest(*tcpDest, pkt.FT().Dst.Port)
		},
		OnInbound: func(pkt *gonat.IPPacket, ft gonat.FourTuple) {
			pkt.SetSource(*tunGW, ft.Dst.Port)
		},
	})
	log.Debugf("Final result: %v", s.Serve(l))
}
