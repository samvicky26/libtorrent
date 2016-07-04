package libtorrent

import (
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

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

type StatsTorrent struct {
	Downloaded  int64
	Uploaded    int64
	Downloading int64
	Seeding     int64
}

func TorrentStats(i int) *StatsTorrent {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	stats := t.Stats()
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

	return &StatsTorrent{stats.Downloaded, stats.Uploaded, downloading, seeding}
}

type InfoTorrent struct {
	Creator       string
	CreateOn      int64
	Comment       string
	DateAdded     int64
	DateCompleted int64
}

func TorrentInfo(i int) *InfoTorrent {
	t := torrents[i]
	fs := filestorage[t.InfoHash()]
	return &InfoTorrent{fs.Creator, fs.CreatedOn, fs.Comment, fs.AddedDate, fs.CompletedDate}
}
