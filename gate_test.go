package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T, upstream string) *app {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config{
		cookieSecret: []byte("0123456789abcdef0123456789abcdef"),
		upstream:     u,
		guilds: []guildEntry{
			{Label: "2027", GuildID: "222"},
			{Label: "2026", GuildID: "111"},
		},
		usernameTmpl: "{lang}{label}",
		publicPaths:  strings.Split(defaultPublicPaths, ","),
		adminIDs:     map[string]bool{"42": true},
		sessionTTL:   time.Hour,
	}
	return &app{cfg: cfg, proxy: newProxy(u), client: http.DefaultClient}
}

func TestSessionRoundTrip(t *testing.T) {
	a := testApp(t, "http://upstream")
	s := &session{
		UserID: "1", Name: "test", Groups: []string{"2027", "2026"},
		Group: "2026", Lang: "ja", Exp: time.Now().Add(time.Hour).Unix(),
	}
	got, ok := a.verifySession(a.signSession(s))
	if !ok {
		t.Fatal("valid session rejected")
	}
	if got.Group != "2026" || got.Lang != "ja" || len(got.Groups) != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSessionTamperRejected(t *testing.T) {
	a := testApp(t, "http://upstream")
	s := &session{UserID: "1", Group: "2026", Exp: time.Now().Add(time.Hour).Unix()}
	signed := a.signSession(s)
	// flip the first byte of the payload so the signature no longer matches
	flip := byte('A')
	if signed[0] == 'A' {
		flip = 'B'
	}
	tampered := string(flip) + signed[1:]
	if _, ok := a.verifySession(tampered); ok {
		t.Fatal("tampered session accepted")
	}
	if _, ok := a.verifySession("garbage"); ok {
		t.Fatal("garbage accepted")
	}
}

func TestSessionExpiry(t *testing.T) {
	a := testApp(t, "http://upstream")
	s := &session{UserID: "1", Group: "2026", Exp: time.Now().Add(-time.Minute).Unix()}
	if _, ok := a.verifySession(a.signSession(s)); ok {
		t.Fatal("expired session accepted")
	}
}

func TestParseGuilds(t *testing.T) {
	gs, err := parseGuilds("2027=222, 2026=111")
	if err != nil {
		t.Fatal(err)
	}
	// config order is preserved
	if len(gs) != 2 || gs[0].Label != "2027" || gs[1].GuildID != "111" {
		t.Fatalf("unexpected: %+v", gs)
	}
	for _, bad := range []string{"2026", "admin=123", "a=1,a=2"} {
		if _, err := parseGuilds(bad); err == nil {
			t.Errorf("parseGuilds(%q) should fail", bad)
		}
	}
	// optional on its own: loadConfig requires GATE_GUILDS or GATE_USERS
	if gs, err := parseGuilds(""); err != nil || len(gs) != 0 {
		t.Fatalf("empty GATE_GUILDS: %v %+v", err, gs)
	}
}

func TestParseUsers(t *testing.T) {
	us, err := parseUsers("alice=123, bob=456")
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 2 || us[0].Label != "alice" || us[1].UserID != "456" {
		t.Fatalf("unexpected: %+v", us)
	}
	for _, bad := range []string{"123", "admin=123", "a=1,a=1"} {
		if _, err := parseUsers(bad); err == nil {
			t.Errorf("parseUsers(%q) should fail", bad)
		}
	}
	// a repeated label with other IDs is one shared group
	us, err = parseUsers("alice=1,team=1,team=2")
	if err != nil {
		t.Fatal(err)
	}
	if len(us) != 3 || us[2].Label != "team" || us[2].UserID != "2" {
		t.Fatalf("shared label: %+v", us)
	}
}

func TestParseGroupNames(t *testing.T) {
	c := &config{
		guilds: []guildEntry{{Label: "2026", GuildID: "111"}},
		users:  []userEntry{{Label: "koinrandolly", UserID: "1"}},
	}
	names, err := parseGroupNames("koinrandolly=コインランドリー, 2026 = Project 2026 ,", c)
	if err != nil {
		t.Fatal(err)
	}
	if names["koinrandolly"] != "コインランドリー" || names["2026"] != "Project 2026" {
		t.Fatalf("unexpected: %q", names)
	}
	for _, bad := range []string{"koinrandolly", "=x", "nobody=x"} {
		if _, err := parseGroupNames(bad, c); err == nil {
			t.Errorf("parseGroupNames(%q) should fail", bad)
		}
	}
	a := testApp(t, "http://upstream")
	a.cfg.groupNameJA = "企画{label}"
	a.cfg.groupNames = names
	if got := a.groupDisplay("ja", "koinrandolly"); got != "コインランドリー" {
		t.Fatalf("named: %q", got)
	}
	if got := a.groupDisplay("ja", "2027"); got != "企画2027" {
		t.Fatalf("template fallback: %q", got)
	}
}

func TestLoadConfigNeedsGuildsOrUsers(t *testing.T) {
	t.Setenv("GATE_BASE_URL", "https://files.example.com")
	t.Setenv("DISCORD_CLIENT_ID", "id")
	t.Setenv("DISCORD_CLIENT_SECRET", "secret")
	t.Setenv("GATE_COOKIE_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GATE_UPSTREAM", "http://127.0.0.1:8080")
	t.Setenv("GATE_GUILDS", "")
	t.Setenv("GATE_USERS", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("neither GATE_GUILDS nor GATE_USERS: should fail")
	}
	t.Setenv("GATE_USERS", "alice=123")
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.users) != 1 || len(c.guilds) != 0 {
		t.Fatalf("unexpected: %+v %+v", c.users, c.guilds)
	}
	t.Setenv("GATE_GUILDS", "alice=999")
	if _, err := loadConfig(); err == nil {
		t.Fatal("label in both lists: should fail")
	}
}

