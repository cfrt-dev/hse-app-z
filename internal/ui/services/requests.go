package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/ui"
)

// requestsPage lists the user's SmartPoint requests (GET /tasks). The item
// schema is undocumented, so items are shown as generic key/value pairs.
type requestsPage struct {
	ctx  *ui.Ctx
	id   int
	load ui.Load

	items []map[string]any
	next  string          // cursor of the next page ("" = no more)
	seen  map[string]bool // cursors already fetched (guards against loops)
	keys  map[string]bool // items already listed (pages may overlap)

	list   ui.List
	scroll ui.Scroll

	// who is the account the requests were fetched for.
	who string
}

// tasksResult tags a fetched page with the cursor it was requested with.
type tasksResult struct {
	Page   api.TasksPage
	Cursor string
	More   bool // appending a further page (vs. replacing everything)
}

// newRequests builds the page; first (may be nil) is an already loaded
// first page, so opening the page doesn't refetch it.
func newRequests(ctx *ui.Ctx, first *api.TasksPage) *requestsPage {
	p := &requestsPage{ctx: ctx, id: ui.NewID(), seen: map[string]bool{}}
	if ctx != nil {
		p.who = ctx.Me.Email
	}
	if first != nil {
		p.apply(tasksResult{Page: *first})
		p.load.Loaded = true
	}
	return p
}

func (p *requestsPage) Title() string    { return myRequests() }
func (p *requestsPage) Capturing() bool  { return false }
func (p *requestsPage) Hints() []ui.Hint { return hints() }

func (p *requestsPage) Init() tea.Cmd {
	if p.load.Loaded {
		return nil
	}
	return p.fetch("", false)
}

func (p *requestsPage) fetch(cursor string, more bool) tea.Cmd {
	if p.ctx == nil || p.ctx.API == nil {
		return nil
	}
	client := p.ctx.API
	p.who = p.ctx.Me.Email
	return ui.Fetch(p.id, p.load.Begin(), func(c context.Context) (tasksResult, api.Meta, error) {
		tp, meta, err := client.Tasks(c, cursor)
		return tasksResult{Page: tp, Cursor: cursor, More: more}, meta, err
	})
}

func (p *requestsPage) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ui.Result[tasksResult]:
		if msg.ID != p.id || !p.load.Accept(msg.Seq) {
			return nil
		}
		cmd := p.load.Done(msg.Meta, msg.Err)
		if msg.Err == nil {
			p.apply(msg.Data)
		}
		return cmd
	case ui.ReloadMsg:
		if p.ctx != nil && p.who != p.ctx.Me.Email {
			// Another account signed in: drop the previous one's requests
			// now rather than when (or if) the reload succeeds.
			p.apply(tasksResult{})
			p.load.Loaded, p.load.Err = false, nil
			p.scroll.Reset()
		}
		return p.fetch("", false)
	case tea.KeyMsg:
		return p.handleKey(msg)
	}
	return nil
}

func (p *requestsPage) apply(r tasksResult) {
	if !r.More {
		p.items = nil
		p.seen = map[string]bool{}
		p.keys = map[string]bool{}
	}
	if p.keys == nil {
		p.keys = map[string]bool{}
	}
	for _, it := range r.Page.Items {
		if k := itemKey(it); k != "" {
			if p.keys[k] {
				continue
			}
			p.keys[k] = true
		}
		p.items = append(p.items, it)
	}
	cur := strings.TrimSpace(r.Cursor)
	if cur == "" {
		cur = strings.TrimSpace(string(r.Page.Cursor))
	}
	p.seen[cur] = true
	next := strings.TrimSpace(string(r.Page.NextCursor))
	if next == "" || next == "0" || next == strings.TrimSpace(string(r.Page.Cursor)) || p.seen[next] {
		next = ""
	}
	p.next = next
	p.list.SetLen(p.rowCount())
}

func (p *requestsPage) rowCount() int {
	if p.next != "" {
		return len(p.items) + 1
	}
	return len(p.items)
}

func (p *requestsPage) onMore() bool {
	return p.next != "" && p.list.HasSelection() && p.list.Cursor == len(p.items)
}

func (p *requestsPage) selected() (map[string]any, bool) {
	if !p.list.HasSelection() || p.list.Cursor >= len(p.items) {
		return nil, false
	}
	return p.items[p.list.Cursor], true
}

func (p *requestsPage) handleKey(msg tea.KeyMsg) tea.Cmd {
	before := p.list.Cursor
	if p.list.HandleKey(msg) {
		if p.list.Cursor != before {
			p.scroll.Reset()
		}
		return nil
	}
	if p.scroll.HandleKey(msg) {
		return nil
	}
	switch msg.String() {
	case "r":
		return p.fetch("", false)
	case "enter", "o":
		if p.onMore() {
			if p.load.Loading {
				return nil
			}
			return p.fetch(p.next, true)
		}
		if it, ok := p.selected(); ok {
			if u := itemURL(it); u != "" {
				return ui.OpenURL(u)
			}
			return ui.Info("%s", ui.Tr("This request has no link", "У этой заявки нет ссылки"))
		}
	case "y":
		if it, ok := p.selected(); ok {
			if u := itemURL(it); u != "" {
				return ui.Copy(u, linkWord())
			}
			return noLinkToCopy()
		}
	}
	return nil
}

// ------------------------------------------------------------------ view

