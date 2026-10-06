// hse-app-z is a keyboard-driven terminal client for HSE App X.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hse-app-z/internal/api"
	"hse-app-z/internal/app"
	"hse-app-z/internal/auth"
	"hse-app-z/internal/config"
	"hse-app-z/internal/kitty"
	"hse-app-z/internal/ui"
	"hse-app-z/internal/ui/avatar"
)

const usage = `hse-app-z — HSE App X in your terminal

Usage:
  hse-app-z [flags]            start the app (signs in on first run)
  hse-app-z login [flags]      sign in through the browser, then exit
  hse-app-z logout             forget the session and cached data
  hse-app-z token              print a fresh access token (for scripts)
  hse-app-z whoami             show the signed-in account
  hse-app-z doctor             show settings, files and image support

Flags:
  --demo                 run on recorded data, without signing in
  --fixtures DIR         recorded data for --demo (default: internal/api/testdata)
  --lang en|ru           interface and data language (default: last used,
                         else en; switch any time with L)
  --images auto|on|off   show avatars with the kitty graphics protocol
                         (auto: kitty and Ghostty; default from
                         HSE_APP_Z_IMAGES, else auto)
  --port N               OAuth callback port (default 8001; must match the
                         redirect URI registered at saml.hse.ru)
  --refresh-token TOKEN  with "login": import a refresh token (e.g. printed by
                         getRefreshToken.py) instead of opening the browser

Files live in the OS config/cache dirs (override with HSE_APP_Z_HOME).
Exit status: 0 on success, 1 on errors, 2 for invalid usage.
`

func main() {
	err := run(os.Args[1:], os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "hse-app-z:", err)
	var ue usageError
	if errors.As(err, &ue) {
		os.Exit(2)
	}
	os.Exit(1)
}

// usageError is a mistake on the command line (exit status 2).
type usageError struct{ error }

func usagef(format string, a ...any) error {
	return usageError{fmt.Errorf(format+" (see --help)", a...)}
}

// Seams for tests: token requests, the browser sign-in and the TUI itself
// can be replaced so tests never reach the network, a browser or the tty.
var (
	newAuthClient = func() *auth.Client { return &auth.Client{} }
	startLogin    = auth.StartLogin
	runProgram    = func(m tea.Model) error {
		_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		return err
	}
)

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("hse-app-z", flag.ContinueOnError)
	// Parse errors are reported once (by main), not with the whole usage.
	fs.SetOutput(io.Discard)
	demo := fs.Bool("demo", false, "")
	fixtures := fs.String("fixtures", "", "")
	lang := fs.String("lang", "", "")
	port := fs.Int("port", auth.DefaultPort, "")
	refreshToken := fs.String("refresh-token", "", "")
	images := fs.String("images", os.Getenv("HSE_APP_Z_IMAGES"), "")

	// The command may sit before, after or between flags
	// ("login --refresh-token T", "--port 9000 login --refresh-token T"):
	// the flag package stops at the first non-flag, so parse again after it.
	cmd, hasCmd := "", false
	err := fs.Parse(args)
	rest := fs.Args()
	if err == nil && len(rest) > 0 {
		cmd, hasCmd = rest[0], true
		err = fs.Parse(rest[1:])
		rest = fs.Args()
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return nil
		}
		return usagef("%v", err)
	}
	switch cmd {
	case "help":
		fmt.Fprint(stdout, usage)
		return nil
	case "", "login", "logout", "token", "whoami", "doctor":
		if hasCmd && cmd == "" {
			return usagef("empty command")
		}
	default:
		return usagef("unknown command %q", cmd)
	}
	if len(rest) > 0 {
		return usagef("unexpected argument %q", rest[0])
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	*lang = strings.ToLower(strings.TrimSpace(*lang))
	if *lang != "" && *lang != "en" && *lang != "ru" {
		return usagef("--lang must be en or ru")
	}
	if *port <= 0 || *port > 65535 {
		return usagef("--port must be 1-65535")
	}
	*images = strings.ToLower(strings.TrimSpace(*images))
	switch *images {
	case "", "auto", "on", "off":
	default:
		if set["images"] {
			return usagef("--images must be auto, on or off")
		}
		*images = "auto" // ignore a bad HSE_APP_Z_IMAGES rather than refuse to start
	}
	if set["images"] && cmd != "" && cmd != "doctor" {
		return usagef("--images only applies when starting the app, not to %q", cmd)
	}
	if set["refresh-token"] {
		if cmd != "login" {
			return usagef("--refresh-token only works with the login command")
		}
		if strings.TrimSpace(*refreshToken) == "" {
			return usagef("--refresh-token is empty")
		}
	}
	if cmd != "" && (*demo || *fixtures != "") {
		return usagef("--demo and --fixtures only apply when starting the app, not to %q", cmd)
	}
	if *fixtures != "" && !*demo {
		return usagef("--fixtures needs --demo")
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return fmt.Errorf("can't find config dir: %w", err)
	}
	store := &auth.Store{Path: paths.TokensFile()}
	authClient := newAuthClient()

	switch cmd {
	case "login":
		return runLogin(stdout, store, authClient, *port, strings.TrimSpace(*refreshToken))
	case "logout":
		_, statErr := os.Stat(store.Path)
		if err := store.Delete(); err != nil {
			return fmt.Errorf("couldn't remove the session: %w", err)
		}
		api.NewCache(paths.HTTPCacheDir()).Clear()
		if statErr == nil {
			fmt.Fprintln(stdout, "Signed out. Session and cached data removed.")
		} else {
			fmt.Fprintln(stdout, "Not signed in. Cached data removed.")
		}
		return nil
	case "token":
		src, err := loadSource(store, authClient)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tok, err := src.AccessToken(ctx, false)
		if err != nil {
			return friendlyAuthErr(err)
		}
		fmt.Fprintln(stdout, tok)
		return nil
	case "whoami":
		return whoami(stdout, store, authClient)
	case "doctor":
		return doctor(stdout, paths, store, *images)
	}
	return runTUI(paths, store, authClient, *demo, *fixtures, *lang, *images, *port)
}

