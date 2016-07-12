package libtorrent

import (
	"bytes"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
)

var tcpPort string
var udpPort string
var mappingAddr []string // clientAddr when mapping called
var clientPorts []string

var mappingClose missinggo.Event

var (
	RefreshPort = (1 * time.Minute).Nanoseconds()
)

func localIP(gip net.IP) (ips []string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue // not an ipv4 address
			}
			if gip != nil && ip.Mask(ip.DefaultMask()).Equal(gip.Mask(gip.DefaultMask())) {
				ips = append(ips, ip.String())
			} else {
				ips = append(ips, ip.String())
			}
		}
	}
	return
}

func PortCount() int {
	mu.Lock()
	defer mu.Unlock()

	clientPorts = portList()

	if udpPort != "" {
		clientPorts = append(clientPorts, udpPort)
	}

	return len(clientPorts)
}

func portList() []string {
	var ports []string

	host, port, err := net.SplitHostPort(clientAddr)
	if err != nil {
		ports = append(ports, clientAddr)
	} else {
		if host == "" || host == "::" {
			ips := localIP(nil)
			if len(ips) == 0 {
				ports = append(ports, net.JoinHostPort(host, port))
			} else {
				for _, v := range ips {
					ports = append(ports, net.JoinHostPort(v, port))
				}
			}
		} else {
			ports = append(ports, net.JoinHostPort(host, port))
		}
	}
	return ports
}

func Port(i int) string {
	mu.Lock()
	defer mu.Unlock()
	return clientPorts[i]
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
	} else {
		_, port, err = net.SplitHostPort(port)
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

func mappingPort(timeout time.Duration) error {
	mu.Lock()
	_, pp, err := net.SplitHostPort(clientAddr)
	mu.Unlock()
	if err != nil {
		return err
	}

	localport, err := net.LookupPort("tcp", pp)
	if err != nil {
		return err
	}

	dd := upnp.Discover(timeout, timeout)

	u := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		mu.Lock()
		pp := udpPort // reuse old port
		if pp == "" {
			pp = tcpPort // reuse tcp port
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
	udp := u

	t := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		mu.Lock()
		pp := tcpPort // reuse old port
		if pp == "" {
			pp = udpPort // reuse udp port
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
	tcp := t

	// start udp priority
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

	// start tcp priority
	mu.Lock()
	if udpPort != tcpPort { // ooops...
		if tcpPort != "" { // tcp assigned, so UPnP/NAP-PMP working.
			// did we miss udp port or tcp is different? which menas we unable to get tcp port number same as udp port.
			// we need to reset udp port and try assign udp port number same as tcp port.
			if udpPort != "" { // udp assgined so UPnP/NAP-PMP udp working.
				udpPort = ""
				mu.Unlock()
				udp = u
				for _, d := range dd {
					if udp != nil {
						if err := udp(d); err == nil {
							udp = nil
						}
					}
				}
				mu.Lock()
				if udpPort == "" { // unable to assign udp port reset booth
					udpPort = ""
					tcpPort = ""
				}
			}
		}
	}
	mu.Unlock()

	mu.Lock()
	defer mu.Unlock()

	if tcp != nil {
		tcpPort = ""
	}

	if udp != nil {
		udpPort = ""
	}

	// udp have priority we are using uTP
	if udpPort == "" { // udp == tcp == ""
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

	if tcpPort == udpPort { // finnely!
		updateClientAddr(udpPort)
		return nil
	}

	return nil // never here
}

func updateClientAddr(addr string) {
	old := client.ListenAddr().String()
	if old == addr {
		return
	}
	client.SetListenAddr(addr)
}

func mappingStart() {
	mu.Lock()
	mappingClose.Set()
	mappingClose.Clear()
	mu.Unlock()

	refresh := RefreshPort

	if udpPort == "" { // start from 1 second if previous mapping failed
		refresh = (1 * time.Second).Nanoseconds()
	}

	for {
		select {
		case <-mappingClose.LockedChan(&mu):
			return
		case <-client.Wait():
			return
		case <-time.After(time.Duration(refresh) * time.Nanosecond):
		}
		// in go routine do 1 seconds discovery
		mappingPort(1 * time.Second)
		if udpPort != "" { // on success, normal refresh rate
			refresh = RefreshPort
		} else {
			refresh = refresh * 2
		}
		if refresh > RefreshPort {
			refresh = RefreshPort
		}
	}
}

func mappingStop() {
	mappingClose.Set()
	tcpPort = ""
	udpPort = ""
	mappingAddr = nil
	updateClientAddr(clientAddr)
}