func (p *requestsPage) View(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if sv := ui.StateView(width, height, p.load, p.rowCount(), ui.Tr("No requests", "Заявок нет")); sv != "" {
		return sv
	}
	lw := ui.LeftWidth(width)
	rw := ui.RightWidth(width, lw)
	note := ui.LoadNote(p.load)
	if note == "" && p.load.Err != nil {
		note = ui.StyleErr.Render("⚠ " + ui.Clean(ui.ErrText(p.load.Err)))
	}
	left := paneTitle(myRequests(), note, lw)
	if height > 1 {
		left += "\n" + p.list.Render(lw, height-1, p.renderRow)
	}
	right := ""
	if it, ok := p.selected(); ok && rw > 0 {
		right = p.scroll.Render(balanceSGR(itemDetail(it, rw)), rw, height)
	} else if p.onMore() && rw > 0 {
		right = ui.StyleDim.Render(ui.Trunc(ui.Tr("press enter to load more requests", "нажмите enter, чтобы загрузить ещё"), rw))
	}
	return ui.Split(width, height, lw, left, right)
}

func (p *requestsPage) renderRow(i int, sel bool, w int) string {
	if i == len(p.items) && p.next != "" {
		label := ui.Tr("Load more…", "Загрузить ещё…")
		if p.load.Loading {
			label = ui.Tr("Loading…", "Загрузка…")
		}
		return ui.Row(sel, ui.StyleAccent.Render(label), w)
	}
	if i < 0 || i >= len(p.items) {
		return ""
	}
	it := p.items[i]
	title := itemTitle(it, i)
	status := itemStatus(it)
	content := title
	if status != "" {
		if nw := w - 1 - ui.Width(status) - 1; nw >= 8 {
			content = ui.PadRight(title, nw) + " " + ui.StyleDim.Render(status)
		}
	}
	return ui.Row(sel, content, w)
}

// --------------------------------------------------------- generic items

var titleKeys = []string{"title", "name", "subject", "service_name", "number", "id"}

// itemKey identifies a request across pages: its id, or its whole content
// when it has none ("" = can't tell, never merged).
func itemKey(it map[string]any) string {
	if id := scalarText(it["id"]); id != "" {
		return "id:" + id
	}
	b, err := json.Marshal(it) // map keys are sorted: stable
	if err != nil {
		return ""
	}
	return "json:" + string(b)
}

func itemTitle(it map[string]any, i int) string {
	for _, k := range titleKeys {
		if s := scalarText(it[k]); s != "" {
			return s
		}
	}
	return ui.Trf("Request %d", "Заявка %d", i+1)
}

func itemStatus(it map[string]any) string {
	for _, k := range []string{"status", "state"} {
		if s := scalarText(it[k]); s != "" {
			return s
		}
	}
	return ""
}

// scalarText renders strings, numbers and booleans on one line; objects
// contribute their name/title/label/value; anything else is "".
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return ui.Clean(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return ui.Clean(x.String())
	case int:
		return strconv.Itoa(x)
	case bool:
		if x {
			return ui.Tr("yes", "да")
		}
		return ui.Tr("no", "нет")
	case map[string]any:
		for _, k := range []string{"name", "title", "label", "value"} {
			if s, ok := x[k].(string); ok && ui.Clean(s) != "" {
				return ui.Clean(s)
			}
		}
	}
	return ""
}

// itemURL is the first top-level string value (in key order) that is a web
// link.
func itemURL(it map[string]any) string {
	for _, k := range sortedKeys(it) {
		if s, ok := it[k].(string); ok {
			s = strings.TrimSpace(s)
			if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
				return s
			}
		}
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

const maxJSON = 240

// valueText renders any JSON value for the detail pane.
func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case string:
		if s := ui.CleanMulti(x); s != "" {
			return s
		}
		return "—"
	case map[string]any, []any:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			return "…"
		}
		s := ui.Clean(buf.String())
		if r := []rune(s); len(r) > maxJSON {
			s = string(r[:maxJSON]) + "…"
		}
		return s
	}
	if s := scalarText(v); s != "" {
		return s
	}
	return ui.Clean(fmt.Sprint(v))
}

func itemDetail(it map[string]any, w int) string {
	title := itemTitle(it, 0)
	blocks := []string{styleLines(ui.Wrap(title, w), ui.StyleTitle)}
	if st := itemStatus(it); st != "" {
		blocks = append(blocks, styleLines(ui.Wrap(st, w), ui.StyleAccent))
	}
	keys := sortedKeys(it)
	lw := 4
	for _, k := range keys {
		lw = max(lw, ui.Width(ui.Clean(k)))
	}
	lw = min(lw, 18, max(4, w/3))
	var kvs []string
	for _, k := range keys {
		kvs = append(kvs, kv(ui.Clean(k), valueText(it[k]), lw, w))
	}
	blocks = append(blocks, "\n"+strings.Join(kvs, "\n"))
	if u := itemURL(it); u != "" {
		blocks = append(blocks, "\n"+styleLines(ui.Wrap(ui.Tr("o open link · y copy link", "o открыть ссылку · y копировать ссылку"), w), ui.StyleDim))
	}
	return ui.Lines(blocks...)
}

// kv renders "label  value" with a label column of lw cells and the value
// wrapped under itself.
func kv(label, value string, lw, w int) string {
	vw := w - lw - 1
	if vw < 8 {
		// Too narrow for two columns: label on its own line.
		return ui.StyleDim.Render(ui.Trunc(label, w)) + "\n" + ui.Wrap(value, w)
	}
	lines := strings.Split(ui.Wrap(value, vw), "\n")
	var b strings.Builder
	b.WriteString(ui.StyleDim.Render(ui.PadRight(label, lw)))
	b.WriteString(" ")
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteString("\n")
		b.WriteString(strings.Repeat(" ", lw+1))
		b.WriteString(l)
	}
	return b.String()
}
