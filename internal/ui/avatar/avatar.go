// Package avatar shows profile pictures in terminals that support the kitty
// graphics protocol (see internal/kitty). Usage from a page:
//
//	// in Update, when an avatar URL becomes relevant:
//	cmd := avatar.Request(url)
//	// in View:
//	if block, ok := avatar.Block(url, avatar.Cols, avatar.Rows); ok { … }
//
// Request downloads and converts the image in the background and then
// broadcasts LoadedMsg (which triggers a redraw). Block returns placeholder
// text for a ready image and schedules its one-time upload; the root model
// emits pending uploads via Transmissions in its next frames. When images
// are disabled (the default), Request returns nil and Block returns false,
// so pages render exactly as before.
package avatar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
	_ "image/gif"  // decoders
	_ "image/jpeg" //

	tea "github.com/charmbracelet/bubbletea"
	xdraw "golang.org/x/image/draw"

	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
)

// Default avatar size in cells (cells are about twice as tall as wide, so
// 12×6 is roughly square).
const (
	Cols = 12
	Rows = 6
)

const (
	pixels      = 192                    // longest side of the uploaded PNG
	maxDownload = 8 << 20                // bytes
	uploadSpan  = 250 * time.Millisecond // see entry.wanted
	retryFailed = 5 * time.Minute
	maxEntries  = 256
)

// LoadedMsg is broadcast when an image finished loading (ok or not).
type LoadedMsg struct{ URL string }

// SettleMsg asks the root to redraw (to drop flushed uploads from frames).
type SettleMsg struct{}

type key struct {
	url        string
	cols, rows int
}

type entry struct {
	id uint32
	// wanted is set when the image is first drawn. Its upload is then part
	// of every frame until frames carrying it have spanned uploadSpan: the
	// renderer flushes at least every ~16 ms, so by then one flush has
	// certainly written it (frames can be coalesced, so one frame isn't
	// enough).
	wanted          bool
	firstTx, lastTx time.Time
	uploaded        bool
	sent            bool // ever uploaded: the terminal may still hold it
	lastUsed        time.Time
}

// Registry holds loaded images. The zero value is disabled.
type Registry struct {
	mu      sync.Mutex
	support kitty.Support
	http    *http.Client
	now     func() time.Time
	images  map[string]*imageData // by URL: decoded once, shared by sizes
	entries map[key]*entry
	nextID  uint32
	sem     chan struct{}
}

type imageData struct {
	png     []byte
	err     error
	failed  time.Time
	loading bool
}

// Default is the process-wide registry used by the package functions.
var Default = &Registry{}

// Enable turns images on with the detected support. Call once at startup.
func (r *Registry) Enable(s kitty.Support, client *http.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.support = s
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	r.http = client
	r.now = time.Now
	r.images = map[string]*imageData{}
	r.entries = map[key]*entry{}
	// Start ids at a random 24-bit offset so we don't collide with images
	// of other programs in the same terminal.
	r.nextID = 0x100000 + uint32(rand.IntN(0xD00000))
	r.sem = make(chan struct{}, 2)
}

// Enabled reports whether images are shown.
func (r *Registry) Enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.support.Enabled
}

func usableURL(u string) bool {
	u = strings.TrimSpace(u)
	return u != "" && (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) && ui.SafeURL(u)
}

// Request starts loading url unless it is loaded, loading, or failed
// recently. Safe to call on every Update.
func (r *Registry) Request(url string) tea.Cmd {
	url = strings.TrimSpace(url)
	r.mu.Lock()
	if !r.support.Enabled || !usableURL(url) {
		r.mu.Unlock()
		return nil
	}
	d := r.images[url]
	switch {
	case d == nil:
		d = &imageData{}
		r.images[url] = d
	case d.loading, d.png != nil:
		r.mu.Unlock()
		return nil
	case d.err != nil && r.now().Sub(d.failed) < retryFailed:
		r.mu.Unlock()
		return nil
	}
	d.loading, d.err = true, nil
	client, sem := r.http, r.sem
	r.mu.Unlock()

	return func() tea.Msg {
		sem <- struct{}{}
		defer func() { <-sem }()
		data, err := fetch(client, url)
		r.mu.Lock()
		d.loading = false
		if err != nil {
			d.err, d.failed = err, r.now()
		} else {
			d.png = data
		}
		r.mu.Unlock()
		return LoadedMsg{URL: url}
	}
}

