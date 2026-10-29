// Package thumbs pre-builds Windows thumbnails for the photos and videos on
// a mounted network drive (the phone drive), so File Explorer shows them
// instantly instead of downloading files while the user waits.
//
// Explorer's thumbnail providers need the whole file for photos and the
// first few MB for videos, so there is no shortcut over the network: each
// file is read once, in the background, newest first. The results go to the
// system thumbnail cache, which survives remounts because wintp pins each
// drive's volume serial (see mount.VolumeIdentityArgs).
package thumbs

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// mediaExts are the file types Explorer shows thumbnails for that a phone
// typically holds.
var mediaExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".heic": true, ".heif": true, ".bmp": true, ".dng": true,
	".mp4": true, ".mov": true, ".3gp": true, ".mkv": true, ".webm": true, ".m4v": true,
}

// skipDirs are never walked: app-private data and hidden folders hold no
// user media and can be huge.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") ||
		strings.EqualFold(name, "Android") || strings.EqualFold(name, "System Volume Information")
}

// Status reports pre-loading progress.
type Status struct {
	Running bool // a pass is in progress
	Scanned bool // the current pass has finished listing files
	Total   int  // media files found
	Ready   int  // thumbnails in the cache
	Failed  int  // files Windows could not make a thumbnail for
}

const (
	workers      = 2
	rescanEvery  = 10 * time.Minute
	notifyEvery  = time.Second
	initialDelay = 3 * time.Second // let Explorer and rclone settle after mounting
)

// Prewarmer walks a drive and builds missing thumbnails.
type Prewarmer struct {
	// OnChange is called (from a worker goroutine, at most about once a
	// second) when Status changes.
	OnChange func()

	size     int
	has      func(path string, size int) bool
	generate func(path string, size int) error
	withCOM  func(func())
	delay    time.Duration

	mu         sync.Mutex
	root       string
	cancel     context.CancelFunc
	done       chan struct{}
	status     Status
	lastNotify time.Time
}

// New returns a prewarmer that builds thumbnails at DefaultSize.
func New() *Prewarmer {
	return &Prewarmer{size: DefaultSize(), has: shellHas, generate: shellGenerate,
		withCOM: withCOM, delay: initialDelay}
}

// Start pre-loads thumbnails under root (e.g. `Z:\`) until Stop, rescanning
// periodically for new files. Calling it again with the same root is a no-op.
func (p *Prewarmer) Start(root string) {
	p.mu.Lock()
	if p.cancel != nil && p.root == root {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	p.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.mu.Lock()
	p.root, p.cancel, p.done = root, cancel, done
	p.status = Status{Running: true}
	p.mu.Unlock()
	p.notify(true)

	go func() {
		defer close(done)
		p.loop(ctx, root)
	}()
}

// Stop ends pre-loading and waits for in-flight work to finish.
func (p *Prewarmer) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done, p.root = nil, nil, ""
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	p.mu.Lock()
	p.status = Status{}
	p.mu.Unlock()
	p.notify(true)
}

// Status returns the current progress.
func (p *Prewarmer) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Prewarmer) notify(force bool) {
	p.mu.Lock()
	if !force && time.Since(p.lastNotify) < notifyEvery {
		p.mu.Unlock()
		return
	}
	p.lastNotify = time.Now()
	p.mu.Unlock()
	if p.OnChange != nil {
		p.OnChange()
	}
}

func (p *Prewarmer) update(fn func(*Status)) {
	p.mu.Lock()
	fn(&p.status)
	p.mu.Unlock()
	p.notify(false)
}

func (p *Prewarmer) loop(ctx context.Context, root string) {
	if !sleep(ctx, p.delay) {
		return
	}
	for {
		p.update(func(s *Status) { *s = Status{Running: true} })
		files := scan(ctx, root)
		p.update(func(s *Status) { s.Scanned, s.Total = true, len(files) })
		p.pass(ctx, files)
		if ctx.Err() != nil {
			return
		}
		p.update(func(s *Status) { s.Running = false })
		p.notify(true)
		if !sleep(ctx, rescanEvery) {
			return
		}
	}
}

// pass builds thumbnails for files with a small worker pool.
func (p *Prewarmer) pass(ctx context.Context, files []string) {
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.withCOM(func() {
				for path := range jobs {
					ok := p.has(path, p.size) || p.generate(path, p.size) == nil
					p.update(func(s *Status) {
						if ok {
							s.Ready++
						} else {
							s.Failed++
						}
					})
				}
			})
		}()
	}
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
}

// scan lists the media files under root, newest first, so recent photos
// get thumbnails before old ones.
func scan(ctx context.Context, root string) []string {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !mediaExts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		var mod time.Time
		if info, err := d.Info(); err == nil {
			mod = info.ModTime()
		}
		files = append(files, file{path, mod})
		return nil
	})
	sort.SliceStable(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
