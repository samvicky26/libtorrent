package libtorrent

// #include <stdlib.h>
import "C"

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/syncthing/syncthing/lib/nat"
	"github.com/syncthing/syncthing/lib/upnp"
	"math/rand"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
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

type torrentOpener struct {
}

func (m *torrentOpener) OpenTorrent(info *metainfo.InfoEx) (storage.Torrent, error) {
	var p string

	if s, ok := filestorage[info.Hash()]; !ok {
		p = clientConfig.DataDir
	} else {
		p = s.Path
	}

	return storage.NewFile(p).OpenTorrent(info)
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
	index = 0

	clientConfig.DefaultStorage = &torrentOpener{}
	clientConfig.Seed = true
	clientConfig.ListenAddr = ":0"

	client, err = torrent.NewClient(&clientConfig)
	if err != nil {
		return false
	}

	clientAddr = client.ListenAddr().String()

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

	filestorage[mi.Info.Hash()] = &fileStorage{Path: path.Dir(p)}

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

	filestorage[spec.InfoHash] = &fileStorage{Path: path}

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
//export AddTorrent
func AddTorrent(path string, file string) int {
	var t *torrent.Torrent
	var metaInfo *metainfo.MetaInfo

	if strings.HasPrefix(file, "http") {
		var resp *http.Response
		resp, err = http.Get(file)
		if err != nil {
			return -1
		}
		defer resp.Body.Close()

		metaInfo, err = metainfo.Load(resp.Body)
		if err != nil {
			return -1
		}
	} else {
		metaInfo, err = metainfo.LoadFromFile(file)
		if err != nil {
			return -1
		}
	}

	if _, ok := filestorage[metaInfo.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	filestorage[metaInfo.Info.Hash()] = &fileStorage{Path: path}

	t, err = client.AddTorrent(metaInfo)
	if err != nil {
		return -1
	}

	return register(t)
}

func AddTorrentFromBytes(path string, buf []byte) int {
	var t *torrent.Torrent
	var metaInfo *metainfo.MetaInfo

	r := bytes.NewReader(buf)

	metaInfo, err = metainfo.Load(r)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[metaInfo.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	filestorage[metaInfo.Info.Hash()] = &fileStorage{Path: path}

	t, err = client.AddTorrent(metaInfo)
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

	buf, err = client.SaveTorrent(t)
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

	// will be read immidialtly within client.LoadTorrent call
	clientConfig.DataDir = path

	t, err = client.LoadTorrent(buf)
	if err != nil {
		return -1
	}

	// prevent addind magnets/torrents with same hash
	filestorage[t.InfoHash()] = &fileStorage{Path: path}

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

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}
		t.FileUpdateCheck()
	}()

	return true
}

// Download only metadata from magnet link and stop torrent
//
//export DownloadMetadata
func DownloadMetadata(i int) bool {
	t := torrents[i]

	if client.ActiveTorrent(t) {
		return true
	}

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}
		t.FileUpdateCheck()
		StopTorrent(i)
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
	if client.ActiveTorrent(t) {
		t.Drop()
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

	if client.ActiveTorrent(t) {
		if t.Info() != nil {
			// TODO t.Seeding() not working
			if t.PendingBytesCompleted() == t.PendingBytesLength() {
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
	return t.PendingBytesLength()
}

// Get total bytes downloaded by pending pieces list
func TorrentPendingBytesCompleted(i int) int64 {
	t := torrents[i]
	return t.PendingBytesCompleted()
}

type StatsInfo struct {
	Downloaded  int64
	Uploaded    int64
	Downloading int64
	Seeding     int64
}

func TorrentStats(i int) *StatsInfo {
	t := torrents[i]
	d, u, dd, ss := t.Stats()
	return &StatsInfo{d, u, dd, ss}
}

type File struct {
	Check          bool
	Path           string
	Length         int64
	BytesCompleted int64
}

func TorrentFilesCount(i int) int {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	f.Files = nil

	ff := t.Files()

	for i, v := range ff {
		p := File{}
		p.Check = t.FileCheck(i)
		p.Path = v.Path()
		p.Length = v.Length()
		p.BytesCompleted = 0
		f.Files = append(f.Files, p)
	}
	return len(f.Files)
}

// return torrent files array
func TorrentFiles(i int, p int) *File {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return &f.Files[p]
}

func TorrentFilesCheck(i int, p int, b bool) {
	t := torrents[i]

	f := filestorage[t.InfoHash()]

	ff := f.Files[p]
	ff.Check = b

	t.FileSetCheck(p, b)
}

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
)

func TorrentPeersCount(i int) int {
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
		}
		f.Peers = append(f.Peers, Peer{v.Id, v.Name, v.Addr, p, v.SupportsEncryption, v.PiecesCompleted, v.Downloaded, v.Uploaded})
	}

	return len(f.Peers) // t.PeersCount()
}

func TorrentPeers(i int, p int) *Peer {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return &f.Peers[p]
}

func TorrentPieceLength(i int) int64 {
	t := torrents[i]
	return t.Info().PieceLength
}

func TorrentPiecesCount(i int) int {
	t := torrents[i]
	return t.NumPieces()
}

const (
	PieceEmpty    int32 = 0
	PieceComplete int32 = 1
	PieceChecking int32 = 2
	PiecePartial  int32 = 3 // when booth empty and completed
	PieceWriting  int32 = 4 // when have partial pieces
	PieceUnpended int32 = 5 // empy pieces can be unpended
)

func TorrentPiecesCompactCount(i int, size int) int {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	f.Pieces = nil

	pended := false
	empty := false
	complete := false
	partial := false
	checking := false
	count := 0

	pos := 0
	for _, v := range t.PieceStateRuns() {
		for i := 0; i < v.Length; i++ {
			if v.Complete {
				complete = true
			} else {
				empty = true
				// at least one pice pendend then mark all (size) pendent
				if t.PiecePended(pos) {
					pended = true
				}
			}
			if v.Partial {
				partial = true
			}
			if v.Checking {
				checking = true
			}
			count = count + 1

			if count >= size {
				state := PieceEmpty
				if checking {
					state = PieceChecking
				} else if partial {
					state = PieceWriting
				} else if empty && complete {
					state = PiecePartial
				} else if complete {
					state = PieceComplete
				} else if !pended {
					state = PieceUnpended
				} else {
					state = PieceEmpty
				}
				f.Pieces = append(f.Pieces, state)

				pended = false
				empty = false
				complete = false
				partial = false
				checking = false
				count = 0
			}
			pos++
		}
	}
	if count > 0 {
		state := PieceEmpty
		if checking {
			state = PieceChecking
		} else if partial {
			state = PieceWriting
		} else if empty && complete {
			state = PiecePartial
		} else if complete {
			state = PieceComplete
		} else if !pended {
			state = PieceUnpended
		} else {
			state = PieceEmpty
		}
		f.Pieces = append(f.Pieces, state)
	}
	return len(f.Pieces)
}

func TorrentPiecesCompact(i int, p int) int32 {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return f.Pieces[p]
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
	a, _ := t.Dates()
	return a
}

func TorrentDateCompleted(i int) int64 {
	t := torrents[i]
	_, c := t.Dates()
	return c
}

// TorrentFileRename
//
// To implement this we need to keep two Metainfo one for network operations,
// and second for local file storage.
//
//export TorrentFileRename
func TorrentFileRename(i int, f int, n string) {
	panic("not implement")
}

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
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	f.Trackers = nil
	for _, v := range t.Trackers() {
		f.Trackers = append(f.Trackers, Tracker{v.Url, v.Err, v.LastAnnounce, v.NextAnnounce, v.Peers, 0, 0, 0, 0})
	}
	return len(f.Trackers)
}

