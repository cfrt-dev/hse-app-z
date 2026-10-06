package app

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

func TestLanguageSwitchesInterface(t *testing.T) {
	t.Cleanup(func() { ui.SetLang("en") })
	d := demoDriver(t)
	if v := d.view(); !strings.Contains(v, "Schedule") || !strings.Contains(v, "help") {
		t.Fatalf("english header/footer expected:\n%s", v)
	}
	d.keys("L")
	if ui.Lang() != "ru" {
		t.Fatal("L must switch the interface language")
	}
	v := d.view()
	for _, want := range []string{"Язык: русский"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q after L:\n%s", want, v)
		}
	}
	d.send(clearStatusMsg{seq: d.m.statusSeq})
	v = d.view()
	if !strings.Contains(v, "справка") || !strings.Contains(v, "выход") {
		t.Errorf("footer not translated:\n%s", v)
	}
	d.keys("?")
	if v := d.view(); !strings.Contains(v, "Везде") || !strings.Contains(v, "Клавиши") {
		t.Errorf("help not translated:\n%s", v)
	}
	checkFits(t, d.m.View(), 110, 32)
	d.keys("j", "L")
	if ui.Lang() != "en" {
		t.Error("L must switch back")
	}
}

// avatarPage draws one avatar.
type avatarPage struct {
	fakePage
	url string
}

func (p *avatarPage) Init() tea.Cmd { return avatar.Request(p.url) }
func (p *avatarPage) View(w, h int) string {
	if blk, ok := avatar.Block(p.url, avatar.Cols, avatar.Rows); ok {
		return blk
	}
	return "no avatar"
}

func TestAvatarUploadRidesOnHeader(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.Black)
	var jpg bytes.Buffer
	_ = jpeg.Encode(&jpg, img, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(jpg.Bytes()) }))
	defer srv.Close()
	avatar.Default = &avatar.Registry{}
	avatar.Default.Enable(kitty.Support{Enabled: true}, srv.Client())
	t.Cleanup(func() { avatar.Default = &avatar.Registry{} })

	d := demoDriver(t)
	d.send(ui.PushMsg{Page: &avatarPage{fakePage: fakePage{title: "Person"}, url: srv.URL + "/a.jpg"}})
	raw := d.m.View()
	first := strings.SplitN(raw, "\n", 2)[0]
	if !strings.HasPrefix(first, "\x1b_Ga=t,f=100") || !strings.Contains(first, "a=p,U=1") {
		t.Fatalf("upload should be at the start of the header line: %q", first[:min(60, len(first))])
	}
	if !strings.Contains(raw, "\U0010EEEE") {
		t.Error("body should contain placeholder cells")
	}
	checkFits(t, raw, 110, 32)
	if ansi.StringWidth(first) > 110 {
		t.Error("upload must not count towards the header width")
	}
	if !d.m.uploadPending {
		t.Error("root should know an upload is pending")
	}
	// The next update schedules exactly one settle redraw.
	_, cmd := d.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if cmd == nil || !d.m.settleScheduled {
		t.Error("expected a settle tick")
	}
	// Frames over a span > 250 ms prove a flush; then the upload goes away.
	time.Sleep(300 * time.Millisecond)
	d.m.View()
	if first := strings.SplitN(d.m.View(), "\n", 2)[0]; strings.Contains(first, "\x1b_G") {
		t.Error("upload should be dropped once flushed")
	}
	if d.m.uploadPending {
		t.Error("nothing pending any more")
	}
}

// Digits must keep switching tabs after passing through Search: the search
// field is only focused by "/" or "i".
func TestDigitsPassThroughSearchTab(t *testing.T) {
	d := demoDriver(t)
	d.keys("4", "5")
	if d.m.active != 4 {
		t.Fatalf("4 then 5 should land on Food, active=%d", d.m.active)
	}
	d.keys("/")
	if d.m.active != searchTab || !d.m.top().Capturing() {
		t.Fatal("/ opens search with the field focused")
	}
	d.keys("5")
	if d.m.active != searchTab {
		t.Fatal("while typing, digits go into the field")
	}
	d.keys("esc", "6")
	if d.m.active != 5 {
		t.Fatalf("after esc digits switch tabs again, active=%d", d.m.active)
	}
}

func TestStartupNotice(t *testing.T) {
	ctx := demoDriver(t).m.ctx
	d := newDriver(t, Options{Ctx: ctx, Notice: "Avatars are off: tmux blocks image passthrough."})
	if v := d.view(); !strings.Contains(v, "Avatars are off") {
		t.Errorf("notice should be in the footer:\n%s", v)
	}
}
