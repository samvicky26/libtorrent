package libtorrent

import (
	"time"

	"github.com/anacrolix/missinggo/bitmap"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

type File struct {
	Check          bool
	Path           string
	Length         int64
	BytesCompleted int64
}

func TorrentFilesCount(i int) int {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	fs.Files = nil

	info := t.Info()
	if info == nil {
		return 0
	}

	torrentstorageLock.Lock()
	defer torrentstorageLock.Unlock()
	ts := torrentstorage[t.InfoHash()]

	for i, v := range t.Files() {
		p := File{}
		p.Check = ts.checks[i]
		p.Path = v.Path()
		v.Offset()
		p.Length = v.Length()

		b := int(v.Offset() / info.PieceLength)
		e := int((v.Offset() + v.Length()) / info.PieceLength)

		// mid length
		var mid int64
		// count middle (b,e)
		for i := b + 1; i < e; i++ {
			p.BytesCompleted += t.PieceBytesCompleted(i)
			mid += t.PieceLength(i)
		}
		rest := v.Length() - mid
		// b and e should be counted as 100% of rest, each have 50% value
		value := t.PieceBytesCompleted(b)/t.PieceLength(b) + t.PieceBytesCompleted(e)/t.PieceLength(e)

		// v:2 - rest/1
		// v:1 - rest/2
		// v:0 - rest*0
		if value > 0 {
			p.BytesCompleted += rest / (2 / value)
		}

		fs.Files = append(fs.Files, p)
	}
	return len(fs.Files)
}

// return torrent files array
func TorrentFiles(i int, p int) *File {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	fs := filestorage[t.InfoHash()]
	return &fs.Files[p]
}

func TorrentFilesCheck(i int, p int, b bool) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	// update dynamic data
	ff := fs.Files[p]
	ff.Check = b

	torrentstorageLock.Lock()
	ts := torrentstorage[t.InfoHash()]
	ts.checks[p] = b
	torrentstorageLock.Unlock()

	fileUpdateCheck(t)
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

func fileUpdateCheck(t *torrent.Torrent) {
	fs := filestorage[t.InfoHash()]

	seeding := false
	downloading := false

	if client.ActiveTorrent(t) {
		pp := t.GetPendingPieces()
		if pendingBytesCompleted(t, &pp) >= pendingBytesLength(t, &pp) {
			seeding = true
		} else {
			downloading = true
		}
	}

	t.CancelPieces(0, t.NumPieces())
	t.UpdatePiecePriorities()

	fb := filePendingBitmap(t.Info())
	fb.IterTyped(func(piece int) (more bool) {
		t.DownloadPieces(piece, piece+1)
		return true
	})

	now := time.Now().UnixNano()

	if pendingBytesCompleted(t, fb) < pendingBytesLength(t, fb) { // now we downloading
		fs.CompletedDate = 0
		// did we seed before? update seed timer
		if seeding {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
			fs.ActivateDate = now
		}
	} else { // now we seeing
		// did we download before? update downloading timer then
		if downloading {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
			fs.ActivateDate = now
		}
	}

	t.UpdatePiecePriorities()
}

func filePendingBitmap(info *metainfo.InfoEx) *bitmap.Bitmap {
	torrentstorageLock.Lock()
	defer torrentstorageLock.Unlock()
	ts := torrentstorage[info.Hash()]
	return filePendingBitmapTs(info, ts.checks)
}

func filePendingBitmapTs(info *metainfo.InfoEx, checks []bool) *bitmap.Bitmap {
	var bm bitmap.Bitmap

	var offset int64
	for i, fi := range info.UpvertedFiles() {
		s := offset / info.PieceLength
		e := (offset+fi.Length)/info.PieceLength + 1
		if checks[i] {
			bm.AddRange(int(s), int(e))
		}
		offset += fi.Length
	}

	return &bm
}

func pendingCompleted(t *torrent.Torrent) bool {
	info := t.Info()
	if info == nil {
		return false
	}

	fb := filePendingBitmap(info)
	return pendingBytesCompleted(t, fb) >= pendingBytesLength(t, fb)
}

func pendingBytesLength(t *torrent.Torrent, fb *bitmap.Bitmap) int64 {
	var b int64

	fb.IterTyped(func(piece int) (again bool) {
		b += t.PieceLength(piece)
		return true
	})

	return b
}

func pendingBytesCompleted(t *torrent.Torrent, fb *bitmap.Bitmap) int64 {
	var b int64

	fb.IterTyped(func(piece int) (again bool) {
		b += t.PieceBytesCompleted(piece)
		return true
	})

	return b
}
