package libtorrent

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var lpd *LPDServer

// http://bittorrent.org/beps/bep_0014.html

// TODO http://bittorrent.org/beps/bep_0026.html

const (
	bep14_host4    = "239.192.152.143:6771"
	bep14_host6    = "[ff15::efc0:988f]:6771"
	bep14_announce = "BT-SEARCH * HTTP/1.1\r\n" +
		"Host: %s\r\n" +
		"Port: %s\r\n" +
		"%s" +
		"\r\n" +
		"\r\n"
	bep14_announce_infohash = "Infohash: %s\r\n"
	bep14_long_timeout      = 5 * time.Minute
	bep14_short_timeout     = 2 * time.Second // bep14 - 1 minute
	bep14_max               = 1               // maximum hashes per request, 0 - only limited by udp packet size
)

type LPDConn struct {
	stop  missinggo.Event
	force missinggo.Event

	network string // "udp4" or "udp6"
	addr    *net.UDPAddr
	conn    *net.UDPConn
	host    string // bep14_host4 or bep14_host6
}

func lpdConnNew(network string, host string) *LPDConn {
	m := &LPDConn{}

	m.network = network
	m.host = host

	var err error

	m.addr, err = net.ResolveUDPAddr(m.network, m.host)
	if err != nil {
		log.Println("LPD unable to start", err)
		return nil
	}
	m.conn, err = net.ListenMulticastUDP(m.network, nil, m.addr)
	if err != nil {
		log.Println("LPD unable to start", err)
		return nil
	}

	return m
}

func (m *LPDConn) receiver() {
	for {
		mu.Lock()
		conn := m.conn
		if conn == nil {
			mu.Unlock()
			return
		}
		mu.Unlock()

		buf := make([]byte, 2000)
		_, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Println("receiver", err)
			continue
		}

		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf)))
		if err != nil {
			log.Println("receiver", err)
			continue
		}

		if req.Method != "BT-SEARCH" {
			log.Println("receiver", "Wrong request: ", req.Method)
			continue
		}

		ih := req.Header.Get("Infohash")
		if ih == "" {
			log.Println("receiver", "No Infohash")
			continue
		}

		port := req.Header.Get("Port")
		if port == "" {
			log.Println("receiver", "No port")
			continue
		}

		addr, err := net.ResolveUDPAddr(m.network, net.JoinHostPort(from.IP.String(), port))
		if err != nil {
			log.Println("receiver", err)
			continue
		}

		mu.Lock()
		if lpd == nil { // can be closed already
			mu.Unlock()
			return
		}
		lpd.peer(addr.String())
		lpd.refresh()
		//log.Println("LPD", m.network, addr.String(), ih)
		hash := metainfo.NewHashFromHex(ih)
		if t, ok := client.Torrent(hash); ok {
			lpdPeer(t, addr.String())
		} else {
			// LPD is the only source for local IP's add to all active torrents
			for t := range active {
				lpdPeer(t, addr.String())
			}
		}
		mu.Unlock()
	}
}

func (m *LPDConn) announcer() {
	var refresh time.Duration = 0
	var next *torrent.Torrent
	var queue []*torrent.Torrent

	for {
		mu.Lock()
		m.force.Clear()
		mu.Unlock()

		//log.Println("LPD", refresh)

		select {
		case <-m.stop.LockedChan(&mu):
			return
		case <-m.force.LockedChan(&mu):
		case <-time.After(refresh):
		}

		mu.Lock()
		// add missing torrent to send queue
		for t := range active {
			if _, ok := lpdContains(queue, t); !ok {
				queue = append(queue, t)
			}
		}

		if next == nil {
			if len(queue) > 0 {
				next = queue[0]
			}
		}

		// remove stopped torrent from queue
		var remove []*torrent.Torrent
		for _, t := range queue {
			if _, ok := active[t]; !ok {
				remove = append(remove, t)
			}
		}
		for _, t := range remove {
			if i, ok := lpdContains(queue, t); ok {
				if next == t { // update next to next+1
					n := i + 1
					if n >= len(queue) {
						next = nil
					} else {
						next = queue[n]
					}
				}
				queue = append(queue[:i], queue[i+1:]...)
			}
		}
		lpd.refresh()

		var ihs string
		var old []byte

		_, port, err := net.SplitHostPort(clientAddr)
		if err != nil {
			mu.Unlock()
			log.Println("announcer", err)
			continue
		}
		count := 0
		for next != nil {
			ihs += fmt.Sprintf(bep14_announce_infohash, strings.ToUpper(next.InfoHash().HexString()))
			req := fmt.Sprintf(bep14_announce, m.host, port, ihs)
			buf := []byte(req)
			if len(buf) >= 1400 {
				break
			}
			old = buf
			if i, ok := lpdContains(queue, next); ok {
				i++
				if i >= len(queue) {
					next = nil
				} else {
					next = queue[i]
				}
			}
			count++
			if bep14_max > 0 && count >= bep14_max {
				break
			}
		}
		mu.Unlock()

		if len(old) > 0 {
			//log.Println("LPD", string(old), len(old))
			_, err = m.conn.WriteToUDP(old, m.addr)
			if err != nil {
				log.Println("announcer", err)
			}
		}

		// sholud we wait for 5 min or 1 min, if we still announcing
		refresh = bep14_short_timeout
		if next == nil { // restart queue
			refresh = bep14_long_timeout
		}
	}
}

func (m *LPDConn) Close() {
	m.stop.Set()
	if m.conn != nil {
		m.conn.Close()
		m.conn = nil
	}
}

type LPDServer struct {
	conn4 *LPDConn
	conn6 *LPDConn

	peers map[int64]string // active local peers
}

func lpdStart() {
	lpd = &LPDServer{}

	lpd.peers = make(map[int64]string)

	lpd.conn4 = lpdConnNew("udp4", bep14_host4)
	if lpd.conn4 != nil {
		go lpd.conn4.receiver()
		go lpd.conn4.announcer()

	}

	lpd.conn6 = lpdConnNew("udp6", bep14_host6)
	if lpd.conn6 != nil {
		go lpd.conn6.receiver()
		go lpd.conn6.announcer()
	}

	return
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

func (m *LPDServer) peer(peer string) {
	now := time.Now().UnixNano()

	var remove []int64
	for k, v := range m.peers {
		if v == peer {
			remove = append(remove, k)
		}
	}

	m.peers[now] = peer

	for _, v := range remove {
		delete(m.peers, v)
	}
}

func lpdContains(queue []*torrent.Torrent, e *torrent.Torrent) (int, bool) {
	for i, t := range queue {
		if t == e {
			return i, true
		}
	}
	return -1, false
}

func lpdForce() {
	if lpd.conn4 != nil {
		lpd.conn4.force.Set()
	}
	if lpd.conn6 != nil {
		lpd.conn6.force.Set()
	}
}

func lpdStop() {
	if lpd != nil {
		if lpd.conn4 != nil {
			lpd.conn4.Close()
			lpd.conn4 = nil
		}
		if lpd.conn6 != nil {
			lpd.conn6.Close()
			lpd.conn6 = nil
		}
		lpd = nil
	}
}

func lpdPeers(t *torrent.Torrent) {
	for _, p := range lpd.peers {
		lpdPeer(t, p)
	}
}

func lpdCount(hash metainfo.Hash) int {
	return len(lpd.peers)
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
	ip := net.ParseIP(host)
	peer := torrent.Peer{
		IP:     ip,
		Port:   pi,
		Source: peerSourceLPD,
	}
	t.AddPeers([]torrent.Peer{peer})
}
