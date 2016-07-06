package libtorrent

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/anacrolix/missinggo/bitmap"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

var filestorage map[metainfo.Hash]*fileStorage

type fileStorage struct {
	// dynamic data
	Trackers []Tracker
	Pieces   []int32
	Files    []File
	Peers    []Peer

	// date in seconds when torrent been StartTorrent, we measure this value to get downloadingTime && seedingTime
	ActivateDate int64

	// elapsed in seconds
	DownloadingTime int64
	SeedingTime     int64

	// dates in seconds
	AddedDate     int64
	CompletedDate int64

	// .torrent info
	Creator   string
	CreatedOn int64
	Comment   string
}

func registerFileStorage(info metainfo.Hash, path string) *fileStorage {
	ts := &torrentStorage{path: path}

	torrentstorageLock.Lock()
	torrentstorage[info] = ts
	torrentstorageLock.Unlock()

	fs := &fileStorage{
		AddedDate: time.Now().UnixNano(),

		Comment:   "dynamic metainfo from client",
		Creator:   "go.libtorrent",
		CreatedOn: time.Now().UnixNano(),
	}

	filestorage[info] = fs

	return fs
}

type torrentStorage struct {
	info            *metainfo.InfoEx
	path            string
	checks          []bool
	completedPieces bitmap.Bitmap
	// fired when torrent downloaded, used for queue engine to roll downloads
	completed bool
	next      missinggo.Event
}

func (m *torrentStorage) Checks() []bool {
	checks := make([]bool, len(m.checks))
	copy(checks, m.checks)
	return checks
}

func (m *torrentStorage) Pieces() []bool {
	bf := make([]bool, m.info.NumPieces())
	m.completedPieces.IterTyped(func(piece int) (again bool) {
		bf[piece] = true
		return true
	})
	return bf
}

func (m *torrentStorage) Completed() {
	fb := filePendingBitmapTs(m.info, m.checks)

	m.completed = true

	// run thougth all pieces and check they all present in m.completedPieces
	fb.IterTyped(func(piece int) (again bool) {
		if !m.completedPieces.Contains(piece) {
			m.completed = false
			return false
		}
		return true
	})
}

var torrentstorage map[metainfo.Hash]*torrentStorage
var torrentstorageLock sync.Mutex

type torrentOpener struct {
}

type fileTorrentStorage struct {
	ts *torrentStorage
}

func (m *torrentOpener) OpenTorrent(info *metainfo.InfoEx) (storage.Torrent, error) {
	torrentstorageLock.Lock()
	defer torrentstorageLock.Unlock()

	ts := torrentstorage[info.Hash()]
	ts.info = info

	// if we come here from LoadTorrent checks is set. otherwise we come here after torrent open, fill defaults
	if ts.checks == nil {
		ts.checks = make([]bool, len(info.UpvertedFiles()))
		for i, _ := range ts.checks {
			ts.checks[i] = true
		}
	}

	// update comleted after torrent open
	ts.Completed()

	return fileTorrentStorage{ts}, nil
}

type fileStorageTorrent struct {
	info *metainfo.InfoEx
	ts   *torrentStorage
}

func (m fileTorrentStorage) Piece(p metainfo.Piece) storage.Piece {
	// Create a view onto the file-based torrent storage.
	_io := &fileStorageTorrent{
		p.Info,
		m.ts,
	}
	// Return the appropriate segments of this.
	return &fileStoragePiece{
		m.ts,
		p,
		missinggo.NewSectionWriter(_io, p.Offset(), p.Length()),
		io.NewSectionReader(_io, p.Offset(), p.Length()),
	}
}

func (fs fileTorrentStorage) Close() error {
	return nil
}

type fileStoragePiece struct {
	*torrentStorage
	p metainfo.Piece
	io.WriterAt
	io.ReaderAt
}

func (m *fileStoragePiece) GetIsComplete() bool {
	torrentstorageLock.Lock()
	defer torrentstorageLock.Unlock()
	return m.completedPieces.Get(m.p.Index())
}

func (m *fileStoragePiece) MarkComplete() error {
	torrentstorageLock.Lock()
	defer torrentstorageLock.Unlock()
	m.completedPieces.Set(m.p.Index(), true)

	if m.completed {
		return nil
	}

	m.Completed()

	if m.completed {
		m.next.Set()
	}

	return nil
}

// Returns EOF on short or missing file.
func (fst *fileStorageTorrent) readFileAt(fi metainfo.FileInfo, b []byte, off int64) (n int, err error) {
	f, err := os.Open(fst.fileInfoName(fi))
	if os.IsNotExist(err) {
		// File missing is treated the same as a short file.
		err = io.EOF
		return
	}
	if err != nil {
		return
	}
	defer f.Close()
	// Limit the read to within the expected bounds of this file.
	if int64(len(b)) > fi.Length-off {
		b = b[:fi.Length-off]
	}
	for off < fi.Length && len(b) != 0 {
		n1, err1 := f.ReadAt(b, off)
		b = b[n1:]
		n += n1
		off += int64(n1)
		if n1 == 0 {
			err = err1
			break
		}
	}
	return
}

// Only returns EOF at the end of the torrent. Premature EOF is ErrUnexpectedEOF.
func (fst *fileStorageTorrent) ReadAt(b []byte, off int64) (n int, err error) {
	for _, fi := range fst.info.UpvertedFiles() {
		for off < fi.Length {
			n1, err1 := fst.readFileAt(fi, b, off)
			n += n1
			off += int64(n1)
			b = b[n1:]
			if len(b) == 0 {
				// Got what we need.
				return
			}
			if n1 != 0 {
				// Made progress.
				continue
			}
			err = err1
			if err == io.EOF {
				// Lies.
				err = io.ErrUnexpectedEOF
			}
			return
		}
		off -= fi.Length
	}
	err = io.EOF
	return
}

func (fst *fileStorageTorrent) WriteAt(p []byte, off int64) (n int, err error) {
	for _, fi := range fst.info.UpvertedFiles() {
		if off >= fi.Length {
			off -= fi.Length
			continue
		}
		n1 := len(p)
		if int64(n1) > fi.Length-off {
			n1 = int(fi.Length - off)
		}
		name := fst.fileInfoName(fi)
		os.MkdirAll(filepath.Dir(name), 0770)
		var f *os.File
		f, err = os.OpenFile(name, os.O_WRONLY|os.O_CREATE, 0660)
		if err != nil {
			return
		}
		n1, err = f.WriteAt(p[:n1], off)
		f.Close()
		if err != nil {
			return
		}
		n += n1
		off = 0
		p = p[n1:]
		if len(p) == 0 {
			break
		}
	}
	return
}

func (fst *fileStorageTorrent) fileInfoName(fi metainfo.FileInfo) string {
	return filepath.Join(append([]string{fst.ts.path, fst.info.Name}, fi.Path...)...)
}
