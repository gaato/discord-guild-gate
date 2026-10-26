package main

import (
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const stateCookie = "guild_gate_state"

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	state := randomHex(16)
	next := safeNext(r.URL.Query().Get("next"))
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state + "|" + next,
		Path:     "/auth/",
		MaxAge:   600,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, a.authorizeURL(state), http.StatusFound)
}

// safeNext prevents open redirects (same-site paths only).
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func (a *app) handleCallback(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	state, next, _ := strings.Cut(c.Value, "|")
	next = safeNext(next)
	if state == "" || r.URL.Query().Get("state") != state {
		a.errorPage(w, http.StatusBadRequest, "state が一致しません。もう一度ログインしてください。",
			"State mismatch. Please try logging in again.")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		// e.g. the user cancelled on the consent screen
		a.errorPage(w, http.StatusBadRequest, "Discord 認証がキャンセルされました。",
			"Discord authorization was cancelled.")
		return
	}

	ctx := r.Context()
	token, err := a.exchangeCode(ctx, code)
	if err != nil {
		log.Printf("callback: %v", err)
		a.errorPage(w, http.StatusBadGateway, "Discord との通信に失敗しました。時間をおいて再試行してください。",
			"Failed to talk to Discord. Please try again later.")
		return
	}
	user, err := a.fetchUser(ctx, token)
	if err != nil {
		log.Printf("callback: %v", err)
		a.errorPage(w, http.StatusBadGateway, "Discord ユーザー情報の取得に失敗しました。",
			"Failed to fetch your Discord profile.")
		return
	}
	var groups []string
	if len(a.cfg.guilds) > 0 {
		guilds, err := a.fetchGuildIDs(ctx, token)
		if err != nil {
			log.Printf("callback: %v", err)
			a.errorPage(w, http.StatusBadGateway, "Discord サーバー一覧の取得に失敗しました。",
				"Failed to fetch your Discord server list.")
			return
		}
		for _, g := range a.cfg.guilds {
			if guilds[g.GuildID] {
				groups = append(groups, g.Label)
			}
		}
	}
	for _, u := range a.cfg.users {
		if u.UserID == user.ID && !slices.Contains(groups, u.Label) {
			groups = append(groups, u.Label)
		}
	}
	admin := a.cfg.adminIDs[user.ID]
	if len(groups) == 0 && !admin {
		log.Printf("denied: %s (%s) is not in any configured guild or the user allowlist", user.Username, user.ID)
		if len(a.cfg.guilds) == 0 {
			// show the ID so the user can send it for the allowlist
			a.errorPage(w, http.StatusForbidden,
				"この Discord アカウントには利用許可がありません。利用したい場合は、次のユーザーID を管理者に伝えてください: "+user.ID,
				"This Discord account is not allowed. To get access, send this user ID to the administrator: "+user.ID)
			return
		}
		a.errorPage(w, http.StatusForbidden,
			"対象の Discord サーバーへの参加が確認できませんでした。参加済みなのにこの画面が出る場合は運営までご連絡ください。",
			"We could not confirm your membership in a required Discord server. If you believe this is an error, please contact the staff.")
		return
	}

	name := user.GlobalName
	if name == "" {
		name = user.Username
	}
	s := &session{
		UserID: user.ID,
		Name:   name,
		Groups: groups,
		Admin:  admin,
		Lang:   pickLang(r.Header.Get("Accept-Language")),
		Exp:    time.Now().Add(a.cfg.sessionTTL).Unix(),
	}
	if len(groups) == 1 && !admin {
		s.Group = groups[0]
	}
	a.setSession(w, s)
	log.Printf("login: %s (%s) groups=%v admin=%v lang=%s", user.Username, user.ID, groups, admin, s.Lang)

	if s.Group == "" {
		http.Redirect(w, r, "/pick?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	a.logoutPage(w)
}

func (a *app) handlePickPage(w http.ResponseWriter, r *http.Request) {
	s, ok := a.readSession(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if lang := r.URL.Query().Get("lang"); lang == "ja" || lang == "en" {
		s.Lang = lang
		a.setSession(w, s)
	}
	a.pickPage(w, s, safeNext(r.URL.Query().Get("next")))
}

func (a *app) handlePickSubmit(w http.ResponseWriter, r *http.Request) {
	s, ok := a.readSession(r)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	group := r.FormValue("group")
	if !s.mayPick(group) {
		a.errorPage(w, http.StatusForbidden, "その項目は選択できません。", "You cannot select that.")
		return
	}
	s.Group = group
	a.setSession(w, s)
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusFound)
}

func (s *session) mayPick(group string) bool {
	if group == "admin" {
		return s.Admin
	}
	for _, g := range s.Groups {
		if g == group {
			return true
		}
	}
	return false
}