func fetch(client *http.Client, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avatar: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxDownload {
		return nil, errors.New("avatar: image too large")
	}
	return Convert(raw)
}

// Convert decodes an image (JPEG, PNG, GIF, WebP), crops it to a centered
// square, scales it down and re-encodes it as PNG (the format kitty takes).
func Convert(raw []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 40_000_000 {
		return nil, errors.New("avatar: unreasonable image size")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	size := min(side, pixels)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, xdraw.Src, nil)
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Block returns cols×rows placeholder cells (lines joined by "\n") showing
// url, or false when the image isn't available. Call it from View.
func (r *Registry) Block(url string, cols, rows int) (string, bool) {
	url = strings.TrimSpace(url)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.support.Enabled || cols <= 0 || rows <= 0 {
		return "", false
	}
	d := r.images[url]
	if d == nil || d.png == nil {
		return "", false
	}
	k := key{url, cols, rows}
	e := r.entries[k]
	now := r.now()
	if e == nil {
		r.evict()
		e = &entry{id: r.nextID}
		r.nextID++
		if r.nextID > 0xFFFFFF {
			r.nextID = 0x100000
		}
		r.entries[k] = e
	}
	e.wanted = true
	e.lastUsed = now
	return strings.Join(kitty.Placeholder(e.id, cols, rows), "\n"), true
}

// evict drops the least recently used entry when the table is full (its
// upload is forgotten; the terminal frees unused images itself).
func (r *Registry) evict() {
	if len(r.entries) < maxEntries {
		return
	}
	var oldest key
	var t time.Time
	for k, e := range r.entries {
		if t.IsZero() || e.lastUsed.Before(t) {
			oldest, t = k, e.lastUsed
		}
	}
	delete(r.entries, oldest)
}

// Transmissions returns the upload commands that must be part of the
// current frame (zero display width). The root prepends them to a line that
// rarely changes: the renderer skips unchanged lines, so an upload is
// written once even though it is part of several frames. pending reports
// whether any upload is still in flight.
func (r *Registry) Transmissions() (cmds string, pending bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.support.Enabled {
		return "", false
	}
	now := r.now()
	var b strings.Builder
	for k, e := range r.entries {
		if e.uploaded || !e.wanted {
			continue
		}
		if !e.firstTx.IsZero() && e.lastTx.Sub(e.firstTx) >= uploadSpan {
			e.uploaded = true
			continue
		}
		d := r.images[k.url]
		if d == nil || d.png == nil {
			continue
		}
		if e.firstTx.IsZero() {
			e.firstTx = now
		}
		e.lastTx, e.sent = now, true
		b.WriteString(r.support.Transmit(e.id, d.png, k.cols, k.rows))
		pending = true
	}
	return b.String(), pending
}

// Reupload forgets which images the terminal already has, so visible ones
// are uploaded again with the next frames. Call it when the terminal may
// have changed (a resize — e.g. tmux reattached from another window).
func (r *Registry) Reupload() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		e.wanted, e.uploaded = false, false
		e.firstTx, e.lastTx = time.Time{}, time.Time{}
	}
}

// Cleanup returns commands deleting every image this run uploaded (write
// them to the terminal after the program exits).
func (r *Registry) Cleanup() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.support.Enabled {
		return ""
	}
	var b strings.Builder
	for _, e := range r.entries {
		if e.sent {
			b.WriteString(r.support.Delete(e.id))
		}
	}
	return b.String()
}

// SettleAfter is how long after an upload starts the root should redraw
// once more so the upload can be dropped from frames.
const SettleAfter = uploadSpan + 50*time.Millisecond

// Package-level helpers on Default.

func Enabled() bool                                   { return Default.Enabled() }
func Request(url string) tea.Cmd                      { return Default.Request(url) }
func Block(url string, cols, rows int) (string, bool) { return Default.Block(url, cols, rows) }