func TestScope(t *testing.T) {
	a := testApp(t, "http://upstream")
	if got := a.scope(); got != "identify guilds" {
		t.Fatalf("with guilds: %q", got)
	}
	a.cfg.guilds = nil
	if got := a.scope(); got != "identify" {
		t.Fatalf("users only: %q", got)
	}
}

func TestPickLang(t *testing.T) {
	cases := map[string]string{
		"ja,en-US;q=0.9":       "ja",
		"en-US,ja;q=0.8":       "en",
		"ko-KR,en;q=0.9":       "en",
		"":                     "en",
		"ja-JP":                "ja",
		"fr-FR;q=0.9,ja;q=0.8": "ja",
	}
	for in, want := range cases {
		if got := pickLang(in); got != want {
			t.Errorf("pickLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUsernameFor(t *testing.T) {
	a := testApp(t, "http://upstream")
	if got := a.usernameFor(&session{Group: "2026", Lang: "ja"}); got != "ja2026" {
		t.Fatalf("got %q", got)
	}
	if got := a.usernameFor(&session{Group: "2026", Lang: "en"}); got != "en2026" {
		t.Fatalf("got %q", got)
	}
	if got := a.usernameFor(&session{Group: "admin", Lang: "ja"}); got != "admin" {
		t.Fatalf("got %q", got)
	}
	a.cfg.usernameTmpl = "{label}"
	if got := a.usernameFor(&session{Group: "2026", Lang: "ja"}); got != "2026" {
		t.Fatalf("default template: got %q", got)
	}
	a.cfg.usernameTmpl = "{user_id}"
	if got := a.usernameFor(&session{UserID: "123456789012345678", Group: "2026", Lang: "ja"}); got != "123456789012345678" {
		t.Fatalf("user_id template: got %q", got)
	}
	if got := a.usernameFor(&session{UserID: "123456789012345678", Group: "admin", Lang: "ja"}); got != "admin" {
		t.Fatalf("user_id template, admin: got %q", got)
	}
}

func TestIsExempt(t *testing.T) {
	a := testApp(t, "http://upstream")
	for _, p := range []string{"/share/abc", "/api/public/dl/abc/f.png", "/static/js/app.js", "/share"} {
		if !a.isExempt(p) {
			t.Errorf("%s should be exempt", p)
		}
	}
	for _, p := range []string{"/", "/files/2026", "/api/resources/", "/sharex", "/api/users"} {
		if a.isExempt(p) {
			t.Errorf("%s should NOT be exempt", p)
		}
	}
	a.cfg.publicPaths = []string{"/share/", "/.cpr/"}
	if !a.isExempt("/.cpr/ui.css") || a.isExempt("/static/js/app.js") {
		t.Error("GATE_PUBLIC_PATHS not applied")
	}
}

// With GATE_GROUPS_HEADER the user's own name and all groups go upstream,
// no group needs picking, and spoofed values are dropped everywhere.
func TestGatewayGroupsHeader(t *testing.T) {
	type seen struct{ user, groups string }
	var got []seen
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, seen{r.Header.Get("X-Username"), r.Header.Get("X-Groups")})
	}))
	defer upstream.Close()
	a := testApp(t, upstream.URL)
	a.cfg.groupsHeader = "X-Groups"
	a.cfg.usernameTmpl = "{user_id}"
	a.cfg.publicPaths = []string{"/share/"}
	a.cfg.users = []userEntry{{Label: "team", UserID: "123"}}

	req := httptest.NewRequest("GET", "/share/abc", nil)
	req.Header.Set("X-Groups", "team")
	a.gateway(httptest.NewRecorder(), req)

	s := &session{UserID: "123", Groups: []string{"team", "2026"}, Lang: "ja",
		Exp: time.Now().Add(time.Hour).Unix()}
	req = httptest.NewRequest("GET", "/team/", nil)
	req.Header.Set("X-Groups", "admin")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession(s)})
	rec := httptest.NewRecorder()
	a.gateway(rec, req)

	want := []seen{{"", ""}, {"123", "2026,team"}}
	if rec.Code == http.StatusFound || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("code %d, upstream saw %+v", rec.Code, got)
	}
}

