// discord-guild-gate is an authorization gateway that fronts a private
// web app trusting a username header (e.g. File Browser with
// auth.method=proxy). It signs users in with Discord OAuth2
// (identify guilds), maps their guild memberships to configured groups,
// and forwards the selected group's shared account name to the upstream
// in the X-Username header.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type guildEntry struct {
	Label   string
	GuildID string
}

type config struct {
	listen       string
	baseURL      string
	clientID     string
	clientSecret string
	cookieSecret []byte
	upstream     *url.URL
	guilds       []guildEntry // picker order = config order
	usernameTmpl string       // "{label}" / "{lang}" placeholders
	adminIDs     map[string]bool
	sessionTTL   time.Duration
	titleJA      string
	titleEN      string
	groupNameJA  string // display template, "{label}" placeholder
	groupNameEN  string
	accent       string // CSS hex color for buttons/links
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

func loadConfig() (*config, error) {
	c := &config{
		listen:       envOr("GATE_LISTEN", ":8081"),
		baseURL:      strings.TrimRight(os.Getenv("GATE_BASE_URL"), "/"),
		clientID:     os.Getenv("DISCORD_CLIENT_ID"),
		usernameTmpl: envOr("GATE_USERNAME_TEMPLATE", "{label}"),
		adminIDs:     map[string]bool{},
		sessionTTL:   24 * time.Hour,
		titleJA:      envOr("GATE_TITLE_JA", "Discord Guild Gate"),
		titleEN:      envOr("GATE_TITLE_EN", "Discord Guild Gate"),
		groupNameJA:  envOr("GATE_GROUP_NAME_JA", "{label}"),
		groupNameEN:  envOr("GATE_GROUP_NAME_EN", "{label}"),
		accent:       envOr("GATE_ACCENT", "#5865f2"),
	}
	c.clientSecret = os.Getenv("DISCORD_CLIENT_SECRET")
	c.cookieSecret = []byte(os.Getenv("GATE_COOKIE_SECRET"))
	for _, name := range []string{"GATE_BASE_URL", "DISCORD_CLIENT_ID", "DISCORD_CLIENT_SECRET", "GATE_COOKIE_SECRET", "GATE_UPSTREAM", "GATE_GUILDS"} {
		if os.Getenv(name) == "" {
			return nil, fmt.Errorf("environment variable %s is not set", name)
		}
	}
	if len(c.cookieSecret) < 32 {
		return nil, fmt.Errorf("GATE_COOKIE_SECRET must be at least 32 characters (e.g. openssl rand -hex 32)")
	}
	if !accentRe.MatchString(c.accent) {
		return nil, fmt.Errorf("GATE_ACCENT must be a hex color like #5865f2, got %q", c.accent)
	}
	u, err := url.Parse(os.Getenv("GATE_UPSTREAM"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid GATE_UPSTREAM: %q", os.Getenv("GATE_UPSTREAM"))
	}
	c.upstream = u
	c.guilds, err = parseGuilds(os.Getenv("GATE_GUILDS"))
	if err != nil {
		return nil, err
	}
	for _, id := range strings.FieldsFunc(os.Getenv("GATE_ADMIN_DISCORD_IDS"), isSep) {
		c.adminIDs[id] = true
	}
	if v := os.Getenv("GATE_SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid GATE_SESSION_TTL: %q", v)
		}
		c.sessionTTL = d
	}
	return c, nil
}

func isSep(r rune) bool { return r == ',' || r == ';' || r == ' ' }

// parseGuilds reads "2026=1386246992589619231,2027=..." keeping config order
// (the picker lists groups in this order).
func parseGuilds(s string) ([]guildEntry, error) {
	var out []guildEntry
	seen := map[string]bool{}
	for _, pair := range strings.FieldsFunc(s, isSep) {
		label, guild, ok := strings.Cut(pair, "=")
		if !ok || label == "" || guild == "" {
			return nil, fmt.Errorf("invalid GATE_GUILDS entry: %q (want label=guildID)", pair)
		}
		if label == "admin" {
			return nil, fmt.Errorf(`GATE_GUILDS label "admin" is reserved`)
		}
		if seen[label] {
			return nil, fmt.Errorf("duplicate GATE_GUILDS label: %q", label)
		}
		seen[label] = true
		out = append(out, guildEntry{Label: label, GuildID: guild})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("GATE_GUILDS is empty")
	}
	return out, nil
}

type app struct {
	cfg    *config
	proxy  http.Handler
	client *http.Client
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	a := &app{
		cfg:    cfg,
		proxy:  newProxy(cfg.upstream),
		client: &http.Client{Timeout: 10 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", a.handleLogin)
	mux.HandleFunc("GET /auth/callback", a.handleCallback)
	mux.HandleFunc("GET /auth/logout", a.handleLogout)
	mux.HandleFunc("GET /pick", a.handlePickPage)
	mux.HandleFunc("POST /pick", a.handlePickSubmit)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", a.gateway)

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// no Read/WriteTimeout so large uploads are not killed
	}
	log.Printf("listening on %s → %s (guilds: %v)", cfg.listen, cfg.upstream, cfg.guilds)
	log.Fatal(srv.ListenAndServe())
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// gateway proxies authorized requests to the upstream.
func (a *app) gateway(w http.ResponseWriter, r *http.Request) {
	// always drop the client-supplied header so it cannot be spoofed
	r.Header.Del("X-Username")

	if isExempt(r.URL.Path) {
		a.proxy.ServeHTTP(w, r)
		return
	}

	s, ok := a.readSession(r)
	if !ok {
		a.requireLogin(w, r)
		return
	}
	if s.Group == "" {
		http.Redirect(w, r, "/pick", http.StatusFound)
		return
	}
	r.Header.Set("X-Username", a.usernameFor(s))
	a.proxy.ServeHTTP(w, r)
}

func (a *app) requireLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		next := r.URL.Path
		if r.URL.RawQuery != "" {
			next += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, "/auth/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	http.Error(w, "authentication required", http.StatusUnauthorized)
}

// isExempt lists paths served without authentication
// (File Browser public share links and static assets).
func isExempt(path string) bool {
	for _, p := range []string{"/share/", "/api/public/", "/static/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return path == "/share"
}

// usernameFor derives the upstream account name from the session.
func (a *app) usernameFor(s *session) string {
	if s.Group == "admin" {
		return "admin"
	}
	return strings.NewReplacer("{label}", s.Group, "{lang}", s.Lang).Replace(a.cfg.usernameTmpl)
}

// pickLang chooses ja / en from an Accept-Language header.
func pickLang(acceptLanguage string) string {
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch {
		case strings.HasPrefix(tag, "ja"):
			return "ja"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "en"
}
