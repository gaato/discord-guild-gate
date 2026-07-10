package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const discordAPI = "https://discord.com/api/v10"

func (a *app) redirectURI() string {
	return a.cfg.baseURL + "/auth/callback"
}

func (a *app) authorizeURL(state string) string {
	q := url.Values{
		"client_id":     {a.cfg.clientID},
		"response_type": {"code"},
		"redirect_uri":  {a.redirectURI()},
		"scope":         {"identify guilds"},
		"state":         {state},
		"prompt":        {"none"}, // skip the consent screen if already authorized
	}
	return "https://discord.com/oauth2/authorize?" + q.Encode()
}

func (a *app) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {a.cfg.clientID},
		"client_secret": {a.cfg.clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.redirectURI()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		discordAPI+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := a.discordDo(req, &body); err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("token exchange: empty access_token")
	}
	return body.AccessToken, nil
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

func (a *app) fetchUser(ctx context.Context, token string) (*discordUser, error) {
	var u discordUser
	if err := a.discordGet(ctx, token, "/users/@me", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// fetchGuildIDs returns the set of guild IDs the user is in
// (guilds scope, up to 200 guilds).
func (a *app) fetchGuildIDs(ctx context.Context, token string) (map[string]bool, error) {
	var guilds []struct {
		ID string `json:"id"`
	}
	if err := a.discordGet(ctx, token, "/users/@me/guilds", &guilds); err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(guilds))
	for _, g := range guilds {
		ids[g.ID] = true
	}
	return ids, nil
}

func (a *app) discordGet(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordAPI+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if err := a.discordDo(req, out); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

func (a *app) discordDo(req *http.Request, out any) error {
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("status %d: %s", resp.StatusCode, snippet)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
