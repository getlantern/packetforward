package main

import (
	"context"
	"flag"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getlantern/golog"
	tun "github.com/getlantern/gotun"
	"github.com/getlantern/packetforward"
)

var (
	log = golog.LoggerFor("packetforward-demo-client")
)

var (
	tunDevice = flag.String("tun-device", "tun0", "tun device name")
	tunAddr   = flag.String("tun-address", "10.0.0.2", "tun device address")
	tunMask   = flag.String("tun-mask", "255.255.255.0", "tun device netmask")
	tunGW     = flag.String("tun-gw", "10.0.0.1", "tun device gateway")
	mtu       = flag.Int("mtu", 1500, "maximum transmission unit for TUN device")
	addr      = flag.String("addr", "127.0.0.1:9780", "address of server")
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

	dev, err := tun.OpenTunDevice(*tunDevice, *tunAddr, *tunGW, *tunMask, *mtu)
	if err != nil {
		log.Fatal(err)
	}
	defer dev.Close()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		<-ch
		log.Debug("Closing TUN device")
		dev.Close()
		log.Debug("Closed TUN device")
		os.Exit(0)
	}()

	log.Debugf("Using packetforward server at %v", *addr)
	var d net.Dialer
	c := packetforward.Client(dev, 70*time.Second, func(ctx context.Context) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", *addr)
	})

	log.Debug("Reading from TUN device")
	b := make([]byte, *mtu)
	for {
		n, err := dev.Read(b)
		if n > 0 {
			c.Write(b[:n])
		}
		if err != nil {
			if err != io.EOF {
				log.Errorf("Unexpected error reading from TUN device: %v", err)
			}
			return
		}
	}
}
