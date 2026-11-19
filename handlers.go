package main

import (
	"bytes"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
)

type app struct {
	catalog   Catalog
	pages     map[string]*template.Template
	partials  *template.Template
	emailHTML *template.Template
	emailText *texttemplate.Template
	site      string   // absolute base URL for links in emails, e.g. https://thecellar.example
	send      sendFunc // logMail unless SMTP is configured
	limits    *mailLimits
	ipHeader  string // header a proxy puts the visitor's IP in (CLIENT_IP_HEADER); empty uses the connection's address
}

// view is what layout.html sees; page-specific data sits in Data.
type view struct {
	Title string
	AgeOK bool
	Cart  badge
	Data  any
}

type badge struct {
	Count     int
	Bump, OOB bool
}

type line struct {
	Wine Wine
}

// cartView is the visitor's saved list of wines (internally still a "cart": a cookie of wine IDs).
type cartView struct {
	Lines []line
	Sent  bool
	Badge badge
	Email string // send form, kept when it needs fixing
	Error string
}

// emailView is the list email: the saved wines grouped by style.
type emailView struct {
	Site   string
	Groups []group
	Count  int
}

type group struct {
	Label string
	Lines []line
}

type price struct {
	Cents int
	Label string
}

var prices = []price{{2500, "Under $25"}, {4000, "Under $40"}, {6000, "Under $60"}}

func newApp(cat Catalog, imgBase, site string) (*app, error) {
	version := strconv.FormatInt(time.Now().Unix(), 36) // new on every deploy, so the year-long static cache never goes stale
	// ?v= too, so after `make photos` and a deploy browsers fetch the new file.
	img := func(w Wine) string { return imgBase + w.Img + "?v=" + version }
	funcs := template.FuncMap{
		"money": money,
		"label": styleLabel,
		"img":   img,
		"img2x": func(w Wine) string { return imgBase + hiRes(w.Img) + "?v=" + version },
		"v":     func() string { return version },
	}
	base, err := template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/partials.html")
	if err != nil {
		return nil, err
	}
	a := &app{catalog: cat, pages: map[string]*template.Template{}, partials: base, site: strings.TrimSuffix(site, "/"), send: logMail,
		limits: &mailLimits{sent: map[string][]time.Time{}}}
	for _, name := range []string{"index", "wines", "wine", "cart", "credits", "404"} {
		t, err := template.Must(base.Clone()).ParseFS(files, "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		a.pages[name] = t
	}

	// Emails need absolute links, including to images served from this site.
	mailFuncs := template.FuncMap{
		"money": money,
		"find":  findURL,
		"img": func(w Wine) string {
			u := img(w)
			if strings.HasPrefix(u, "/") {
				u = a.site + u
			}
			return u
		},
	}
	if a.emailHTML, err = template.New("email.html").Funcs(mailFuncs).ParseFS(files, "templates/email.html"); err != nil {
		return nil, err
	}
	if a.emailText, err = texttemplate.New("email.txt").Funcs(texttemplate.FuncMap(mailFuncs)).ParseFS(files, "templates/email.txt"); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *app) routes() http.Handler {
	static, _ := fs.Sub(files, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /wines", a.wines)
	mux.HandleFunc("GET /wines/{id}", a.wine)
	mux.HandleFunc("GET /wines/{id}/quick", a.quick)
	mux.HandleFunc("GET /credits", a.credits)
	mux.HandleFunc("GET /list", a.cart)
	mux.HandleFunc("POST /list/add", a.cartAdd)
	mux.HandleFunc("POST /list/remove", a.cartRemove)
	mux.HandleFunc("POST /list/send", a.checkout)
	mux.HandleFunc("POST /age", a.age)
	mux.HandleFunc("/", a.notFound)
	return secure(http.NewCrossOriginProtection().Handler(mux))
}

// secure adds browser safety headers and caps request bodies. No form here needs more than a few KB; without
// a cap any POST could make the server read megabytes (and spool multipart uploads to disk).
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Frame-Options", "DENY") // other sites can't frame the pages (clickjacking the forms)
		hd.Set("Content-Security-Policy", "frame-ancestors 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		h.ServeHTTP(w, r)
	})
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") { // no folder listings
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		h.ServeHTTP(w, r)
	})
}

func (a *app) index(w http.ResponseWriter, r *http.Request) {
	var shelf []Wine // one bottle per style for the back-bar in the hero
	for _, s := range styles {
		if ws := a.catalog.List(Filter{Style: s.Key}); len(ws) > 0 {
			shelf = append(shelf, ws[0])
		}
	}
	a.render(w, r, "index", "", struct {
		Shelf    []Wine
		Featured []Wine
		Styles   []style
	}{shelf, a.catalog.List(Filter{Featured: true}), styles})
}

