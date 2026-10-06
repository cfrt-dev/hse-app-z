package search

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/api"
	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

// placeholderCell is the kitty Unicode placeholder each image cell starts with.
const placeholderCell = "\U0010EEEE"

// enableAvatars turns images on for the test, served by a local server.
func enableAvatars(t *testing.T) (base string, hits *atomic.Int32) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{uint8(4 * x), uint8(4 * y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	hits = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)
	avatar.Default.Enable(kitty.Support{Enabled: true}, srv.Client())
	t.Cleanup(func() { avatar.Default = &avatar.Registry{} })
	return srv.URL, hits
}

// noBanner dismisses the search notices so the body starts on line 1.
func noBanner(p *Page) {
	for _, b := range p.banners {
		p.ctx.Settings.DismissBanner(bannerKey(b))
	}
}

// rightPane is the preview part of a stripped split line.
func rightPane(line string) string {
	_, r, _ := strings.Cut(line, "│")
	return r
}

// pictureLines returns the indexes of the lines showing image cells.
func pictureLines(view string) []int {
	var out []int
	for i, l := range strings.Split(view, "\n") {
		if strings.Contains(l, placeholderCell) {
			out = append(out, i)
		}
	}
	return out
}

func TestAvatarInPreview(t *testing.T) {
	base, downloads := enableAvatars(t)
	p, h, _ := newPage(t)
	noBanner(p)
	h.Key("ab", "esc")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "ab", items: []api.Person{
		{ID: "1", FullName: "Bez Kartinki", Type: "STUDENT", Email: "bk@edu.hse.ru"},
		{ID: "2", FullName: "Ivanov Ivan Ivanovich", Type: "STAFF", Email: "ivanov@hse.ru", Description: "Professor", AvatarURL: base + "/img/user/2.jpg"},
		{ID: "3", Type: "GROUP", Label: "БИБ255", AvatarURL: base + "/img/group.jpg"},
	}}})
	if v := p.View(100, 30); len(pictureLines(v)) != 0 || downloads.Load() != 0 {
		t.Fatalf("no picture for a person without one (downloads %d):\n%s", downloads.Load(), ansi.Strip(v))
	}

	// Selecting the person loads the picture; it sits at the top of the
	// preview with the name and kind to its right.
	h.Key("j")
	if downloads.Load() != 1 {
		t.Fatalf("selection change should download the avatar, got %d", downloads.Load())
	}
	raw := p.View(100, 30)
	checkSize(t, p, 100, 30)
	lines := strings.Split(raw, "\n")
	pic := pictureLines(raw)
	if len(pic) != avatar.Rows {
		t.Fatalf("want %d picture lines, got %v:\n%s", avatar.Rows, pic, ansi.Strip(raw))
	}
	for i, n := range pic {
		if n != pic[0]+i {
			t.Fatalf("picture lines must be consecutive: %v", pic)
		}
	}
	first := rightPane(ansi.Strip(lines[pic[0]]))
	if !strings.Contains(first, "Ivanov Ivan Ivanovich") || !strings.Contains(rightPane(ansi.Strip(lines[pic[0]+1])), "Staff · Professor") {
		t.Errorf("name and kind should be right of the picture:\n%s", ansi.Strip(raw))
	}
	if strings.Index(first, "Ivanov") < strings.Index(first, placeholderCell) {
		t.Errorf("the picture is left of the text: %q", first)
	}
	if pic[0] != 1 {
		t.Errorf("picture should start on the first preview line, got line %d", pic[0])
	}
	// The details follow under the picture.
	if !strings.Contains(ansi.Strip(lines[pic[len(pic)-1]+2]), "Email") {
		t.Errorf("details should follow the picture:\n%s", ansi.Strip(raw))
	}
	t.Logf("with avatar 100x30:\n%s", ansi.Strip(raw))

	// Narrow pane: the picture goes above the text.
	raw = checkSize(t, p, 60, 12)
	narrow := p.View(60, 12)
	pic = pictureLines(narrow)
	if len(pic) == 0 {
		t.Fatalf("picture missing at 60x12:\n%s", raw)
	}
	nl := strings.Split(ansi.Strip(narrow), "\n")
	for _, n := range pic {
		if strings.Contains(rightPane(nl[n]), "Ivanov") {
			t.Errorf("narrow layout puts the name under the picture:\n%s", raw)
		}
	}
	if below := rightPane(nl[pic[len(pic)-1]+2]); !strings.Contains(below, "Ivanov") {
		t.Errorf("name should follow the picture:\n%s", raw)
	}
	t.Logf("with avatar 60x12:\n%s", raw)

	// Too little room: no picture, layout as before.
	if v := p.View(60, 10); len(pictureLines(v)) != 0 {
		t.Errorf("no picture in a tiny pane:\n%s", ansi.Strip(v))
	}
	checkSizes(t, p)

	// Groups never show one (and aren't downloaded).
	h.Key("j")
	if v := p.View(100, 30); len(pictureLines(v)) != 0 || downloads.Load() != 1 {
		t.Errorf("group preview must not show a picture (downloads %d)", downloads.Load())
	}

	// New results: the first hit's picture loads without a key press.
	h.Key("i", "ctrl+u", "cd", "esc")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "cd", items: []api.Person{
		{ID: "4", FullName: "Petrova Anna", Type: "STUDENT", Email: "ap@edu.hse.ru", AvatarURL: base + "/img/user/4.jpg"},
	}}})
	if downloads.Load() != 2 || len(pictureLines(p.View(100, 30))) != avatar.Rows {
		t.Errorf("results should load the selected avatar (downloads %d):\n%s", downloads.Load(), ansi.Strip(p.View(100, 30)))
	}

	// Scrolling the preview keeps every line within the pane.
	h.Key("J", "J", "J")
	checkSizes(t, p)
}

func TestAvatarDisabledRendersAsBefore(t *testing.T) {
	p, h, _ := newPage(t)
	noBanner(p)
	h.Key("ab", "esc")
	h.Send(ui.Result[hits]{ID: p.id, Seq: p.load.Begin(), Data: hits{q: "ab", items: []api.Person{
		{ID: "2", FullName: "Ivanov Ivan", Type: "STAFF", Email: "ivanov@hse.ru", AvatarURL: "https://example.invalid/a.jpg"},
	}}})
	v := h.View(100, 30)
	if len(pictureLines(v)) != 0 {
		t.Fatal("images are disabled by default")
	}
	if l := strings.Split(v, "\n")[1]; !strings.Contains(l, "│ Ivanov Ivan") {
		t.Errorf("name should start the preview: %q", l)
	}
}
