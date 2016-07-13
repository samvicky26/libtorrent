package libtorrent

type Peer struct {
	Id     [20]byte
	Name   string
	Addr   string
	Source string
	// Peer is known to support encryption.
	SupportsEncryption bool
	PiecesCompleted    int
	// how many data we downloaded/uploaded from peer
	Downloaded int64
	Uploaded   int64
}

const (
	peerSourceTracker  = '\x00' // It's the default.
	peerSourceIncoming = 'I'
	peerSourceDHT      = 'H'
	peerSourcePEX      = 'X'
	peerSourceLPD      = 'L'
)

func TorrentPeersCount(i int) int {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	f := filestorage[t.InfoHash()]

	f.Peers = nil

	for _, v := range t.Peers() {
		var p string
		switch v.Source {
		case peerSourceTracker:
			p = "Tracker"
		case peerSourceIncoming:
			p = "Incoming"
		case peerSourceDHT:
			p = "DHT"
		case peerSourcePEX:
			p = "PEX"
		case peerSourceLPD:
			p = "LPD"
		}
		f.Peers = append(f.Peers, Peer{v.Id, v.Name, v.Addr, p, v.SupportsEncryption, v.PiecesCompleted, v.Downloaded, v.Uploaded})
	}

	return len(f.Peers) // t.PeersCount()
}

func TorrentPeers(i int, p int) *Peer {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return &f.Peers[p]
}