func (a *app) wines(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	maxCents, _ := strconv.Atoi(q.Get("max"))
	f := Filter{Q: strings.TrimSpace(q.Get("q")), Style: q.Get("style"), Country: q.Get("country"), MaxCents: maxCents}
	data := struct {
		Filter    Filter
		Wines     []Wine
		Styles    []style
		Countries []string
		Prices    []price
	}{f, a.catalog.List(f), styles, a.countries(), prices}

	w.Header().Set("Vary", "HX-Request") // same URL returns a fragment or a full page
	if isHX(r) && r.Header.Get("HX-History-Restore-Request") != "true" {
		a.exec(w, a.partials, "grid", data, http.StatusOK)
		return
	}
	a.render(w, r, "wines", "Wines", data)
}

func (a *app) wine(w http.ResponseWriter, r *http.Request) {
	wine, ok := a.catalog.Get(r.PathValue("id"))
	if !ok {
		a.notFound(w, r)
		return
	}
	a.render(w, r, "wine", wine.Name, wine)
}

// quick is the htmx fragment for the quick-look dialog. Without JS the bottle links to the full page instead.
func (a *app) quick(w http.ResponseWriter, r *http.Request) {
	wine, ok := a.catalog.Get(r.PathValue("id"))
	if !ok {
		a.notFound(w, r)
		return
	}
	a.exec(w, a.partials, "quick", wine, http.StatusOK)
}

func (a *app) credits(w http.ResponseWriter, r *http.Request) {
	var photos []Wine
	for _, wine := range a.catalog.List(Filter{}) {
		if wine.Img != "" {
			photos = append(photos, wine)
		}
	}
	a.render(w, r, "credits", "Photo credits", photos)
}

func (a *app) cart(w http.ResponseWriter, r *http.Request) {
	cv := a.cartView(readCart(r))
	cv.Sent = r.URL.Query().Get("sent") == "1"
	a.render(w, r, "cart", "My list", cv)
}

func (a *app) cartAdd(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if _, ok := a.catalog.Get(id); !ok {
		http.Error(w, "unknown wine", http.StatusBadRequest)
		return
	}
	c := readCart(r)
	c[id] = 1 // a list of wines, not an order: no quantities
	writeCart(w, c)
	if !isHX(r) {
		http.Redirect(w, r, "/list", http.StatusSeeOther)
		return
	}
	b := a.cartView(c).Badge
	b.Bump = true
	a.exec(w, a.partials, "badge", b, http.StatusOK)
}

func (a *app) cartRemove(w http.ResponseWriter, r *http.Request) {
	c := readCart(r)
	delete(c, r.FormValue("id"))
	writeCart(w, c)
	if !isHX(r) {
		http.Redirect(w, r, "/list", http.StatusSeeOther)
		return
	}
	cv := a.cartView(c)
	cv.Badge.OOB = true
	a.exec(w, a.partials, "cart-body", cv, http.StatusOK)
}

// checkout emails the visitor their saved wines, with pairings and links to shops that stock each one,
// then empties the list. Nothing is sold here: no prices are charged and no order is placed.
// Anyone can type any address, so the email's content is fixed (nothing the visitor typed goes in it),
// sends are rate limited, and a hidden field catches simple bots. Other sites can't post this form
// (CrossOriginProtection in routes).
func (a *app) checkout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10) // the form is one field; refuse anything big
	cv := a.cartView(readCart(r))
	if len(cv.Lines) == 0 {
		http.Redirect(w, r, "/list", http.StatusSeeOther)
		return
	}
	if r.FormValue("website") != "" { // the hidden field: people never see it, form-filling bots do. Act as if it worked.
		writeCart(w, cart{})
		http.Redirect(w, r, "/list?sent=1", http.StatusSeeOther)
		return
	}
	cv.Email = strings.TrimSpace(r.FormValue("email"))
	addr, err := mail.ParseAddress(cv.Email)
	if err != nil || len(addr.Address) > 254 || strings.ContainsAny(addr.Address, "\r\n") {
		cv.Error = "Please enter a valid email address."
		a.exec(w, a.pages["cart"], "layout", a.view(r, "My list", cv), http.StatusUnprocessableEntity)
		return
	}
	if !a.limits.allow(a.clientIP(r), addr.Address, time.Now()) {
		cv.Error = "That's a lot of emails in a short time. Your list is saved, so please try again in an hour."
		a.exec(w, a.pages["cart"], "layout", a.view(r, "My list", cv), http.StatusTooManyRequests)
		return
	}
	if err := a.emailOrder(addr.Address, cv); err != nil {
		log.Printf("checkout email: %v", err)
		cv.Error = "We couldn't send your email just now. Your list is saved, so please try again in a moment."
		a.exec(w, a.pages["cart"], "layout", a.view(r, "My list", cv), http.StatusServiceUnavailable)
		return
	}
	writeCart(w, cart{})
	http.Redirect(w, r, "/list?sent=1", http.StatusSeeOther)
}

