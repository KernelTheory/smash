package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	barWidth    = 24
	detailWidth = 28
)

var (
	spinner  = []rune("⣾⣽⣻⢿⡿⣟⣯⣷")
	shatter  = []rune("▓▒░")
	keyChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz0123456789_-"
)

// render draws one frame of the "shredder": the finished part of the bar is solid redaction,
// the rest is a fake key re-rolled every frame that crumbles through ▓▒░ at the edge.
func render(label string, done, total, tick int, detail string) string {
	pct, filled := 0, 0
	if total > 0 {
		d := min(done, total)
		pct, filled = d*100/total, d*barWidth/total
	}
	var bar strings.Builder
	bar.WriteString(strings.Repeat("█", filled))
	for i := 0; filled+i < barWidth; i++ {
		if i < len(shatter) {
			bar.WriteRune(shatter[i])
		} else {
			bar.WriteByte(keyChars[rand.IntN(len(keyChars))])
		}
	}
	if utf8.RuneCountInString(detail) > detailWidth {
		detail = string([]rune(detail)[:detailWidth-1]) + "…"
	}
	detail += strings.Repeat(" ", detailWidth-utf8.RuneCountInString(detail))
	return fmt.Sprintf("%c %-9s %s %3d%%  %s", spinner[tick%len(spinner)], label, bar.String(), pct, detail)
}

// progress animates one status line on a terminal. A nil *progress does nothing, so callers
// never need to check whether output is a terminal (the daily job's output is a log file).
type progress struct {
	mu                sync.Mutex
	w                 *os.File
	label, detail     string
	done, total, tick int
	stop, stopped     chan struct{}
}

func newProgress(f *os.File) *progress {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 || os.Getenv("TERM") == "dumb" {
		return nil
	}
	p := &progress{w: f, stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(p.stopped)
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.mu.Lock()
				if p.label != "" {
					p.tick++
					fmt.Fprint(p.w, "\r\x1b[2K"+render(p.label, p.done, p.total, p.tick, p.detail))
				}
				p.mu.Unlock()
			}
		}
	}()
	return p
}

func (p *progress) set(label string, done, total int, detail string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.label, p.done, p.total, p.detail = label, done, total, detail
	p.mu.Unlock()
}

// Write prints a message on its own line; the bar redraws below it on the next frame.
func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprint(p.w, "\r\x1b[2K")
	return p.w.Write(b)
}

func (p *progress) finish() {
	if p == nil {
		return
	}
	close(p.stop)
	<-p.stopped
	fmt.Fprint(p.w, "\r\x1b[2K")
}
