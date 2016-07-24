# libtorrent

Go torrent library with nice and simple interface. Can be used as standart Go library or gomobile library (iOS / Android)

## Features

Base Features (https://github.com/anacrolix/torrent):

 * Protocol obfuscation
 * DHT
 * uTP
 * PEX
 * Magnet links
 * IP Blocklists
 * Some IPv6
 * HTTP and UDP tracker clients
 * BEPs:
  -  3: Basic BitTorrent protocol
  -  5: DHT
  -  6: Fast Extension (have all/none only)
  -  7: IPv6 Tracker Extension
  -  9: ut_metadata
  - 10: Extension protocol
  - 11: PEX
  - 12: Multitracker metadata extension
  - 15: UDP Tracker Protocol
  - 20: Peer ID convention ("-GTnnnn-")
  - 23: Tracker Returns Compact Peer Lists
  - 27: Private torrents
  - 29: uTorrent transport protocol
  - 41: UDP Tracker Protocol Extensions
  - 42: DHT Security extension
  - 43: Read-only DHT Nodes

Additional features:
  * UPnP / PMP
  * Rename Torrent
  * Runtime torrent states (save state between restarts)
  * BEPs
    - 14: Local Peers Discovery
  * Queue Engine
  * Full Contorl over torrent state (download, stop, pause, resume)

## Headers

`# go tool cgo libtorrent.go`

## Android

Use within your android gralde project:

```
# gomobile bind -o libtorrent.aar github.com/axet/libtorrent
```

Then import your libtorrent.arr into Android Studio or Eclipse.

## Examples

```go
func createTorrentFileExample() {
	t1 := libtorrent.CreateTorrentFile("/Users/axet/Downloads/Prattchet")
	ioutil.WriteFile("./test.torrent", t1, 0644)
}

func downloadMagnetWaitExample() {
	libtorrent.Create()
	t1 := libtorrent.AddMagnet("/tmp", "magnet:?...")
	libtorrent.StartTorrent(t1)
	libtorrent.WaitAll()
	log.Println("done")
	libtorrent.Close()
}

func downloadMagnetStatusExample() {
	libtorrent.Create()
	t1 := libtorrent.AddMagnet("/tmp", "magnet:?...")
	libtorrent.StartTorrent(t1)
	for libtorrent.TorrentStatus(t1) == libtorrent.StatusDownloading {
		time.Sleep(100 * time.Millisecond)
		log.Println("loop")
	}
	log.Println("done")
	libtorrent.Close()
}
```