func whoami(stdout io.Writer, store *auth.Store, client *auth.Client) error {
	src, err := loadSource(store, client)
	if err != nil {
		return err
	}
	t := src.Tokens()
	if who := accountLabel(src.Claims()); who != "" {
		fmt.Fprintln(stdout, who)
	} else {
		fmt.Fprintln(stdout, "Signed in (the access token doesn't name the account)")
	}
	now := time.Now()
	when := func(at time.Time) string {
		s := at.Local().Format(time.DateTime)
		if !now.Before(at) {
			s += " (expired)"
		}
		return s
	}
	if !t.ExpiresAt.IsZero() {
		fmt.Fprintf(stdout, "access token expires %s\n", when(t.ExpiresAt))
	}
	if !t.RefreshExpiresAt.IsZero() {
		fmt.Fprintf(stdout, "session expires      %s\n", when(t.RefreshExpiresAt))
	}
	if !src.SignedIn() {
		return errors.New("the session has expired — run: hse-app-z login")
	}
	return nil
}

// doctor prints what the app would use, to debug "why no avatars?" and
// "where are my files?" without starting the UI. It makes no network calls.
func doctor(stdout io.Writer, paths config.Paths, store *auth.Store, images string) error {
	row := func(k, v string) { fmt.Fprintf(stdout, "%-10s %s\n", k, v) }
	row("config", paths.ConfigDir)
	row("cache", paths.CacheDir)
	switch t, ok, err := store.Load(); {
	case err != nil:
		row("session", "unreadable: "+err.Error())
	case !ok:
		row("session", "not signed in (run: hse-app-z login)")
	default:
		c, _ := auth.ParseClaims(t.AccessToken)
		who := accountLabel(c)
		if who == "" {
			who = "signed in"
		}
		state := "token valid until " + t.ExpiresAt.Local().Format(time.DateTime)
		if !t.Valid(time.Now(), 0) {
			state = "token expired, will refresh"
			if !t.CanRefresh(time.Now()) {
				state = "session expired (run: hse-app-z login)"
			}
		}
		row("session", who+" — "+state)
	}
	row("language", config.LoadSettings(paths.SettingsFile()).GetLang())
	mode := images
	if mode == "" {
		mode = "auto"
	}
	sup := kitty.Detect(mode, os.Getenv)
	state := "off"
	if sup.Enabled {
		state = "on"
	}
	row("images", fmt.Sprintf("%s (--images %s) — %s", state, mode, sup.Reason))
	if sup.Hint != "" {
		row("", sup.Hint)
	}
	return nil
}

// accountLabel is "Name <email>", just one of them, or "" when the token
// carries neither.
func accountLabel(c auth.Claims) string {
	name, email := strings.TrimSpace(c.DisplayName()), c.UserEmail()
	switch {
	case name == "" || name == email:
		return email
	case email == "":
		return name
	}
	return name + " <" + email + ">"
}

func friendlyAuthErr(err error) error {
	if auth.IsLoginRequired(err) {
		return errors.New("not signed in (or the session expired) — run: hse-app-z login")
	}
	return err
}

func loadSource(store *auth.Store, client *auth.Client) (*auth.Source, error) {
	t, ok, err := store.Load()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("not signed in — run: hse-app-z login")
	}
	return auth.NewSource(t, store, client), nil
}

