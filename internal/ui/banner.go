package ui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
)

// Banners is the announcement strip of one app section ("grades",
// "ratings", "search", or "" for home). Failures are silent: banners are
// optional decoration. Route Result[[]api.Banner] messages to Handle.
type Banners struct {
	Section string
	Load    Load
	items   []api.Banner
	// hidden holds banners dismissed this session (covers banners without
	// an id, which the settings store can't remember).
	hidden map[string]bool
}

// Fetch loads the section's banners, tagging the result with the page id.
func (b *Banners) Fetch(ctx *Ctx, id int) tea.Cmd {
	if ctx == nil || ctx.API == nil {
		return nil
	}
	client, section := ctx.API, b.Section
	return Fetch(id, b.Load.Begin(), func(c context.Context) ([]api.Banner, api.Meta, error) {
		return client.Banners(c, section)
	})
}

// Handle applies a result addressed to the page; errors are ignored.
func (b *Banners) Handle(msg Result[[]api.Banner]) {
	if !b.Load.Accept(msg.Seq) {
		return
	}
	b.Load.Loading = false
	if msg.Err == nil {
		b.items = msg.Data
		b.Load.Loaded = true
	}
}

// BannerKey identifies a banner for dismissal.
func BannerKey(bn api.Banner) string {
	if id := strings.TrimSpace(string(bn.ID)); id != "" {
		return id
	}
	return "title:" + Clean(bn.Title) + "|" + Clean(bn.Description)
}

// Current is the banner to show: the first enabled one with some text
// that hasn't been dismissed.
func (b *Banners) Current(ctx *Ctx) (api.Banner, bool) {
	for _, bn := range b.items {
		if !bn.Enabled() || (Clean(bn.Title) == "" && Clean(bn.Description) == "") {
			continue
		}
		key := BannerKey(bn)
		if b.hidden[key] {
			continue
		}
		if id := strings.TrimSpace(string(bn.ID)); id != "" && ctx != nil && ctx.Settings != nil && ctx.Settings.BannerDismissed(id) {
			continue
		}
		return bn, true
	}
	return api.Banner{}, false
}

// Dismissible reports whether a banner is shown that x can hide.
func (b *Banners) Dismissible(ctx *Ctx) bool {
	bn, ok := b.Current(ctx)
	return ok && bn.IsDismissible
}

// Dismiss hides the shown banner for good (if it allows that).
func (b *Banners) Dismiss(ctx *Ctx) tea.Cmd {
	bn, ok := b.Current(ctx)
	if !ok || !bn.IsDismissible {
		return nil
	}
	if b.hidden == nil {
		b.hidden = map[string]bool{}
	}
	b.hidden[BannerKey(bn)] = true
	if id := strings.TrimSpace(string(bn.ID)); id != "" && ctx != nil && ctx.Settings != nil {
		ctx.Settings.DismissBanner(id)
	}
	return Info("%s", Tr("Banner hidden", "Объявление скрыто"))
}

// View renders the banner as one line ("" when there is none).
func (b *Banners) View(ctx *Ctx, width int) string {
	bn, ok := b.Current(ctx)
	if !ok || width <= 0 {
		return ""
	}
	title, desc := Clean(bn.Title), Clean(bn.Description)
	if title == "" {
		title, desc = desc, ""
	}
	text := StyleWarn.Render("! " + title)
	if desc != "" {
		text += StyleDim.Render(" — " + desc)
	}
	right := ""
	if bn.IsDismissible {
		right = StyleKey.Render("x") + StyleDim.Render(Tr(" hide", " скрыть"))
	}
	avail := width - Width(right) - 2
	if right == "" || avail < 12 {
		return Trunc(text, width)
	}
	text = Trunc(text, avail)
	return text + strings.Repeat(" ", max(1, width-Width(text)-Width(right))) + right
}
