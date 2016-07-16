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

var metainfoRoot string
var metainfoBuild *metainfo.MetaInfo
var metainfoPr *io.PipeReader

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

	metainfoBuild = &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}

	metainfoRoot = root

	var size int64

	metainfoBuild.Info.Name = filepath.Base(metainfoRoot)
	metainfoBuild.Info.Files = nil
	err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			// Directories are implicit in torrent files.
			return nil
		} else if path == root {
			// The root is a file.
			metainfoBuild.Info.Length = fi.Size()
			size += fi.Size()
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("error getting relative path: %s", err)
		}
		metainfoBuild.Info.Files = append(metainfoBuild.Info.Files, metainfo.FileInfo{
			Path:   strings.Split(relPath, string(filepath.Separator)),
			Length: fi.Size(),
		})
		size += fi.Size()
		return nil
	})
	if err != nil {
		return -1
	}
	slices.Sort(metainfoBuild.Info.Files, func(l, r metainfo.FileInfo) bool {
		return strings.Join(l.Path, "/") < strings.Join(r.Path, "/")
	})

	private := false

	metainfoBuild.Info.Private = &private
	metainfoBuild.Comment = ""
	metainfoBuild.CreatedBy = "libtorrent"
	metainfoBuild.CreationDate = time.Now().Unix()
	metainfoBuild.Info.PieceLength = bestPieceSize(size)

	open := func(fi metainfo.FileInfo) (io.ReadCloser, error) {
		return os.Open(filepath.Join(root, strings.Join(fi.Path, string(filepath.Separator))))
	}

	var pw *io.PipeWriter
	metainfoPr, pw = io.Pipe()
	go func() {
		var err error
		for _, fi := range metainfoBuild.Info.UpvertedFiles() {
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

	return int(size/metainfoBuild.Info.PieceLength) + 1
}

func HashMetaInfo(piece int) bool {
	mu.Lock()
	defer mu.Unlock()

	var wn int64

	if metainfoPr == nil {
		err = errors.New("pr nil")
		return false
	}

	hasher := sha1.New()
	wn, err = io.CopyN(hasher, metainfoPr, metainfoBuild.Info.PieceLength)
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		metainfoPr.Close()
		metainfoPr = nil
		return false
	}
	if wn == 0 {
		metainfoPr.Close()
		metainfoPr = nil
		metainfoBuild.Info.UpdateBytes()
		return true
	}
	metainfoBuild.Info.Pieces = hasher.Sum(metainfoBuild.Info.Pieces)
	if wn < metainfoBuild.Info.PieceLength {
		metainfoPr.Close()
		metainfoPr = nil
		metainfoBuild.Info.UpdateBytes()
	}
	return true
}

func CloseMetaInfo() {
	mu.Lock()
	defer mu.Unlock()

	if metainfoPr != nil {
		metainfoPr.Close()
		metainfoPr = nil
	}
	metainfoRoot = ""
	metainfoBuild = nil
}