func TestLoadConfigGroupsHeader(t *testing.T) {
	t.Setenv("GATE_BASE_URL", "https://files.example.com")
	t.Setenv("DISCORD_CLIENT_ID", "id")
	t.Setenv("DISCORD_CLIENT_SECRET", "secret")
	t.Setenv("GATE_COOKIE_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("GATE_UPSTREAM", "http://127.0.0.1:3923")
	t.Setenv("GATE_USERS", "team=1")
	t.Setenv("GATE_GROUPS_HEADER", "x-groups")
	t.Setenv("GATE_USERNAME_TEMPLATE", "{label}")
	if _, err := loadConfig(); err == nil {
		t.Fatal("{label} with GATE_GROUPS_HEADER should fail")
	}
	t.Setenv("GATE_USERNAME_TEMPLATE", "{user_id}")
	t.Setenv("GATE_PUBLIC_PATHS", "/share/,/.cpr/")
	c, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.groupsHeader != "X-Groups" || len(c.publicPaths) != 2 {
		t.Fatalf("unexpected: %q %q", c.groupsHeader, c.publicPaths)
	}
	t.Setenv("GATE_PUBLIC_PATHS", "/")
	if _, err := loadConfig(); err == nil {
		t.Fatal("GATE_PUBLIC_PATHS=/ should fail")
	}
}

func TestMayPick(t *testing.T) {
	s := &session{Groups: []string{"2027", "2026"}, Admin: false}
	if !s.mayPick("2026") || s.mayPick("2024") || s.mayPick("admin") {
		t.Fatal("mayPick wrong for member")
	}
	admin := &session{Groups: nil, Admin: true}
	if !admin.mayPick("admin") || admin.mayPick("2026") {
		t.Fatal("mayPick wrong for admin")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/files/2026":          "/files/2026",
		"//evil.example":       "/",
		"https://evil.example": "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// The gateway must add X-Username to authorized requests and always
// drop the client-supplied header.
func TestGatewayHeaderInjection(t *testing.T) {
	var gotUser []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = append(gotUser, r.Header.Get("X-Username"))
	}))
	defer upstream.Close()
	a := testApp(t, upstream.URL)

	// unauthenticated + spoofed header → exempt path is proxied anonymously
	req := httptest.NewRequest("GET", "/share/abc", nil)
	req.Header.Set("X-Username", "admin")
	a.gateway(httptest.NewRecorder(), req)

	// authenticated → the selected group's account name is set
	s := &session{UserID: "1", Groups: []string{"2026"}, Group: "2026", Lang: "ja",
		Exp: time.Now().Add(time.Hour).Unix()}
	req = httptest.NewRequest("GET", "/api/resources/", nil)
	req.Header.Set("X-Username", "admin") // spoof must be overwritten
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession(s)})
	a.gateway(httptest.NewRecorder(), req)

	if len(gotUser) != 2 || gotUser[0] != "" || gotUser[1] != "ja2026" {
		t.Fatalf("X-Username seen by upstream: %q", gotUser)
	}
}

