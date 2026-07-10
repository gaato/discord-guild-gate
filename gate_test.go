package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
	for _, bad := range []string{"2026", "", "admin=123", "a=1,a=2"} {
		if _, err := parseGuilds(bad); err == nil {
			t.Errorf("parseGuilds(%q) should fail", bad)
		}
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
}

func TestIsExempt(t *testing.T) {
	for _, p := range []string{"/share/abc", "/api/public/dl/abc/f.png", "/static/js/app.js", "/share"} {
		if !isExempt(p) {
			t.Errorf("%s should be exempt", p)
		}
	}
	for _, p := range []string{"/", "/files/2026", "/api/resources/", "/sharex", "/api/users"} {
		if isExempt(p) {
			t.Errorf("%s should NOT be exempt", p)
		}
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
