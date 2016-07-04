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
	builtinAnnounceList = [][]string{
		{"udp://tracker.openbittorrent.com:80"},
		{"udp://tracker.kicks-ass.net:80/announce"},
	}
)

func SetDefaultAnnouncesList(str string) {
	builtinAnnounceList = nil
	for _, s := range strings.Split(str, "\n") {
		builtinAnnounceList = append(builtinAnnounceList, []string{s})
	}
}

//export CreateTorrentFile
func CreateTorrentFile(path string) []byte {
	mi := &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}
	mi.SetDefaults()
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

//export ListenAddr
func ListenAddr() string {
	return client.ListenAddr().String()
}

// Create
//
// Create libtorrent object
//
//export Create
func Create() bool {
	torrents = make(map[int]*torrent.Torrent)
	filestorage = make(map[metainfo.Hash]*fileStorage)
	queue = make(map[*torrent.Torrent]int64)
	index = 0

	clientConfig.DefaultStorage = &torrentOpener{}
	clientConfig.Seed = true
	clientConfig.ListenAddr = ":0"

	client, err = torrent.NewClient(&clientConfig)
	if err != nil {
		return false
	}

	clientAddr = client.ListenAddr().String()

	// when create client do 1 second discovery
	mapping(1 * time.Second)

	err = client.Start()
	if err != nil {
		return false
	}

	go func() {
		for {
			select {
			case <-client.Wait():
				return
			case <-time.After(refreshPort):
			}
			// in go routine do 5 seconds discovery
			mapping(5 * time.Second)
		}
	}()

	return true
}

type BytesInfo struct {
	Downloaded int64
	Uploaded   int64
}

func Stats() *BytesInfo {
	d, u := client.Stats()
	return &BytesInfo{d, u}
}

// Get Torrent Count
//
//export Count
func Count() int {
	return len(torrents)
}

//export CreateTorrent
func CreateTorrent(p string) int {
	var t *torrent.Torrent

	mi := &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}

	mi.SetDefaults()

	err = mi.Info.BuildFromFilePath(p)
	if err != nil {
		return -1
	}

	mi.Info.UpdateBytes()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := CreateFileStorage(t, path.Dir(p))

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	return register(t)
}