func (a *app) emailOrder(to string, cv cartView) error {
	ev := emailView{Site: a.site, Count: cv.Badge.Count}
	for _, s := range styles {
		g := group{Label: s.Label}
		for _, l := range cv.Lines {
			if l.Wine.Style == s.Key {
				g.Lines = append(g.Lines, l)
			}
		}
		if len(g.Lines) > 0 {
			ev.Groups = append(ev.Groups, g)
		}
	}
	var text, html bytes.Buffer
	if err := a.emailText.Execute(&text, ev); err != nil {
		return err
	}
	if err := a.emailHTML.Execute(&html, ev); err != nil {
		return err
	}
	return a.send(to, "Your wine list from The Cellar", text.String(), html.String())
}

// clientIP is the visitor's address, for rate limits. Behind a proxy (Render, Fly, Cloudflare) every request
// arrives from the proxy, so CLIENT_IP_HEADER names the header it puts the real address in; the last entry is
// the one the proxy added (earlier ones are whatever the visitor sent). IPv6 visitors usually own a whole
// /64, so they count as one.
func (a *app) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if v := r.Header.Get(a.ipHeader); a.ipHeader != "" && v != "" {
		host = strings.TrimSpace(v[strings.LastIndex(v, ",")+1:])
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String()
	}
	return host
}

// hiRes is the 1400px file `make photos` saves next to each photo: chianti.webp -> chianti@2x.webp.
func hiRes(img string) string {
	ext := path.Ext(img)
	return strings.TrimSuffix(img, ext) + "@2x" + ext
}

// findURL searches Wine-Searcher, which lists shops that stock a wine and their prices.
func findURL(w Wine) string {
	name, _, _ := strings.Cut(w.Name, " (") // "Sauternes (375 ml)" -> "Sauternes"
	u := "https://www.wine-searcher.com/find/" + url.QueryEscape(strings.ToLower(w.Producer+" "+name))
	if len(w.Vintage) == 4 {
		u += "/" + w.Vintage
	}
	return u
}

func (a *app) age(w http.ResponseWriter, r *http.Request) {
	ok := r.FormValue("ok") == "yes"
	if ok {
		http.SetCookie(w, &http.Cookie{Name: "age_ok", Value: "1", Path: "/", MaxAge: 365 * 24 * 3600,
			HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	if !isHX(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if ok {
		return // empty 200: htmx removes the gate once the doors have swung open
	}
	// Swap only the message, so the doors stay shut.
	w.Header().Set("HX-Retarget", "#gate-msg")
	w.Header().Set("HX-Reswap", "innerHTML")
	a.exec(w, a.partials, "gate-denied", nil, http.StatusOK)
}

func (a *app) notFound(w http.ResponseWriter, r *http.Request) {
	a.exec(w, a.pages["404"], "layout", a.view(r, "Not found", nil), http.StatusNotFound)
}

// cartView walks the catalog (not the map) so lines keep a stable order and IDs that no longer exist drop out.
func (a *app) cartView(c cart) cartView {
	var cv cartView
	for _, wine := range a.catalog.List(Filter{}) {
		if c[wine.ID] > 0 {
			cv.Lines = append(cv.Lines, line{wine})
			cv.Badge.Count++
		}
	}
	return cv
}

func (a *app) countries() []string {
	var out []string
	for _, w := range a.catalog.List(Filter{}) {
		if !slices.Contains(out, w.Country) {
			out = append(out, w.Country)
		}
	}
	slices.Sort(out)
	return out
}

func (a *app) view(r *http.Request, title string, data any) view {
	ck, err := r.Cookie("age_ok")
	return view{Title: title, AgeOK: err == nil && ck.Value == "1", Cart: a.cartView(readCart(r)).Badge, Data: data}
}

func (a *app) render(w http.ResponseWriter, r *http.Request, page, title string, data any) {
	a.exec(w, a.pages[page], "layout", a.view(r, title, data), http.StatusOK)
}

// exec renders into a buffer first so a template error never leaves a half-written page.
func (a *app) exec(w http.ResponseWriter, t *template.Template, name string, data any, status int) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }
