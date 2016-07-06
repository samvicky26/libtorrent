package libtorrent

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

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
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	var buf []byte

	buf, err = saveTorrentState(t)
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
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent

	t, err = loadTorrentState(path, buf)
	if err != nil {
		return -1
	}

	return register(t)
}

type TorrentState struct {
	Version int `json:"version"`

	// metainfo or these
	InfoHash *metainfo.Hash `json:"hash,omitempty"`
	Name     string         `json:"name,omitempty"`
	Trackers [][]string     `json:"trackers,omitempty"`

	MetaInfo *metainfo.MetaInfo `json:"metainfo,omitempty"`
	Pieces   []bool             `json:"pieces,omitempty"`

	Checks []bool `json:"checks,omitempty"`

	// Stats bytes
	Downloaded int64 `json:"downloaded,omitempty"`
	Uploaded   int64 `json:"uploaded,omitempty"`

	// dates
	AddedDate     int64 `json:"added_date,omitempty"`
	CompletedDate int64 `json:"completed_date,omitempty"`

	// time
	DownloadingTime int64 `json:"downloading_time,omitempty"`
	SeedingTime     int64 `json:"seeding_time,omitempty"`

	// .torrent
	Comment   string `json:"comment,omitempty"`
	Creator   string `json:"creator,omitempty"`
	CreatedOn int64  `json:"created_on,omitempty"`
}

// Save torrent to state file
func saveTorrentState(t *torrent.Torrent) ([]byte, error) {
	s := TorrentState{Version: 3}

	fs := filestorage[t.InfoHash()]

	if t.Info() != nil {
		s.MetaInfo = &metainfo.MetaInfo{
			CreationDate: fs.CreatedOn,
			Comment:      fs.Comment,
			CreatedBy:    fs.Creator,
			AnnounceList: t.AnnounceList(),
		}
		s.MetaInfo.Info = *t.Info()
	} else {
		hash := t.InfoHash()
		s.InfoHash = &hash
		s.Name = t.Name()
		s.Trackers = t.AnnounceList()
	}

	if client.ActiveTorrent(t) {
		now := time.Now().UnixNano()
		if t.Seeding() {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
		} else {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		}
		fs.ActivateDate = now
	}

	stats := t.Stats()
	s.Downloaded = stats.Downloaded
	s.Uploaded = stats.BytesSent

	s.DownloadingTime = fs.DownloadingTime
	s.SeedingTime = fs.SeedingTime

	s.AddedDate = fs.AddedDate
	s.CompletedDate = fs.CompletedDate

	s.Comment = fs.Comment
	s.Creator = fs.Creator
	s.CreatedOn = fs.CreatedOn

	if t.Info() != nil {
		torrentstorageLock.Lock()
		ts := torrentstorage[t.InfoHash()]

		bf := make([]bool, t.Info().NumPieces())
		ts.completedPieces.IterTyped(func(piece int) (again bool) {
			bf[piece] = true
			return true
		})
		s.Pieces = bf

		s.Checks = make([]bool, len(ts.checks))
		copy(s.Checks, ts.checks)

		torrentstorageLock.Unlock()
	}

	return json.Marshal(s)
}

// Load torrent from saved state
func loadTorrentState(path string, buf []byte) (t *torrent.Torrent, err error) {
	var s TorrentState
	err = json.Unmarshal(buf, &s)
	if err != nil {
		return
	}

	switch s.Version {
	case 1:
		version1to2(&s)
		version2to3(&s)
	case 2:
		version2to3(&s)
	}

	var spec *torrent.TorrentSpec

	if s.MetaInfo == nil {
		spec = &torrent.TorrentSpec{
			Trackers:    s.Trackers,
			DisplayName: s.Name,
			InfoHash:    *s.InfoHash,
		}
	} else {
		spec = torrent.TorrentSpecFromMetaInfo(s.MetaInfo)
	}

	fs := registerFileStorage(spec.InfoHash, path)

	var n bool
	t, n = client.AddTorrentInfoHash(spec.InfoHash)
	if !n {
		err = errors.New("already exists")
		t = nil
		return
	}
	if spec.DisplayName != "" {
		t.SetDisplayName(spec.DisplayName)
	}

	torrentstorageLock.Lock()
	ts := torrentstorage[spec.InfoHash]
	for i, b := range s.Pieces {
		ts.completedPieces.Set(i, b)
	}
	ts.checks = s.Checks
	torrentstorageLock.Unlock()

	if spec.Info != nil {
		err = t.LoadInfoBytes(spec.Info.Bytes)
		if err != nil {
			return
		}
		t.UpdateAllPieceCompletions()
	}

	if t.Info() != nil {
		fileUpdateCheck(t)
	}

	if spec.ChunkSize != 0 {
		t.SetChunkSize(pp.Integer(spec.ChunkSize))
	}
	t.AddTrackers(spec.Trackers)

	t.SetStats(s.Downloaded, s.Uploaded)

	fs.DownloadingTime = s.DownloadingTime
	fs.SeedingTime = s.SeedingTime

	fs.AddedDate = s.AddedDate
	fs.CompletedDate = s.CompletedDate

	fs.Comment = s.Comment
	fs.Creator = s.Creator
	fs.CreatedOn = s.CreatedOn

	return
}

func version1to2(s *TorrentState) {
	// no changes between format 1..2
}

func version2to3(s *TorrentState) {
	s.AddedDate = (time.Duration(s.AddedDate) * time.Second).Nanoseconds()
	s.CompletedDate = (time.Duration(s.CompletedDate) * time.Second).Nanoseconds()
	s.DownloadingTime = (time.Duration(s.DownloadingTime) * time.Second).Nanoseconds()
	s.SeedingTime = (time.Duration(s.SeedingTime) * time.Second).Nanoseconds()
	s.CreatedOn = (time.Duration(s.CreatedOn) * time.Second).Nanoseconds()
}
