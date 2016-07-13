package libtorrent

// #include <stdlib.h>
import "C"

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var (
	builtinAnnounceList = [][]string{
		{"udp://tracker.openbittorrent.com:80"},
		{"udp://tracker.kicks-ass.net:80/announce"},
	}

	SocketsPerTorrent = 40
)

func SetDefaultAnnouncesList(str string) {
	mu.Lock()
	defer mu.Unlock()

	builtinAnnounceList = nil
	for _, s := range strings.Split(str, "\n") {
		builtinAnnounceList = append(builtinAnnounceList, []string{s})
	}
}

// from transmissionbt makemeta.c
func bestPieceSize(totalSize int64) int64 {
	var KiB int64 = 1024
	var MiB int64 = 1048576
	var GiB int64 = 1073741824

	if totalSize >= (2 * GiB) {
		return 2 * MiB
	}
	if totalSize >= (1 * GiB) {
		return 1 * MiB
	}
	if totalSize >= (512 * MiB) {
		return 512 * KiB
	}
	if totalSize >= (350 * MiB) {
		return 256 * KiB
	}
	if totalSize >= (150 * MiB) {
		return 128 * KiB
	}
	if totalSize >= (50 * MiB) {
		return 64 * KiB
	}
	return 32 * KiB // less than 50 meg
}

func walkSize(root string) (size int64) {
	err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			// Directories are implicit in torrent files.
			return nil
		} else if path == root {
			// The root is a file.
			size += fi.Size()
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("error getting relative path: %s, %s", relPath, err)
		}
		return nil
	})
	if err != nil {
		size = 0
		return
	}
	return
}

func metainfoCreate(p string) *metainfo.MetaInfo {
	mi := &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}

	s := walkSize(p)

	private := false

	mi.Info.Private = &private
	mi.Comment = ""
	mi.CreatedBy = "libtorrent"
	mi.CreationDate = time.Now().Unix()
	mi.Info.PieceLength = bestPieceSize(s)
	return mi
}

//export CreateTorrentFile
func CreateTorrentFile(path string) []byte {
	mi := metainfoCreate(path)
	err = mi.Info.BuildFromFilePath(path)
	if err != nil {
		return nil
	}
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	err = mi.Write(w)
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
	pause = nil
	index = 0

	clientConfig.DefaultStorage = &torrentOpener{}
	clientConfig.Seed = true
	clientConfig.ListenAddr = ":0"

	client, err = torrent.NewClient(&clientConfig)
	if err != nil {
		return false
	}

	client.SetHalfOpenLimit(SocketsPerTorrent)

	clientAddr = client.ListenAddr().String()

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

//export CreateTorrent
func CreateTorrent(p string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent

	mi := metainfoCreate(p)

	err = mi.Info.BuildFromFilePath(p)
	if err != nil {
		return -1
	}

	mi.Info.UpdateBytes()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := registerFileStorage(mi.Info.Hash(), path.Dir(p))

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

	if client.ActiveTorrent(t) {
		return true
	}

	if client.ActiveCount() >= ActiveCount {
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

	if client.ActiveTorrent(t) {
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

	if pause != nil {
		delete(pause, t)
		return
	}

	stopTorrent(t)
	queueNext(nil)
}

func stopTorrent(t *torrent.Torrent) {
	fs := filestorage[t.InfoHash()]

	if client.ActiveTorrent(t) {
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
	if client.ActiveTorrent(t) {
		t.Drop()
	}
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

	delete(queue, t)

	delete(pause, t)

	delete(torrents, i)
}
