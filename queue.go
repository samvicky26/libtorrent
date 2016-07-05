package libtorrent

import (
	"sort"
	"time"

	"github.com/anacrolix/torrent"
)

var ActiveCount = 3
var QueueTimeout = (30 * time.Minute).Nanoseconds()

var queue map[*torrent.Torrent]int64

// IntSlice attaches the methods of Interface to []int, sorting in increasing order.
type Int64Slice []int64

func (p Int64Slice) Len() int           { return len(p) }
func (p Int64Slice) Less(i, j int) bool { return p[i] < p[j] }
func (p Int64Slice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// Sort is a convenience method.
func (p Int64Slice) Sort() { sort.Sort(p) }

// priority start torrent. downloading torrent goes first, seeding second.
func queueStart(t *torrent.Torrent) bool {
	if client.ActiveCount() < ActiveCount {
		return startTorrent(t)
	}

	// build active torrent array with activate time
	q := make(map[int64]*torrent.Torrent)
	var l []int64

	for _, m := range torrents {
		if client.ActiveTorrent(m) {
			fs := filestorage[m.InfoHash()]
			v := fs.ActivateDate
			q[v] = m
			l = append(l, v)
		}
	}

	// older torrent will be removed first
	sort.Sort(Int64Slice(l))

	now := time.Now().UnixNano()

	// t is downloading?
	if t.Info() == nil || !pendingCompleted(t) {
		// try to find seeding torrent
		for _, v := range l {
			m := q[v]
			// m is seeding?
			if m.Info() != nil && pendingCompleted(m) {
				stopTorrent(m)
				queue[m] = now
				return startTorrent(t)
			}
		}
		// ok all torrents are downloading, remove first downloading torrent
		for _, v := range l {
			m := q[v]
			stopTorrent(m)
			queue[m] = now
			return startTorrent(t)
		}
	} else {
		// try to find first seeding torrent
		for _, v := range l {
			m := q[v]
			if m.Info() != nil && pendingCompleted(m) {
				stopTorrent(m)
				queue[m] = now
				return startTorrent(t)
			}
		}
	}

	// seems like we are seeding, and have no slots, just queue
	queue[t] = now
	return true
}

// 30 min seeding, download complete, 30 min stole torrent.
func queueNext(t *torrent.Torrent) bool {
	now := time.Now().UnixNano()

	q := make(map[int64]*torrent.Torrent)
	var l []int64

	for m, v := range queue {
		if client.ActiveCount() < ActiveCount { // queue all
			q[v] = m
			l = append(l, v)
		} else if v+QueueTimeout <= now { // keep torrent resting for 30 mins
			// duplicates are lost
			q[v] = m
			l = append(l, v)
		}
	}

	// older first
	sort.Sort(Int64Slice(l))

	// check for downloading queue torrents
	for _, v := range l {
		m := q[v]

		if m.Info() == nil || !pendingCompleted(m) {
			if startTorrent(m) {
				if t != nil {
					stopTorrent(t)
					queue[t] = now
				}
				return true
			}
			// unable to start torrent, here no place to report an error. keep looping.
		}
	}

	// check for seeding queue
	for _, v := range l {
		m := q[v]
		if m.Info() != nil && pendingCompleted(m) {
			if startTorrent(m) {
				if t != nil {
					stopTorrent(t)
					queue[t] = now
				}
				return true
			}
			// unable to start torrent, here no place to report an error. keep looping.
		}
	}

	if t != nil {
		// is 't' seeding torrent? if here any downloading, queue it, regardless on timeout
		if t.Info() != nil && pendingCompleted(t) {
			for m := range queue {
				// m - downloading in queue?
				if m.Info() != nil && !pendingCompleted(m) {
					if startTorrent(m) {
						stopTorrent(t)
						queue[t] = now
						return true
					}
					// unable to start torrent, here no place to report an error. keep looping.
				}
			}
		}
	}

	// queue is empty, change nothing
	return false
}