// AddMagnet
//
// Add magnet link to download list
//
//export AddMagnet
func AddMagnet(path string, magnet string) int {
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

	filestorage[spec.InfoHash] = CreateFileStorage(t, path)

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

	fs := CreateFileStorage(t, path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	return register(t)
}

// AddTorrent
//
// Add torrent from local file or remote url.
//
//export AddTorrentFromFile
func AddTorrentFromFile(path string, file string) int {
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

	fs := CreateFileStorage(t, path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	return register(t)
}

//export AddTorrentFromBytes
func AddTorrentFromBytes(path string, buf []byte) int {
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

	fs := CreateFileStorage(t, path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	return register(t)
}

// Get Torrent file from runtime torrent
//
//export GetTorrent
func GetTorrent(i int) []byte {
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

// SaveTorrent
//
// Every torrent application restarts it require to check files consistency. To
// avoid this, and save machine time we need to store torrents runtime states
// completed pieces and other information externaly.
//
// Save runtime torrent data to state file
//
//export SaveTorrent
func SaveTorrent(i int) []byte {
	t := torrents[i]

	var buf []byte

	buf, err = SaveTorrentState(t)
	if err != nil {
		return nil
	}

	return buf
}

// LoadTorrent
//
// Load runtime torrent data from saved state file
//
//export LoadTorrent
func LoadTorrent(path string, buf []byte) int {
	var t *torrent.Torrent

	t, err = LoadTorrentState(path, buf)
	if err != nil {
		return -1
	}

	return register(t)
}

// Separate load / create torrent from network activity.
//
// Start announce torrent, seed/download
//
//export StartTorrent
func StartTorrent(i int) bool {
	t := torrents[i]

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

	delete(queue, t)

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	fs.ActivateDate = time.Now().Unix()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		now := time.Now().Unix()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
	}()

	// queue engine
	go func() {
		timeout := QueueTimeout
		for {
			b1 := t.BytesCompleted()
			select {
			case <-time.After(timeout):
				timeout = QueueTimeout
				s := torrentStatus(t)
				if s == StatusSeeding {
					if !queueNext(t) {
						// we not been removed
						if len(queue) != 0 {
							// queue full, some one soon be available, check every minute
							timeout = 1 * time.Minute
						}
					}
				}
				if s == StatusDownloading {
					// check stole progress, and rotate downloading
					b2 := t.BytesCompleted()
					if b1 == b2 {
						if !queueNext(t) {
							// we not been removed
							if len(queue) != 0 {
								// queue full, some one soon be available, check every minute
								timeout = 1 * time.Minute
							}
						}
					}
				}
			case <-fs.Completed.LockedChan(&mu):
				timeout = QueueTimeout
				if !queueNext(t) {
					// we not been removed
					if len(queue) != 0 {
						// queue full, some one soon be available, check every minute
						timeout = 1 * time.Minute
					}
				}
			case <-t.Wait():
				return
			}
		}
	}()

	return true
}

// Download only metadata from magnet link and stop torrent
//
//export DownloadMetadata
func DownloadMetadata(i int) bool {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	if client.ActiveTorrent(t) {
		return true
	}

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	fs.ActivateDate = time.Now().Unix()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		now := time.Now().Unix()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
		stopTorrent(t)
	}()

	return true
}

func InfoTorrent(i int) bool {
	t := torrents[i]
	return t.Info() != nil
}

// Stop torrent from announce, check, seed, download
//
//export StopTorrent
func StopTorrent(i int) {
	t := torrents[i]
	stopTorrent(t)
	queueNext(nil)
}

func stopTorrent(t *torrent.Torrent) {
	fs := filestorage[t.InfoHash()]

	if client.ActiveTorrent(t) {
		t.Drop()

		now := time.Now().Unix()
		if t.Seeding() {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
		} else {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		}
	} else {
		t.Stop()
		delete(queue, t)
	}
}

// CheckTorrent
//
// Check torrent file consisteny (pices hases) on a disk. Pause torrent if
// downloading, resume after.
//
//export CheckTorrent
func CheckTorrent(i int) {
	t := torrents[i]
	client.CheckTorrent(t)
}

// Remote torrent for library
//
//export RemoveTorrent
func RemoveTorrent(i int) {
	t := torrents[i]
	if client.ActiveTorrent(t) {
		t.Drop()
	}
	unregister(i)
}

//export Error
func Error() string {
	if err != nil {
		return err.Error()
	}
	return ""
}

//export Close
func Close() {
	if client != nil {
		client.Close()
		client = nil
	}
}

//
// Torrent* methods
//

// Get Magnet from runtime torrent.
//
//export TorrentMagnet
func TorrentMagnet(i int) string {
	t := torrents[i]
	return t.Metainfo().Magnet().String()
}

func TorrentMetainfo(i int) *metainfo.MetaInfo {
	t := torrents[i]
	return t.Metainfo()
}

//export TorrentHash
func TorrentHash(i int) string {
	t := torrents[i]
	h := t.InfoHash()
	return h.HexString()
}

//export TorrentName
func TorrentName(i int) string {
	t := torrents[i]
	return t.Name()
}

//export TorrentActive
func TorrentActive(i int) bool {
	t := torrents[i]
	return client.ActiveTorrent(t)
}

const (
	StatusPaused      int32 = 0
	StatusDownloading int32 = 1
	StatusSeeding     int32 = 2
	StatusChecking    int32 = 3
	StatusQueued      int32 = 4
)

//export TorrentStatus
func TorrentStatus(i int) int32 {
	t := torrents[i]
	return torrentStatus(t)
}

func torrentStatus(t *torrent.Torrent) int32 {
	if client.ActiveTorrent(t) {
		if t.Info() != nil {
			// TODO t.Seeding() not working
			if pendingCompleted(t) {
				if t.Seeding() {
					return StatusSeeding
				}
			}
		}
		return StatusDownloading
	} else {
		if t.Check() {
			return StatusChecking
		}
		if _, ok := queue[t]; ok {
			return StatusQueued
		}
		return StatusPaused
	}
}

//export TorrentBytesLength
func TorrentBytesLength(i int) int64 {
	t := torrents[i]
	return t.Length()
}

//export TorrentBytesCompleted
func TorrentBytesCompleted(i int) int64 {
	t := torrents[i]
	return t.BytesCompleted()
}

// Get total bytes for pending pieces list
func TorrentPendingBytesLength(i int) int64 {
	t := torrents[i]
	fb := filePendingBitmap(t)
	return pendingBytesLength(t, fb)
}

// Get total bytes downloaded by pending pieces list
func TorrentPendingBytesCompleted(i int) int64 {
	t := torrents[i]
	fb := filePendingBitmap(t)
	return pendingBytesCompleted(t, fb)
}

type StatsInfo struct {
	Downloaded  int64
	Uploaded    int64
	Downloading int64
	Seeding     int64
}

func TorrentStats(i int) *StatsInfo {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	downloaded, uploaded := t.Stats()
	downloading := fs.DownloadingTime
	seeding := fs.SeedingTime

	if client.ActiveTorrent(t) {
		now := time.Now().Unix()
		if t.Seeding() {
			seeding = seeding + (now - fs.ActivateDate)
		} else {
			downloading = downloading + (now - fs.ActivateDate)
		}
	}

	return &StatsInfo{downloaded, uploaded, downloading, seeding}
}

//export TorrentCreator
func TorrentCreator(i int) string {
	t := torrents[i]
	return t.Metainfo().CreatedBy
}

//export TorrentCreateOn
func TorrentCreateOn(i int) int64 {
	t := torrents[i]
	return t.Metainfo().CreationDate
}

//export TorrentComment
func TorrentComment(i int) string {
	t := torrents[i]
	return t.Metainfo().Comment
}

func TorrentDateAdded(i int) int64 {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]
	return fs.AddedDate
}

func TorrentDateCompleted(i int) int64 {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]
	return fs.CompletedDate
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

func register(t *torrent.Torrent) int {
	mu.Lock()
	defer mu.Unlock()

	index++
	for torrents[index] != nil {
		index++
	}
	torrents[index] = t

	return index
}

func unregister(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	delete(filestorage, t.InfoHash())

	delete(torrents, i)
}
