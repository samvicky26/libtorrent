package libtorrent

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/missinggo/slices"
	"github.com/anacrolix/torrent/metainfo"
)

var (
	builtinAnnounceList = [][]string{
		{"udp://tracker.openbittorrent.com:80"},
		{"udp://tracker.kicks-ass.net:80/announce"},
	}
)

type metaInfoBuilder struct {
	root string
	info *metainfo.MetaInfo
	pr   *io.PipeReader
	last int
}

var metainfoBuild *metaInfoBuilder

// transmissionbt/makemeta.c
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

//export CreateMetaInfo
func CreateMetaInfo(root string) int {
	mu.Lock()
	defer mu.Unlock()

	metainfoBuild = &metaInfoBuilder{}

	metainfoBuild.info = &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}
	metainfoBuild.root = root

	var size int64

	metainfoBuild.info.Info.Name = filepath.Base(metainfoBuild.root)
	metainfoBuild.info.Info.Files = nil
	err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			// Directories are implicit in torrent files.
			return nil
		} else if path == root {
			// The root is a file.
			metainfoBuild.info.Info.Length = fi.Size()
			size += fi.Size()
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("error getting relative path: %s", err)
		}
		metainfoBuild.info.Info.Files = append(metainfoBuild.info.Info.Files, metainfo.FileInfo{
			Path:   strings.Split(relPath, string(filepath.Separator)),
			Length: fi.Size(),
		})
		size += fi.Size()
		return nil
	})
	if err != nil {
		return -1
	}
	slices.Sort(metainfoBuild.info.Info.Files, func(l, r metainfo.FileInfo) bool {
		return strings.Join(l.Path, "/") < strings.Join(r.Path, "/")
	})

	private := false

	metainfoBuild.info.Info.Private = &private
	metainfoBuild.info.Comment = ""
	metainfoBuild.info.CreatedBy = "libtorrent"
	metainfoBuild.info.CreationDate = time.Now().Unix()
	metainfoBuild.info.Info.PieceLength = bestPieceSize(size)

	open := func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		return os.Open(filepath.Join(root, strings.Join(fi.Path, string(filepath.Separator))))
	}

	var pw *io.PipeWriter
	metainfoBuild.pr, pw = io.Pipe()
	go func() {
		var err error
		for _, fi := range metainfoBuild.info.Info.UpvertedFiles() {
			var r io.ReadCloser
			r, err = open(fi)
			if err != nil {
				err = fmt.Errorf("error opening %v: %s", fi, err)
				break
			}
			var wn int64
			wn, err = io.CopyN(pw, r, fi.Length)
			r.Close()
			if wn != fi.Length || err != nil {
				err = fmt.Errorf("error hashing %v: %s", fi, err)
				break
			}
		}
		pw.CloseWithError(err)
	}()

	s := size / metainfoBuild.info.Info.PieceLength
	r := size % metainfoBuild.info.Info.PieceLength
	if r > 0 { // remaining peace
		s++
	}
	metainfoBuild.last = int(s) - 1
	return int(s)
}

//export HashMetaInfo
func HashMetaInfo(piece int) bool {
	mu.Lock()
	defer mu.Unlock()

	var wn int64

	if metainfoBuild.pr == nil {
		err = errors.New("pr nil")
		return false
	}

	hasher := sha1.New()
	wn, err = io.CopyN(hasher, metainfoBuild.pr, metainfoBuild.info.Info.PieceLength)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		metainfoBuild.pr.Close()
		metainfoBuild.pr = nil
		return false
	}
	if wn == 0 {
		metainfoBuild.pr.Close()
		metainfoBuild.pr = nil
		metainfoBuild.info.Info.UpdateBytes()
		return true
	}
	metainfoBuild.info.Info.Pieces = hasher.Sum(metainfoBuild.info.Info.Pieces)
	if wn < metainfoBuild.info.Info.PieceLength {
		metainfoBuild.pr.Close()
		metainfoBuild.pr = nil
		metainfoBuild.info.Info.UpdateBytes()
		return true
	}
	if piece == metainfoBuild.last {
		metainfoBuild.pr.Close()
		metainfoBuild.pr = nil
		metainfoBuild.info.Info.UpdateBytes()
		return true
	}
	return true
}

//export CloseMetaInfo
func CloseMetaInfo() {
	mu.Lock()
	defer mu.Unlock()

	if metainfoBuild.pr != nil {
		metainfoBuild.pr.Close()
		metainfoBuild.pr = nil
	}
	metainfoBuild = nil
}