// Unauthenticated browser navigation redirects to login; non-GET gets 401.
func TestGatewayUnauthenticated(t *testing.T) {
	a := testApp(t, "http://upstream.invalid")

	rec := httptest.NewRecorder()
	a.gateway(rec, httptest.NewRequest("GET", "/files/2026?p=1", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("GET: got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/login?next=") || !strings.Contains(loc, url.QueryEscape("/files/2026?p=1")) {
		t.Fatalf("Location: %s", loc)
	}

	rec = httptest.NewRecorder()
	a.gateway(rec, httptest.NewRequest("POST", "/api/resources/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST: got %d", rec.Code)
	}
}

// Access follows the current config, not the one at login.
func TestRefresh(t *testing.T) {
	a := testApp(t, "http://upstream")
	a.cfg.users = []userEntry{{Label: "team", UserID: "7"}}

	// a guild label still configured stays, a dropped one goes, and an
	// allowlist label added since login appears
	s := &session{UserID: "7", Groups: []string{"2026", "2019"}, Group: "2019"}
	if !a.refresh(s) || !slices.Equal(s.Groups, []string{"2026", "team"}) || s.Group != "" {
		t.Fatalf("after refresh: %+v", s)
	}

	// removed from the allowlist and from every configured guild: no access
	gone := &session{UserID: "8", Groups: []string{"team", "2019"}, Group: "team"}
	if a.refresh(gone) {
		t.Fatalf("removed user kept access: %+v", gone)
	}

	// admin rights are not taken from the cookie
	fake := &session{UserID: "8", Admin: true, Group: "admin"}
	if a.refresh(fake) || fake.Group != "" {
		t.Fatalf("cookie admin kept: %+v", fake)
	}
	real := &session{UserID: "42", Group: "admin"}
	if !a.refresh(real) || !real.Admin || real.Group != "admin" {
		t.Fatalf("configured admin lost: %+v", real)
	}

	// one group left and no admin: selected without the picker
	one := &session{UserID: "7"}
	a.cfg.guilds = nil
	if !a.refresh(one) || one.Group != "team" {
		t.Fatalf("single group not selected: %+v", one)
	}
}

// A session from before a config change must not carry old groups upstream.
func TestGatewayDropsStaleGroups(t *testing.T) {
	var groups []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groups = append(groups, r.Header.Get("X-Groups"))
	}))
	defer upstream.Close()
	a := testApp(t, upstream.URL)
	a.cfg.guilds = nil
	a.cfg.users = []userEntry{{Label: "team", UserID: "7"}}
	a.cfg.groupsHeader = "X-Groups"
	a.cfg.usernameTmpl = "{user_id}"

	for _, s := range []*session{
		{UserID: "7", Groups: []string{"oldlabel"}, Exp: time.Now().Add(time.Hour).Unix()},
		{UserID: "9", Groups: []string{"team"}, Exp: time.Now().Add(time.Hour).Unix()},
	} {
		req := httptest.NewRequest("GET", "/team/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: a.signSession(s)})
		a.gateway(httptest.NewRecorder(), req)
	}
	if !slices.Equal(groups, []string{"team"}) {
		t.Fatalf("upstream saw groups %q", groups)
	}
}
