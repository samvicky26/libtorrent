package libtorrent

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

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
func SaveTorrentState(t *torrent.Torrent) ([]byte, error) {
	s := TorrentState{Version: 1}

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
		now := time.Now().Unix()
		if t.Seeding() {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
		} else {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		}
		fs.ActivateDate = now
	}

	s.Downloaded, s.Uploaded = t.Stats()

	s.Checks = fs.Checks

	s.DownloadingTime = fs.DownloadingTime
	s.SeedingTime = fs.SeedingTime

	s.AddedDate = fs.AddedDate
	s.CompletedDate = fs.CompletedDate

	s.Comment = fs.Comment
	s.Creator = fs.Creator
	s.CreatedOn = fs.CreatedOn

	if t.Info() != nil {
		bf := make([]bool, t.Info().NumPieces())
		fs.CompletedPieces.IterTyped(func(piece int) (again bool) {
			bf[piece] = true
			return true
		})
		s.Pieces = bf
	}

	return json.Marshal(s)
}

// Load torrent from saved state
func LoadTorrentState(path string, buf []byte) (t *torrent.Torrent, err error) {
	var s TorrentState
	err = json.Unmarshal(buf, &s)
	if err != nil {
		return
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

	fs := CreateFileStorage(path)

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

	for i, b := range s.Pieces {
		fs.CompletedPieces.Set(i, b)
	}
	fs.Checks = s.Checks

	filestorage[spec.InfoHash] = fs

	if spec.Info != nil {
		err = t.LoadInfoBytes(spec.Info.Bytes)
		if err != nil {
			return
		}
		t.UpdateAllPieceCompletions()
	}

	if t.Info() != nil {
		fs.fillInfo(t.Info())
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
