package libtorrent

// #include <stdlib.h>
import "C"

import (
	"bufio"
	"bytes"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var (
	SocketsPerTorrent = 40
	DefaultListenAddr = ":53007"
)

func SetDefaultAnnouncesList(str string) {
	mu.Lock()
	defer mu.Unlock()

	builtinAnnounceList = nil
	for _, s := range strings.Split(str, "\n") {
		builtinAnnounceList = append(builtinAnnounceList, []string{s})
	}
}

func SetClientVersion(str string) {
	torrent.ExtendedHandshakeClientVersion = str
}

//export CreateTorrentFileFromMetaInfo
func CreateTorrentFileFromMetaInfo() []byte {
	mu.Lock()
	defer mu.Unlock()

	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	err = metainfoBuild.Write(w)
	if err != nil {
		return nil
	}
	err = w.Flush()
	if err != nil {
		return nil
	}
	return b.Bytes()
}

// Create
//
// Create libtorrent object
//
//export Create
func Create() bool {
	mu.Lock()
	defer mu.Unlock()

	torrents = make(map[int]*torrent.Torrent)
	filestorage = make(map[metainfo.Hash]*fileStorage)
	torrentstorage = make(map[metainfo.Hash]*torrentStorage)
	queue = make(map[*torrent.Torrent]int64)
	active = make(map[*torrent.Torrent]int64)
	pause = nil
	index = 0
	tcpPort = ""
	udpPort = ""
	mappingAddr = nil

	clientConfig.DefaultStorage = &torrentOpener{}
	clientConfig.Seed = true
	clientConfig.ListenAddr = DefaultListenAddr

	client, err = torrent.NewClient(&clientConfig)
	if err != nil {
		return false
	}

	client.SetHalfOpenLimit(SocketsPerTorrent)

	clientAddr = client.ListenAddr().String()

	lpdStart()

	// when create client do 1 second discovery
	mu.Unlock()
	mappingPort(1 * time.Second)
	mu.Lock()

	err = client.Start()
	if err != nil {
		return false
	}

	go func() {
		mappingStart()
	}()

	return true
}

type BytesInfo struct {
	Downloaded int64
	Uploaded   int64
}

func Stats() *BytesInfo {
	stats := client.Stats()
	return &BytesInfo{stats.BytesRead, stats.BytesWritten}
}

// Get Torrent Count
//
//export Count
func Count() int {
	mu.Lock()
	defer mu.Unlock()

	return len(torrents)
}

//export ListenAddr
func ListenAddr() string {
	return client.ListenAddr().String()
}

//export CreateTorrentFromMetaInfo
func CreateTorrentFromMetaInfo() int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent

	if _, ok := filestorage[metainfoBuild.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := registerFileStorage(metainfoBuild.Info.Hash(), path.Dir(metainfoRoot))

	fs.Comment = metainfoBuild.Comment
	fs.Creator = metainfoBuild.CreatedBy
	fs.CreatedOn = (time.Duration(metainfoBuild.CreationDate) * time.Second).Nanoseconds()

	t, err = client.AddTorrent(metainfoBuild)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// AddMagnet
//
// Add magnet link to download list
//
//export AddMagnet
func AddMagnet(path string, magnet string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent
	var spec *torrent.TorrentSpec

	spec, err = torrent.TorrentSpecFromMagnetURI(magnet)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[spec.InfoHash]; ok {
		err = errors.New("Already exists")
		return -1
	}

	registerFileStorage(spec.InfoHash, path)

	t, _, err = client.AddTorrentSpec(spec)
	if err != nil {
		return -1
	}

	return register(t)
}

// AddTorrent
//
// Add torrent from local file or remote url.
//
//export AddTorrentFromURL
func AddTorrentFromURL(path string, url string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	var resp *http.Response
	resp, err = http.Get(url)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()

	mi, err = metainfo.Load(resp.Body)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := registerFileStorage(mi.Info.Hash(), path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = (time.Duration(mi.CreationDate) * time.Second).Nanoseconds()

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// AddTorrent
//
// Add torrent from local file and seed.
//
//export AddTorrent
func AddTorrent(file string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	mi, err = metainfo.LoadFromFile(file)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := registerFileStorage(mi.Info.Hash(), path.Dir(file))

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = (time.Duration(mi.CreationDate) * time.Second).Nanoseconds()

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

//export AddTorrentFromBytes
func AddTorrentFromBytes(path string, buf []byte) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	r := bytes.NewReader(buf)

	mi, err = metainfo.Load(r)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := registerFileStorage(mi.Info.Hash(), path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = (time.Duration(mi.CreationDate) * time.Second).Nanoseconds()

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// Get Torrent file from runtime torrent
//
//export GetTorrent
func GetTorrent(i int) []byte {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	err = t.Metainfo().Write(w)
	if err != nil {
		return nil
	}
	err = w.Flush()
	if err != nil {
		return nil
	}
	return buf.Bytes()
}

// Separate load / create torrent from network activity.
//
// Start announce torrent, seed/download
//
//export StartTorrent
func StartTorrent(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	if pause != nil {
		pause[t] = StatusDownloading
		return true
	}

	if _, ok := active[t]; ok {
		return true
	}

	if len(active) >= ActiveCount {
		// priority to start, seeding torrent will not start over downloading torrents
		return queueStart(t)
	}

	return startTorrent(t)
}

func startTorrent(t *torrent.Torrent) bool {
	fs := filestorage[t.InfoHash()]

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	active[t] = time.Now().UnixNano()

	lpdPeers(t)

	lpdForce()

	fs.ActivateDate = time.Now().UnixNano()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		mu.Lock()
		defer mu.Unlock()

		now := time.Now().UnixNano()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
	}()

	go func() {
		queueEngine(t)
	}()

	return true
}

// Download only metadata from magnet link and stop torrent
//
//export DownloadMetadata
func DownloadMetadata(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	if _, ok := active[t]; ok {
		return true
	}

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	fs.ActivateDate = time.Now().UnixNano()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		mu.Lock()
		defer mu.Unlock()

		now := time.Now().UnixNano()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
		t.Drop()
	}()

	return true
}

func MetaTorrent(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	return t.Info() != nil
}

// Stop torrent from announce, check, seed, download
//
//export StopTorrent
func StopTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	stopTorrent(t)

	if pause != nil {
		return
	}

	queueNext(nil)
}

func stopTorrent(t *torrent.Torrent) {
	if pause != nil {
		delete(pause, t)
	}

	fs := filestorage[t.InfoHash()]

	if _, ok := active[t]; ok {
		t.Drop()
		now := time.Now().UnixNano()
		if t.Seeding() {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
		} else {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		}
		fs.ActivateDate = now
	} else {
		t.Stop()
	}

	delete(active, t)
}

// CheckTorrent
//
// Check torrent file consisteny (pices hases) on a disk. Pause torrent if
// downloading, resume after.
//
//export CheckTorrent
func CheckTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()
	t := torrents[i]
	torrentstorageLock.Lock()
	ts := torrentstorage[t.InfoHash()]
	ts.completedPieces.Clear()
	torrentstorageLock.Unlock()

	fb := filePendingBitmap(t.Info())

	client.CheckTorrent(t, fb)
}

// Remote torrent for library
//
//export RemoveTorrent
func RemoveTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	stopTorrent(t)

	unregister(i)
}

//export Error
func Error() string {
	mu.Lock()
	defer mu.Unlock()

	if err != nil {
		return err.Error()
	}
	return ""
}

//export Close
func Close() {
	mu.Lock()
	defer mu.Unlock()

	mappingStop()

	lpdStop()

	clientAddr = ""

	if client != nil {
		client.Close()
		client = nil
	}
}

//
// protected
//

var clientConfig torrent.Config
var client *torrent.Client
var clientAddr string
var err error
var torrents map[int]*torrent.Torrent
var active map[*torrent.Torrent]int64
var index int
var mu sync.Mutex
var pause map[*torrent.Torrent]int32

func register(t *torrent.Torrent) int {
	index++
	for torrents[index] != nil {
		index++
	}
	torrents[index] = t

	t.SetMaxConns(SocketsPerTorrent)

	return index
}

func unregister(i int) {
	t := torrents[i]

	delete(filestorage, t.InfoHash())

	torrentstorageLock.Lock()
	delete(torrentstorage, t.InfoHash())
	torrentstorageLock.Unlock()

	delete(active, t)

	delete(queue, t)

	delete(pause, t)

	delete(torrents, i)
}
