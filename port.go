package libtorrent

import (
	"bytes"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
)

var tcpPort string
var udpPort string

var (
	RefreshPort = (1 * time.Minute).Nanoseconds()
)

type PortInfo struct {
	TCP string
	UDP string
}

func PortMapping() *PortInfo {
	return &PortInfo{tcpPort, udpPort}
}

func PortCheck() bool {
	port := tcpPort
	if port == "" {
		// check does not perfome on UDP but what we can do?
		port = udpPort
	}
	if port == "" {
		// ports are not forwarded? using local socket port
		_, port, err = net.SplitHostPort(clientAddr)
		if err != nil {
			return false
		}
	}
	url := "http://portcheck.transmissionbt.com/" + port

	var resp *http.Response
	resp, err = http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	s := buf.String()

	return s == "1"
}

func getPort(d nat.Device, proto nat.Protocol, port int, extPort string) (int, error) {
	n := "libtorrent " + strings.ToLower(string(proto))

	_, ep, err := net.SplitHostPort(extPort)
	if err == nil && ep != "" {
		extPort = ep
	}

	ext, err := net.LookupPort("tcp", extPort)
	if err != nil || ext == 0 {
		ext = port
	}

	lease := 2 * time.Duration(RefreshPort) * time.Nanosecond

	// try specific port
	p, err := d.AddPortMapping(proto, port, ext, n, lease)
	if err == nil {
		return p, nil
	}

	// try random port
	p, err = d.AddPortMapping(proto, port, 0, n, lease)
	if err == nil {
		return p, nil
	}

	// try rand port
	for i := 0; i < 10; i++ {
		// Then try up to ten random ports.
		extPort := 1024 + rand.Intn(65535-1024)

		p, err = d.AddPortMapping(proto, port, extPort, n, lease)
		if err == nil {
			return p, nil
		}
	}

	return 0, err
}

func mapping(timeout time.Duration) error {
	_, pp, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return err
	}

	localport, err := net.LookupPort("tcp", pp)
	if err != nil {
		return err
	}

	dd := upnp.Discover(timeout, timeout)

	tcp := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		mu.Lock()
		pp := tcpPort
		if pp == "" {
			pp = udpPort
		}
		mu.Unlock()
		p, err := getPort(d, nat.TCP, localport, pp)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		tcpPort = net.JoinHostPort(ext.String(), strconv.Itoa(p))
		return nil
	}

	udp := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		mu.Lock()
		pp := udpPort
		if pp == "" {
			pp = tcpPort
		}
		mu.Unlock()
		p, err := getPort(d, nat.UDP, localport, pp)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		udpPort = net.JoinHostPort(ext.String(), strconv.Itoa(p))
		return nil
	}

	for _, d := range dd {
		if udp != nil {
			if err := udp(d); err == nil {
				udp = nil
			}
		}
		if tcp != nil {
			if err := tcp(d); err == nil {
				tcp = nil
			}
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if tcp != nil {
		tcpPort = ""
	}

	if udp != nil {
		udpPort = ""
	}

	// udp have priority we are using uTP
	if udpPort == "" {
		tcpPort = ""
		updateClientAddr(clientAddr)
		return nil
	}

	if tcpPort != udpPort {
		// if we got different TCP port, reset it
		tcpPort = ""
		updateClientAddr(udpPort)
		return nil
	}

	if tcpPort == udpPort {
		updateClientAddr(udpPort)
		return nil
	}

	return nil
}

func updateClientAddr(addr string) {
	old := client.ListenAddr().String()
	if old == addr {
		return
	}
	client.SetListenAddr(addr)
}
