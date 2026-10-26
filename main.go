// discord-guild-gate is an authorization gateway that fronts a private
// web app trusting a username header (e.g. File Browser with
// auth.method=proxy). It signs users in with Discord OAuth2
// (identify guilds), maps their guild memberships, or their user ID on an
// allowlist, to configured groups, and forwards the selected group's
// account name to the upstream in the X-Username header (or, with
// GATE_GROUPS_HEADER, the user's own name plus all their groups).
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

type guildEntry struct {
	Label   string
	GuildID string
}

type userEntry struct {
	Label  string
	UserID string
}

type config struct {
	listen       string
	baseURL      string
	clientID     string
	clientSecret string
	cookieSecret []byte
	upstream     *url.URL
	guilds       []guildEntry // picker order = config order
	users        []userEntry  // allowlisted Discord users, listed after guilds; a label may repeat
	usernameTmpl string       // "{label}" / "{lang}" / "{user_id}" placeholders
	adminIDs     map[string]bool
	sessionTTL   time.Duration
	titleJA      string
	titleEN      string
	groupNameJA  string // display template, "{label}" placeholder
	groupNameEN  string
	groupNames   map[string]string // per-label display name, overrides the templates
	groupsHeader string            // if set, all groups go upstream in this header and there is no picker
	publicPaths  []string          // path prefixes proxied without authentication
	accent       string            // CSS hex color for buttons/links
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{3,8}$`)

// File Browser's public share links and the static assets they load.
const defaultPublicPaths = "/share/,/api/public/,/static/"

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
		groupsHeader: http.CanonicalHeaderKey(os.Getenv("GATE_GROUPS_HEADER")),
		publicPaths:  strings.FieldsFunc(envOr("GATE_PUBLIC_PATHS", defaultPublicPaths), isSep),
	}
	c.clientSecret = os.Getenv("DISCORD_CLIENT_SECRET")
	c.cookieSecret = []byte(os.Getenv("GATE_COOKIE_SECRET"))
	for _, name := range []string{"GATE_BASE_URL", "DISCORD_CLIENT_ID", "DISCORD_CLIENT_SECRET", "GATE_COOKIE_SECRET", "GATE_UPSTREAM"} {
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
	c.users, err = parseUsers(os.Getenv("GATE_USERS"))
	if err != nil {
		return nil, err
	}
	if len(c.guilds) == 0 && len(c.users) == 0 {
		return nil, fmt.Errorf("set GATE_GUILDS, GATE_USERS or both")
	}
	c.groupNames, err = parseGroupNames(os.Getenv("GATE_GROUP_NAMES"), c)
	if err != nil {
		return nil, err
	}
	if c.groupsHeader != "" {
		// without a picker no group is selected, so {label} would be empty
		// and every user would share one blank account name
		if strings.Contains(c.usernameTmpl, "{label}") || !strings.Contains(c.usernameTmpl, "{user_id}") {
			return nil, fmt.Errorf("with GATE_GROUPS_HEADER, GATE_USERNAME_TEMPLATE must contain {user_id} and not {label}")
		}
		if c.groupsHeader == "X-Username" {
			return nil, fmt.Errorf("GATE_GROUPS_HEADER must differ from X-Username")
		}
	}
	for _, p := range c.publicPaths {
		if !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") || p == "/" {
			return nil, fmt.Errorf("GATE_PUBLIC_PATHS entry %q must start and end with / and not be / itself", p)
		}
	}
	// one picker and one template namespace, so a label names one group
	for _, u := range c.users {
		for _, g := range c.guilds {
			if u.Label == g.Label {
				return nil, fmt.Errorf("label %q is in both GATE_GUILDS and GATE_USERS", u.Label)
			}
		}
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
	pairs, err := parseLabeled("GATE_GUILDS", "guildID", s, false)
	if err != nil {
		return nil, err
	}
	out := make([]guildEntry, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, guildEntry{Label: p[0], GuildID: p[1]})
	}
	return out, nil
}

// parseUsers reads "alice=123456789012345678,..."; the label is the
// account name under the {label} template. Repeating a label with other
// user IDs puts those users in one shared group.
func parseUsers(s string) ([]userEntry, error) {
	pairs, err := parseLabeled("GATE_USERS", "discordUserID", s, true)
	if err != nil {
		return nil, err
	}
	out := make([]userEntry, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, userEntry{Label: p[0], UserID: p[1]})
	}
	return out, nil
}

// parseLabeled reads comma-separated label=value pairs in config order.
// With repeat, a label may appear again with another value, but the same
// pair twice is still rejected as a likely typo.
func parseLabeled(name, value, s string, repeat bool) ([][2]string, error) {
	var out [][2]string
	seen := map[string]bool{}
	seenPair := map[[2]string]bool{}
	for _, pair := range strings.FieldsFunc(s, isSep) {
		label, v, ok := strings.Cut(pair, "=")
		if !ok || label == "" || v == "" {
			return nil, fmt.Errorf("invalid %s entry: %q (want label=%s)", name, pair, value)
		}
		if label == "admin" {
			return nil, fmt.Errorf(`%s label "admin" is reserved`, name)
		}
		p := [2]string{label, v}
		if (seen[label] && !repeat) || seenPair[p] {
			return nil, fmt.Errorf("duplicate %s entry: %q", name, pair)
		}
		seen[label] = true
		seenPair[p] = true
		out = append(out, p)
	}
	return out, nil
}

// parseGroupNames reads "label=Display name,..." for the picker. Only
// commas separate entries, so a display name may contain spaces.
func parseGroupNames(s string, c *config) (map[string]string, error) {
	labels := map[string]bool{}
	for _, g := range c.guilds {
		labels[g.Label] = true
	}
	for _, u := range c.users {
		labels[u.Label] = true
	}
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		label, display, ok := strings.Cut(pair, "=")
		label, display = strings.TrimSpace(label), strings.TrimSpace(display)
		if !ok || label == "" || display == "" {
			return nil, fmt.Errorf("invalid GATE_GROUP_NAMES entry: %q (want label=display name)", pair)
		}
		// a typo would otherwise leave the raw label on the picker unnoticed
		if !labels[label] {
			return nil, fmt.Errorf("GATE_GROUP_NAMES label %q is not in GATE_GUILDS or GATE_USERS", label)
		}
		out[label] = display
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
	log.Printf("listening on %s → %s (guilds: %v, users: %v)", cfg.listen, cfg.upstream, cfg.guilds, cfg.users)
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
	// always drop the client-supplied headers so they cannot be spoofed
	r.Header.Del("X-Username")
	if a.cfg.groupsHeader != "" {
		r.Header.Del(a.cfg.groupsHeader)
	}

	if a.isExempt(r.URL.Path) {
		a.proxy.ServeHTTP(w, r)
		return
	}

	s, ok := a.readSession(r)
	if ok {
		ok = a.refresh(s)
	}
	if !ok {
		clearSession(w)
		a.requireLogin(w, r)
		return
	}
	if a.cfg.groupsHeader != "" {
		r.Header.Set("X-Username", a.usernameFor(s))
		if len(s.Groups) > 0 {
			r.Header.Set(a.cfg.groupsHeader, strings.Join(s.Groups, ","))
		}
		a.proxy.ServeHTTP(w, r)
		return
	}
	if s.Group == "" {
		http.Redirect(w, r, "/pick", http.StatusFound)
		return
	}
	r.Header.Set("X-Username", a.usernameFor(s))
	a.proxy.ServeHTTP(w, r)
}

// refresh recomputes a session's access from the current config, so a
// config change applies at once instead of when the session expires:
// allowlist labels and admin rights come from the config alone, and guild
// labels from the session are kept only while still configured (their
// membership is re-checked with Discord at the next login). A selected
// group that is gone is dropped. It reports false when nothing is left.
func (a *app) refresh(s *session) bool {
	var groups []string
	for _, g := range a.cfg.guilds {
		if slices.Contains(s.Groups, g.Label) {
			groups = append(groups, g.Label)
		}
	}
	for _, u := range a.cfg.users {
		if u.UserID == s.UserID && !slices.Contains(groups, u.Label) {
			groups = append(groups, u.Label)
		}
	}
	s.Groups = groups
	s.Admin = a.cfg.adminIDs[s.UserID]
	if !s.mayPick(s.Group) {
		s.Group = ""
	}
	if s.Group == "" && len(groups) == 1 && !s.Admin {
		s.Group = groups[0]
	}
	return len(groups) > 0 || s.Admin
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

// isExempt reports paths served without authentication: by default File
// Browser's public share links and static assets. A prefix "/x/" also
// matches "/x" itself.
func (a *app) isExempt(path string) bool {
	for _, p := range a.cfg.publicPaths {
		if strings.HasPrefix(path, p) || path == strings.TrimSuffix(p, "/") {
			return true
		}
	}
	return false
}

// usernameFor derives the upstream account name from the session.
func (a *app) usernameFor(s *session) string {
	if s.Group == "admin" {
		return "admin"
	}
	return strings.NewReplacer("{label}", s.Group, "{lang}", s.Lang, "{user_id}", s.UserID).Replace(a.cfg.usernameTmpl)
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
