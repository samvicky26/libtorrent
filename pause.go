package libtorrent

import (
	"time"

	"github.com/anacrolix/torrent"
)

func Pause() {
	mu.Lock()
	defer mu.Unlock()

	if pause == nil {
		pause = make(map[*torrent.Torrent]int32)
	}

	for _, t := range torrents {
		if _, ok := pause[t]; ok {
			continue
		}
		s := torrentStatus(t)
		switch s {
		case StatusPaused:
			// ignore
		case StatusChecking:
			// ignore
		default:
			delete(queue, t)
			stopTorrent(t)
			pause[t] = s
		}
	}

	mappingStop()
}

func Resume() {
	mu.Lock()
	defer mu.Unlock()

	// every time application call resume() means network configuration changed.
	// we need update port info stats and port mapping.
	go func() {
		mappingPort(1 * time.Second)

		mappingStart()
	}()

	if pause == nil {
		return
	}

	now := time.Now().UnixNano()

	// at first resume active
	for t, s := range pause {
		switch s {
		case StatusQueued:
		default:
			if !queueStart(t) { // problem starting? unable to report error. queue it manually.
				queue[t] = now
			}
		}
	}
	// second run resume queued
	for t, s := range pause {
		switch s {
		case StatusQueued:
			// user can remove active torrents from queue while paused.
			// so we may still have slots available after 'resume active' step. start until we full.
			if client.ActiveCount() < ActiveCount {
				if !startTorrent(t) { // problem starting? unable to report error. queue it manually.
					queue[t] = now
				}
			} else {
				queue[t] = now
			}
		default:
		}
	}
	pause = nil
}

func Paused() bool {
	mu.Lock()
	defer mu.Unlock()
	return pause != nil
}
