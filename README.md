# libtorrent

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

func downloadMagnetExample() {
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
