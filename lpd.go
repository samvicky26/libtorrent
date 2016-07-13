package libtorrent

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var lpd *LPDServer

// http://bittorrent.org/beps/bep_0014.html

// TODO http://bittorrent.org/beps/bep_0026.html

const (
	host           = "239.192.152.143:6771"
	bep14_announce = "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: %s\r\n" +
		"Port: %s\r\n" +
		"Infohash: %s\r\n\r\n"
	bep14_long_timeout  = 5 * time.Minute
	bep14_short_timeout = 1 * time.Minute
)

type LPDServer struct {
	stop  missinggo.Event
	force missinggo.Event

	addr  *net.UDPAddr
	conn  *net.UDPConn
	peers map[int64]string // active local peers

	queue []*torrent.Torrent
	next  *torrent.Torrent
}

func lpdStart() {
	lpd = &LPDServer{}

	lpd.peers = make(map[int64]string)

	lpd.addr, err = net.ResolveUDPAddr("udp4", host)
	if err != nil {
		log.Println("LPT unable to start", err)
		return
	}

	lpd.conn, err = net.ListenMulticastUDP("udp4", nil, lpd.addr)
	if err != nil {
		log.Println("LPT unable to start", err)
		return
	}

	go lpd.receiver()
	go lpd.announcer()

	return
}

func (m *LPDServer) receiver() {
	for {
		buf := make([]byte, 1500)
		_, from, err := lpd.conn.ReadFromUDP(buf)
		if err != nil {
			log.Println("Local Announce read error: ", err)
			continue
		}

		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf)))
		if err != nil {
			log.Println("Local Announce error: ", err)
			continue
		}

		if req.Method != "BT-SEARCH" {
			log.Println("Wrong request: ", req.Method)
			continue
		}

		ih := req.Header.Get("Infohash")
		if ih == "" {
			log.Println("No Infohash")
			continue
		}

		var ihs []string
		for i := 0; i < len(ih); i += 40 {
			h := ih[i : i+40]
			ihs = append(ihs, h)
		}

		port := req.Header.Get("Port")
		if port == "" {
			log.Println("No port")
			continue
		}

		addr, err := net.ResolveTCPAddr("tcp4", net.JoinHostPort(from.IP.String(), port))
		if err != nil {
			log.Println(err)
			continue
		}
		now := time.Now().UnixNano()

		mu.Lock()
		m.peers[now] = addr.String()
		m.refresh()
		for _, h := range ihs {
			hash := metainfo.NewHashFromHex(h)
			if t, ok := client.Torrent(hash); ok {
				lpdPeer(t, addr.String())
			}
		}
		mu.Unlock()
	}
}

func (m *LPDServer) refresh() {
	now := time.Now().UnixNano()
	var remove []int64
	for t, _ := range m.peers {
		// remove old peers who did not refresh for 2 * bep14_long_timeout
		if t+(2*bep14_long_timeout).Nanoseconds() < now {
			remove = append(remove, t)
		}
	}
	for _, t := range remove {
		delete(m.peers, t)
	}
}

func (m *LPDServer) contains(e *torrent.Torrent) (int, bool) {
	for i, t := range m.queue {
		if t == e {
			return i, true
		}
	}
	return -1, false
}

func (m *LPDServer) announcer() {
	var refresh time.Duration = 0

	for {
		mu.Lock()
		m.force.Clear()
		mu.Unlock()

		select {
		case <-m.stop.LockedChan(&mu):
			return
		case <-m.force.LockedChan(&mu):
		case <-time.After(refresh):
		}

		mu.Lock()
		// add missing torrent to send queue
		for t := range active {
			if _, ok := m.contains(t); !ok {
				m.queue = append(m.queue, t)
			}
		}

		if m.next == nil {
			if len(m.queue) > 0 {
				m.next = m.queue[0]
			}
		}

		// remove stopped torrent from queue
		var remove []*torrent.Torrent
		for _, t := range m.queue {
			if _, ok := active[t]; !ok {
				remove = append(remove, t)
			}
		}
		for _, t := range remove {
			if i, ok := m.contains(t); ok {
				if m.next == t { // update next to next+1
					n := i + 1
					if n >= len(m.queue) {
						m.next = nil
					} else {
						m.next = m.queue[n]
					}
				}
				m.queue = append(m.queue[:i], m.queue[i+1:]...)
			}
		}
		mu.Unlock()

		var ihs string
		var old []byte

		mu.Lock()
		_, port, err := net.SplitHostPort(clientAddr)
		if err != nil {
			mu.Unlock()
			log.Println("Announce error", err)
			continue
		}
		for m.next != nil {
			ihs += m.next.InfoHash().HexString()
			req := fmt.Sprintf(bep14_announce, host, port, ihs)
			buf := []byte(req)
			if len(buf) >= 1400 {
				break
			}
			old = buf
			if i, ok := m.contains(m.next); ok {
				i++
				if i >= len(m.queue) {
					m.next = nil
				} else {
					m.next = m.queue[i]
				}
			}
		}
		mu.Unlock()

		_, err = m.conn.WriteToUDP(old, m.addr)
		if err != nil {
			log.Println("Announce error", err)
		}

		// sholud we wait for 5 min or 1 min, if we still announcing
		refresh = bep14_short_timeout
		if m.next == nil { // restart queue
			refresh = bep14_long_timeout
		}
	}
}

func lpdForce() {
	lpd.force.Set()
}

func lpdStop() {
	if lpd != nil {
		lpd.conn.Close()
		lpd.stop.Set()
		lpd = nil
	}
}

func lpdPeers(t *torrent.Torrent) {
	for _, p := range lpd.peers {
		lpdPeer(t, p)
	}
}

func lpdPeer(t *torrent.Torrent, p string) {
	host, port, err := net.SplitHostPort(p)
	if err != nil {
		return
	}
	pi, err := strconv.Atoi(port)
	if err != nil {
		return
	}
	peer := torrent.Peer{
		IP:     make([]byte, 4),
		Port:   pi,
		Source: peerSourceLPD,
	}
	ip := net.ParseIP(host)
	ip4 := ip.To4()
	missinggo.CopyExact(peer.IP, ip4[:])
	t.AddPeers([]torrent.Peer{peer})
}
