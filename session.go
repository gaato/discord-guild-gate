package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "guild_gate"

type session struct {
	UserID string   `json:"uid"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`          // groups the user may access, picker order
	Admin  bool     `json:"admin"`           // may select the admin account
	Group  string   `json:"group,omitempty"` // selected group label or "admin"
	Lang   string   `json:"lang"`            // "ja" / "en"
	Exp    int64    `json:"exp"`
}

func (a *app) signSession(s *session) string {
	payload, _ := json.Marshal(s)
	b64 := base64.RawURLEncoding.EncodeToString(payload)
	return b64 + "." + a.sign(b64)
}

func (a *app) sign(data string) string {
	mac := hmac.New(sha256.New, a.cfg.cookieSecret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *app) readSession(r *http.Request) (*session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	return a.verifySession(c.Value)
}

func (a *app) verifySession(value string) (*session, bool) {
	b64, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(a.sign(b64)), []byte(sig)) {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, false
	}
	var s session
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, false
	}
	if time.Now().Unix() >= s.Exp {
		return nil, false
	}
	return &s, true
}

func (a *app) setSession(w http.ResponseWriter, s *session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    a.signSession(s),
		Path:     "/",
		Expires:  time.Unix(s.Exp, 0),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
