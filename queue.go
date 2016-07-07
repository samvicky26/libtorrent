package libtorrent

import (
	"sort"
	"time"

	"github.com/anacrolix/torrent"
)

var ActiveCount = 3
var QueueTimeout = (30 * time.Minute).Nanoseconds()

var queue map[*torrent.Torrent]int64

type Int64Slice []int64

func (p Int64Slice) Len() int           { return len(p) }
func (p Int64Slice) Less(i, j int) bool { return p[i] < p[j] }
func (p Int64Slice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p Int64Slice) Sort()              { sort.Sort(p) }

// priority start torrent. downloading torrent goes first, seeding second.
func queueStart(t *torrent.Torrent) bool {
	delete(queue, t)

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
	sort.Sort(Int64Slice(l)) // older torrent will be removed first

	now := time.Now().UnixNano()

	if !pendingCompleted(t) { // t is downloading
		// try to find seeding torrent
		for _, v := range l {
			m := q[v]
			// m is seeding?
			if pendingCompleted(m) {
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
	} else { // t is seeding
		// try to find first seeding torrent to replace with
		for _, v := range l {
			m := q[v]
			if pendingCompleted(m) { // seeding
				stopTorrent(m)
				queue[m] = now
				return startTorrent(t)
			}
		}
	}

	// seems like t seeding, and have no slots, just queue and downloading.

	// try to find first active torrent to replace with. if 't' seeding downloading will be resumed after 't' removed by timeout.
	for _, v := range l {
		m := q[v]
		stopTorrent(m)
		queue[m] = now
		return startTorrent(t)
	}

	// len(l) can't be == 0 should never be here
	stopTorrent(t)
	queue[t] = now
	return true
}

func queueEngine(t *torrent.Torrent) {
	timeout := time.Duration(QueueTimeout) * time.Nanosecond
	for {
		b1 := t.BytesCompleted()
		// in case if user set file to download on the same torrent, we need to receive Completed again.
		torrentstorageLock.Lock()
		ts := torrentstorage[t.InfoHash()]
		ts.next.Clear()
		torrentstorageLock.Unlock()
		select {
		case <-time.After(timeout):
		case <-ts.next.LockedChan(&mu):
			mu.Lock()
			fs := filestorage[t.InfoHash()]
			// we will be first who knows torrent is complete, and moved from active (downloading) state.
			if fs.CompletedDate == 0 {
				now := time.Now().UnixNano()
				fs.CompletedDate = now
				fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
				fs.ActivateDate = now // seeding time now
			}
			mu.Unlock()
		case <-t.Wait():
			return
		}
		timeout = time.Duration(QueueTimeout) * time.Nanosecond
		mu.Lock()
		if pendingCompleted(t) { // seeding
			if queueNext(t) {
				// we been removed, stop queue engine
				mu.Unlock()
				return
			} else {
				// we not been removed
				if len(queue) != 0 {
					// queue full, some one soon be available, check every minute
					timeout = 1 * time.Minute
				}
			}
		} else { // downloading
			// check stalled, and rotate if it does
			b2 := t.BytesCompleted()
			if b1 == b2 {
				if queueNext(t) {
					// we been removed, stop queue engine
					mu.Unlock()
					return
				} else {
					// we not been removed
					if len(queue) != 0 {
						// queue full, some one soon be available, check every minute
						timeout = 1 * time.Minute
					}
				}
			}
		}
		mu.Unlock()
	}
}

// 30 min seeding, download complete, 30 min stole torrent.
func queueNext(t *torrent.Torrent) bool {
	now := time.Now().UnixNano()

	// build active torrent array with activate time
	q := make(map[int64]*torrent.Torrent)
	var l []int64
	for m, v := range queue {
		// queue all || keep torrent resting for 30 mins
		if client.ActiveCount() < ActiveCount || v+QueueTimeout <= now {
			q[v] = m
			l = append(l, v)
		}
	}
	sort.Sort(Int64Slice(l)) // older first

	// check for downloading queue torrents
	for _, v := range l {
		m := q[v]
		if !pendingCompleted(m) {
			if startTorrent(m) {
				delete(queue, m)
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
		if pendingCompleted(m) {
			if startTorrent(m) {
				delete(queue, m)
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
		if pendingCompleted(t) {
			// load all torrents, we will take most oldest added to the queue
			q := make(map[int64]*torrent.Torrent)
			var l []int64
			// add all from queue
			for m, v := range queue {
				q[v] = m
				l = append(l, v)
			}
			sort.Sort(Int64Slice(l)) // older first

			for _, v := range l {
				m := q[v]
				// m - downloading in queue?
				if !pendingCompleted(m) {
					if startTorrent(m) {
						delete(queue, m)
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
