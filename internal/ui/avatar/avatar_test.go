package avatar

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/kitty"
)

func testJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

func TestConvert(t *testing.T) {
	out, err := Convert(testJPEG(640, 480))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != pixels || b.Dy() != pixels {
		t.Errorf("want %dx%d square, got %v", pixels, pixels, b)
	}
	small, err := Convert(testJPEG(40, 90))
	if err != nil {
		t.Fatal(err)
	}
	if img, _ := png.Decode(bytes.NewReader(small)); img.Bounds().Dx() != 40 || img.Bounds().Dy() != 40 {
		t.Errorf("small images are cropped, not upscaled: %v", img.Bounds())
	}
	if _, err := Convert([]byte("<html>not an image</html>")); err == nil {
		t.Error("garbage must fail")
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newRegistry(t *testing.T, handler http.HandlerFunc) (*Registry, *clock, string) {
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := &Registry{}
	r.Enable(kitty.Support{Enabled: true}, srv.Client())
	c := &clock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	r.now = c.now
	return r, c, srv.URL
}

func TestRegistryFlow(t *testing.T) {
	var hits atomic.Int32
	jpg := testJPEG(100, 100)
	r, c, base := newRegistry(t, func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(jpg)
	})
	url := base + "/img/user/1.jpg"

	if _, ok := r.Block(url, Cols, Rows); ok {
		t.Fatal("not loaded yet")
	}
	cmd := r.Request(url)
	if cmd == nil {
		t.Fatal("expected a load command")
	}
	if r.Request(url) != nil {
		t.Error("a second request while loading must not start another download")
	}
	if msg, ok := cmd().(LoadedMsg); !ok || msg.URL != url {
		t.Fatalf("got %#v", msg)
	}
	if r.Request(url) != nil || hits.Load() != 1 {
		t.Error("loaded images are not downloaded again")
	}

	blk, ok := r.Block(url, Cols, Rows)
	if !ok {
		t.Fatal("block should be ready")
	}
	lines := strings.Split(blk, "\n")
	if len(lines) != Rows || ansi.StringWidth(lines[0]) != Cols {
		t.Fatalf("block shape %d×%d", len(lines), ansi.StringWidth(lines[0]))
	}

	tx, pending := r.Transmissions()
	if !pending || !strings.Contains(tx, "a=t,f=100") || !strings.Contains(tx, "a=p,U=1") || ansi.StringWidth(tx) != 0 {
		t.Fatalf("expected an upload, got pending=%v len=%d", pending, len(tx))
	}
	// More frames shortly after (the renderer may coalesce them): still
	// included, identical, so the renderer doesn't rewrite the line.
	c.t = c.t.Add(10 * time.Millisecond)
	if tx2, _ := r.Transmissions(); tx2 != tx {
		t.Error("upload must be stable across frames")
	}
	// A long pause without frames: the next frame still carries it (no
	// flush has been proven yet beyond the first)…
	c.t = c.t.Add(uploadSpan)
	if tx3, _ := r.Transmissions(); tx3 != tx {
		t.Error("upload must stay until frames carrying it span uploadSpan")
	}
	// …and the one after drops it.
	c.t = c.t.Add(time.Millisecond)
	if tx, pending := r.Transmissions(); tx != "" || pending {
		t.Error("upload should be dropped once proven flushed")
	}
	r.Block(url, Cols, Rows)
	if tx, _ := r.Transmissions(); tx != "" {
		t.Error("an uploaded image is not uploaded again")
	}
	// A different size is a separate placement.
	r.Block(url, 8, 4)
	if tx, _ := r.Transmissions(); !strings.Contains(tx, "c=8,r=4") {
		t.Error("new size needs its own upload")
	}
	// After a resize (maybe another terminal window), visible images are
	// uploaded again.
	c.t = c.t.Add(time.Second)
	r.Transmissions()
	r.Reupload()
	if tx, _ := r.Transmissions(); tx != "" {
		t.Error("nothing is visible yet after Reupload")
	}
	r.Block(url, Cols, Rows)
	if tx, _ := r.Transmissions(); !strings.Contains(tx, "c=12,r=6") || strings.Contains(tx, "c=8,r=4") {
		t.Error("only the visible image is uploaded again")
	}
	if cl := r.Cleanup(); strings.Count(cl, "a=d") != 2 {
		t.Errorf("cleanup deletes every image ever uploaded: %q", cl)
	}
}

func TestRegistryFailures(t *testing.T) {
	var hits atomic.Int32
	r, c, base := newRegistry(t, func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		if strings.Contains(req.URL.Path, "html") {
			w.Write([]byte("<html>"))
			return
		}
		http.NotFound(w, req)
	})
	for _, u := range []string{base + "/missing.jpg", base + "/page.html"} {
		cmd := r.Request(u)
		if cmd == nil {
			t.Fatal("expected a load command")
		}
		cmd()
		if _, ok := r.Block(u, Cols, Rows); ok {
			t.Errorf("%s: failed image must not render", u)
		}
		if r.Request(u) != nil {
			t.Errorf("%s: failures are not retried immediately", u)
		}
	}
	c.t = c.t.Add(retryFailed + time.Second)
	if r.Request(base+"/missing.jpg") == nil {
		t.Error("failures are retried after a while")
	}
	for _, bad := range []string{"", "  ", "javascript:alert(1)", "file:///etc/passwd", "ftp://x/y.jpg"} {
		if r.Request(bad) != nil {
			t.Errorf("%q must be ignored", bad)
		}
	}
}

func TestDisabled(t *testing.T) {
	r := &Registry{}
	if r.Enabled() || r.Request("https://x/y.jpg") != nil {
		t.Error("zero registry must be inert")
	}
	if _, ok := r.Block("https://x/y.jpg", Cols, Rows); ok {
		t.Error("zero registry renders nothing")
	}
	if tx, settle := r.Transmissions(); tx != "" || settle || r.Cleanup() != "" {
		t.Error("zero registry emits nothing")
	}
}
