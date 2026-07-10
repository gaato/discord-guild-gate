package main

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"
)

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
  body { font-family: sans-serif; background: #faf6f0; color: #333;
         display: flex; justify-content: center; align-items: center;
         min-height: 100vh; margin: 0; }
  .card { background: #fff; border-radius: 12px; padding: 2rem;
          box-shadow: 0 2px 12px rgba(0,0,0,.08); max-width: 24rem;
          width: calc(100% - 2rem); text-align: center; }
  h1 { font-size: 1.2rem; margin: 0 0 1rem; }
  p { font-size: .9rem; line-height: 1.6; }
  .btn { display: block; width: 100%; box-sizing: border-box;
         margin: .5rem 0; padding: .8rem; font-size: 1rem;
         border: none; border-radius: 8px; cursor: pointer;
         background: {{.Accent}}; color: #fff; }
  .btn:hover { filter: brightness(.92); }
  .btn.admin { background: #888; }
  .foot { margin-top: 1.2rem; font-size: .8rem; }
  .foot a { color: {{.Accent}}; }
</style>
</head>
<body>
<div class="card">
{{.Body}}
</div>
</body>
</html>`))

var pickBodyTmpl = template.Must(template.New("pick").Parse(`
<h1>{{if .JA}}アクセス先を選択してください{{else}}Select which one to access{{end}}</h1>
{{if .Name}}<p>{{if .JA}}ようこそ、{{.Name}} さん{{else}}Welcome, {{.Name}}{{end}}</p>{{end}}
{{range .Groups}}
<form method="post" action="/pick">
  <input type="hidden" name="group" value="{{.Label}}">
  <input type="hidden" name="next" value="{{$.Next}}">
  <button class="btn" type="submit">{{.Display}}</button>
</form>
{{end}}
{{if .Admin}}
<form method="post" action="/pick">
  <input type="hidden" name="group" value="admin">
  <input type="hidden" name="next" value="{{$.Next}}">
  <button class="btn admin" type="submit">admin</button>
</form>
{{end}}
<div class="foot">
  {{if .JA}}<a href="/pick?lang=en&next={{$.Next}}">English</a>{{else}}<a href="/pick?lang=ja&next={{$.Next}}">日本語</a>{{end}}
  ・ <a href="/auth/logout">{{if .JA}}ログアウト{{else}}Log out{{end}}</a>
</div>`))

type pageData struct {
	Lang   string
	Title  string
	Accent template.CSS
	Body   template.HTML
}

func (a *app) renderPage(w http.ResponseWriter, status int, lang, title string, body template.HTML) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTmpl.Execute(w, pageData{
		Lang:   lang,
		Title:  title,
		Accent: template.CSS(a.cfg.accent), // validated as a hex color at startup
		Body:   body,
	})
}

func (a *app) title(lang string) string {
	if lang == "ja" {
		return a.cfg.titleJA
	}
	return a.cfg.titleEN
}

// groupDisplay renders the picker button text for a group label.
func (a *app) groupDisplay(lang, label string) string {
	tmpl := a.cfg.groupNameEN
	if lang == "ja" {
		tmpl = a.cfg.groupNameJA
	}
	return strings.ReplaceAll(tmpl, "{label}", label)
}

func (a *app) pickPage(w http.ResponseWriter, s *session, next string) {
	type group struct{ Label, Display string }
	groups := make([]group, 0, len(s.Groups))
	for _, g := range s.Groups {
		groups = append(groups, group{Label: g, Display: a.groupDisplay(s.Lang, g)})
	}
	var buf bytes.Buffer
	_ = pickBodyTmpl.Execute(&buf, map[string]any{
		"JA":     s.Lang == "ja",
		"Name":   s.Name,
		"Groups": groups,
		"Admin":  s.Admin,
		"Next":   next,
	})
	title := "選択 | " + a.cfg.titleJA
	if s.Lang != "ja" {
		title = "Select | " + a.cfg.titleEN
	}
	a.renderPage(w, http.StatusOK, s.Lang, title, template.HTML(buf.String()))
}

func (a *app) errorPage(w http.ResponseWriter, status int, ja, en string) {
	heading := a.cfg.titleJA
	if a.cfg.titleEN != a.cfg.titleJA {
		heading += " / " + a.cfg.titleEN
	}
	body := template.HTML(
		`<h1>` + template.HTMLEscapeString(heading) + `</h1><p>` +
			template.HTMLEscapeString(ja) + `</p><p>` +
			template.HTMLEscapeString(en) + `</p>` +
			`<div class="foot"><a href="/auth/login">ログイン / Log in</a></div>`)
	a.renderPage(w, status, "ja", heading, body)
}

func (a *app) logoutPage(w http.ResponseWriter) {
	body := template.HTML(
		`<h1>ログアウトしました / Logged out</h1>` +
			`<div class="foot"><a href="/auth/login">ログイン / Log in</a></div>`)
	a.renderPage(w, http.StatusOK, "ja", "ログアウト | "+a.title("ja"), body)
}