func runLogin(stdout io.Writer, store *auth.Store, client *auth.Client, port int, refreshToken string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var t auth.Tokens
	if refreshToken != "" {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var err error
		if t, err = client.Refresh(rctx, refreshToken); err != nil {
			if auth.IsLoginRequired(err) {
				return fmt.Errorf("that refresh token was rejected (expired, revoked or issued for another client): %w", err)
			}
			return fmt.Errorf("couldn't use that refresh token: %w", err)
		}
	} else {
		s, err := startLogin(client, port)
		if err != nil {
			return err
		}
		defer s.Close()
		fmt.Fprintln(stdout, "Opening the HSE sign-in page in your browser. If it doesn't open, visit:")
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "  "+s.AuthURL)
		fmt.Fprintln(stdout)
		if st, ok := ui.OpenURL(s.AuthURL)().(ui.StatusMsg); ok && st.Kind != ui.StatusOK {
			fmt.Fprintln(stdout, "(couldn't launch a browser automatically)")
		}
		fmt.Fprintln(stdout, "Waiting for the browser… (ctrl+c to cancel)")
		if t, err = s.Wait(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return errors.New("cancelled")
			}
			return err
		}
	}
	if err := store.Save(t); err != nil {
		return fmt.Errorf("signed in, but couldn't save the session: %w", err)
	}
	c, _ := auth.ParseClaims(t.AccessToken)
	if who := accountLabel(c); who != "" {
		fmt.Fprintf(stdout, "Signed in as %s.\n", who)
	} else {
		fmt.Fprintln(stdout, "Signed in.")
	}
	return nil
}

// demoNow is inside the recorded fixtures' date range.
var demoNow = time.Date(2026, 10, 13, 12, 30, 0, 0, api.Moscow)

func runTUI(paths config.Paths, store *auth.Store, authClient *auth.Client, demo bool, fixtures, lang, images string, port int) error {
	// Find the demo data before touching any settings.
	var dir string
	if demo {
		var err error
		if dir, err = findFixtures(fixtures); err != nil {
			return err
		}
	}
	settings := config.LoadSettings(paths.SettingsFile())
	if lang != "" {
		settings.SetLang(lang)
	}
	ctx := &ui.Ctx{
		Settings: settings,
		Favs:     config.LoadFavourites(paths.FavouritesFile()),
		Now:      time.Now,
		Demo:     demo,
	}
	var src *auth.Source
	if demo {
		client := api.NewClient("http://demo.invalid", nil, api.NewCache(""))
		client.HTTP = &http.Client{Transport: &api.FixtureTransport{Dir: dir, Latency: 150 * time.Millisecond}}
		ctx.API = client
		ctx.Me = ui.Me{Email: api.DemoEmail, Name: "Demo Student"}
		ctx.Now = func() time.Time { return demoNow.Add(time.Since(startTime)) }
		// Keep demo favourites away from the real ones.
		ctx.Favs = config.LoadFavourites("")
	} else {
		t, _, err := store.Load()
		if err != nil {
			return err
		}
		src = auth.NewSource(t, store, authClient)
		ctx.API = api.NewClient("", src, api.NewCache(paths.HTTPCacheDir()))
		app.ApplyIdentity(ctx, src)
	}
	ctx.API.SetLang(settings.GetLang())
	ui.SetLang(settings.GetLang())

	notice := ""
	if support := kitty.Detect(images, os.Getenv); support.Enabled {
		avatar.Default.Enable(support, nil)
	} else if support.Hint != "" {
		notice = ui.Tr(support.Hint, "Аватары выключены: tmux не пропускает изображения. Добавьте `set -g allow-passthrough on` в tmux.conf (или запустите с --images off).")
	}
	err := runProgram(app.New(app.Options{Ctx: ctx, Source: src, AuthClient: authClient, Port: port, Notice: notice}))
	// Free the avatars this run uploaded to the terminal.
	if cleanup := avatar.Default.Cleanup(); cleanup != "" {
		fmt.Fprint(os.Stdout, cleanup)
	}
	return err
}

var startTime = time.Now()

func findFixtures(dir string) (string, error) {
	var candidates []string
	if dir != "" {
		candidates = []string{dir}
	} else {
		candidates = []string{"internal/api/testdata"}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "internal/api/testdata"))
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(filepath.Join(c, "lessons_me.json")); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	if dir != "" {
		return "", fmt.Errorf("demo data not found in %s (it should hold the recorded responses from internal/api/testdata)", dir)
	}
	return "", fmt.Errorf("demo data not found (looked in %s); pass --fixtures DIR", strings.Join(candidates, ", "))
}