func TorrentTrackers(i int, p int) *Tracker {
	t := torrents[i]
	f := filestorage[t.InfoHash()]
	return &f.Trackers[p]
}

func TorrentTrackerRemove(i int, url string) {
	t := torrents[i]
	t.RemoveTracker(url)
}

func TorrentTrackerAdd(i int, addr string) {
	t := torrents[i]
	t.AddTrackers([][]string{[]string{addr}})
}

//
// protected
//

type fileStorage struct {
	Path     string
	Trackers []Tracker
	Pieces   []int32
	Files    []File
	Checks   []bool
	Peers    []Peer
}

var clientConfig torrent.Config
var client *torrent.Client
var clientAddr string
var err error
var torrents map[int]*torrent.Torrent
var filestorage map[metainfo.Hash]*fileStorage
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

var tcpPort string
var udpPort string
var refreshPort = 1 * time.Minute

type PortInfo struct {
	TCP string
	UDP string
}

func PortMapping() *PortInfo {
	return &PortInfo{tcpPort, udpPort}
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

	ext, err := net.LookupPort("tcp", extPort)
	if err != nil || ext == 0 {
		ext = port
	}

	// try specific port
	p, err := d.AddPortMapping(proto, port, ext, n, 2*refreshPort)
	if err == nil {
		return p, nil
	}

	// try random port
	p, err = d.AddPortMapping(proto, port, 0, n, 2*refreshPort)
	if err == nil {
		return p, nil
	}

	// try rand port
	for i := 0; i < 10; i++ {
		// Then try up to ten random ports.
		extPort := 1024 + rand.Intn(65535-1024)

		p, err = d.AddPortMapping(proto, port, extPort, n, 2*refreshPort)
		if err == nil {
			return p, nil
		}
	}

	return 0, err
}

func mapping(timeout time.Duration) error {
	_, pp, err := net.SplitHostPort(clientAddr)
	if err != nil {
		return err
	}

	localport, err := net.LookupPort("tcp", pp)
	if err != nil {
		return err
	}

	dd := upnp.Discover(timeout, timeout)

	tcp := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		p, err := getPort(d, nat.TCP, localport, tcpPort)
		if err != nil {
			return err
		}
		mu.Lock()
		tcpPort = strconv.Itoa(p)
		mu.Unlock()
		client.SetListenTCPAddr(net.JoinHostPort(ext.String(), tcpPort))
		return nil
	}

	udp := func(d nat.Device) error {
		ext, err := d.GetExternalIPAddress()
		if err != nil {
			return err
		}
		p, err := getPort(d, nat.UDP, localport, udpPort)
		if err != nil {
			return err
		}
		mu.Lock()
		udpPort = strconv.Itoa(p)
		mu.Unlock()
		client.SetListenUDPAddr(net.JoinHostPort(ext.String(), udpPort))
		return nil
	}

	for _, d := range dd {
		if tcp != nil {
			if err := tcp(d); err == nil {
				tcp = nil
			}
		}
		if udp != nil {
			if err := udp(d); err == nil {
				udp = nil
			}
		}
	}

	if tcp != nil {
		mu.Lock()
		tcpPort = ""
		mu.Unlock()
		client.SetListenTCPAddr(clientAddr)
	}

	if udp != nil {
		mu.Lock()
		udpPort = ""
		mu.Unlock()
		client.SetListenUDPAddr(clientAddr)
	}

	return nil
}
