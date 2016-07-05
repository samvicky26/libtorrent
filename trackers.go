package libtorrent

type Tracker struct {
	// Tracker URI or DHT, LSD, PE
	Addr         string
	Error        string
	LastAnnounce int64
	NextAnnounce int64
	Peers        int

	// scrape info
	LastScrape int64
	Seeders    int
	Leechers   int
	Downloaded int
}

func TorrentTrackersCount(i int) int {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	f := filestorage[t.InfoHash()]
	f.Trackers = nil
	for _, v := range t.Trackers() {
		f.Trackers = append(f.Trackers, Tracker{v.Url, v.Err, v.LastAnnounce, v.NextAnnounce, v.Peers, 0, 0, 0, 0})
	}
	return len(f.Trackers)
}

func TorrentTrackers(i int, p int) *Tracker {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return &f.Trackers[p]
}

func TorrentTrackerRemove(i int, url string) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	t.RemoveTracker(url)
}

func TorrentTrackerAdd(i int, addr string) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	t.AddTrackers([][]string{[]string{addr}})
}
