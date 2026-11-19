package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) *app {
	t.Helper()
	data, err := files.ReadFile("wines.json")
	if err != nil {
		t.Fatal(err)
	}
	cat, err := loadCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newApp(cat, "/static/img/", "http://localhost/")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func testApp(t *testing.T) http.Handler { return newTestApp(t).routes() }

func TestPhotoBottle(t *testing.T) {
	var b strings.Builder
	if err := newTestApp(t).partials.ExecuteTemplate(&b, "bottle", Wine{ID: "x", Img: "x.webp"}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `src="/static/img/x.webp?v=`) || !strings.Contains(out, "--mask: url(") || strings.Contains(out, "ZgotmplZ") {
		t.Fatalf("photo bottle rendered wrong:\n%s", out)
	}
}

func TestAgeGate(t *testing.T) {
	h := testApp(t)
	post := func(ok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/age", strings.NewReader("ok="+ok))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		return do(h, req)
	}
	if rec := post("no"); rec.Header().Get("HX-Retarget") != "#gate-msg" || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("under 21: want message swap and no cookie, got %v %v", rec.Header(), rec.Result().Cookies())
	}
	if rec := post("yes"); rec.Code != 200 || len(rec.Result().Cookies()) != 1 || rec.Result().Cookies()[0].Name != "age_ok" {
		t.Fatalf("21+: want age_ok cookie, got %d %v", rec.Code, rec.Result().Cookies())
	}
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMoney(t *testing.T) {
	for cents, want := range map[int]string{3800: "$38.00", 1999: "$19.99", 5: "$0.05"} {
		if got := money(cents); got != want {
			t.Errorf("money(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestLoadCatalogRejectsBadData(t *testing.T) {
	for name, js := range map[string]string{
		"duplicate id": `[{"id":"a","name":"A","style":"red","price_cents":1},{"id":"a","name":"B","style":"red","price_cents":1}]`,
		"bad style":    `[{"id":"a","name":"A","style":"blue","price_cents":1}]`,
		"typo field":   `[{"id":"a","name":"A","style":"red","price":10}]`,
		"bad id":       `[{"id":"Bad Id","name":"A","style":"red","price_cents":1}]`,
		"no price":     `[{"id":"a","name":"A","style":"red"}]`,
		"no credit":    `[{"id":"a","name":"A","style":"red","price_cents":1,"img":"a.webp"}]`,
	} {
		if _, err := loadCatalog([]byte(js)); err == nil {
			t.Errorf("%s: want error, got nil", name)
		}
	}
}

func TestReadCartIgnoresJunk(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "cart", Value: "a=2&b=x&c=-1&d=500"})
	if got := readCart(r); len(got) != 1 || got["a"] != 2 {
		t.Fatalf("readCart = %v, want map[a:2]", got)
	}
}

func TestPages(t *testing.T) {
	h := testApp(t)
	for path, want := range map[string]int{
		"/": 200, "/wines": 200, "/wines?style=red&max=4000&q=cherry": 200,
		"/wines/trapiche-oak-cask-malbec": 200, "/wines/nope": 404,
		"/wines/trapiche-oak-cask-malbec/quick": 200, "/wines/nope/quick": 404,
		"/credits": 200, "/list": 200, "/static/style.css": 200, "/nope": 404,
	} {
		rec := do(h, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), "ZgotmplZ") { // html/template's marker for a value it refused to insert
			t.Errorf("GET %s: template emitted ZgotmplZ", path)
		}
	}
}

func TestShopFlow(t *testing.T) {
	h := testApp(t)

	// htmx filter returns just the grid fragment.
	req := httptest.NewRequest("GET", "/wines?style=sparkling", nil)
	req.Header.Set("HX-Request", "true")
	body := do(h, req).Body.String()
	if strings.Contains(body, "<html") || !strings.Contains(body, "Ultra D Extra Brut") || strings.Contains(body, "Bodegas Beronia") {
		t.Fatalf("filter fragment wrong:\n%s", body)
	}

	// Quick look is a fragment with the spinner stage, and no view-transition-name (the card already has it).
	req = httptest.NewRequest("GET", "/wines/devaux-ultra-d/quick", nil)
	req.Header.Set("HX-Request", "true")
	body = do(h, req).Body.String()
	if strings.Contains(body, "<html") || !strings.Contains(body, "spin-stage") || !strings.Contains(body, "Côte des Bar") ||
		strings.Contains(body, "view-transition-name") || !strings.Contains(body, "devaux-ultra-d@2x.webp?v=") {
		t.Fatalf("quick look wrong:\n%s", body)
	}

	// Add the same wine twice: it's a list, not an order, so it appears once with no quantity or total.
	var cookie *http.Cookie
	for range 2 {
		req = httptest.NewRequest("POST", "/list/add", strings.NewReader("id=trapiche-oak-cask-malbec"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := do(h, req)
		if rec.Code != 200 {
			t.Fatalf("add = %d", rec.Code)
		}
		cookie = rec.Result().Cookies()[0]
	}
	req = httptest.NewRequest("GET", "/list", nil)
	req.AddCookie(cookie)
	body = do(h, req).Body.String()
	if strings.Count(body, "/wines/trapiche-oak-cask-malbec\"") != 1 || strings.Contains(body, "$24.00") || strings.Contains(body, "Checkout") ||
		!strings.Contains(body, "Email me my list") || !strings.Contains(body, `<span id="cart-badge" class="badge">1</span>`) {
		t.Fatalf("list should hold the wine once, no totals or checkout:\n%s", body)
	}

	// Remove empties the list.
	req = httptest.NewRequest("POST", "/list/remove", strings.NewReader("id=trapiche-oak-cask-malbec"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookie)
	if rec := do(h, req); rec.Code != 200 || rec.Result().Cookies()[0].MaxAge != -1 || !strings.Contains(rec.Body.String(), "Your list is empty") {
		t.Fatalf("remove = %d, want an emptied list", rec.Code)
	}

	// Unknown wine is rejected.
	req = httptest.NewRequest("POST", "/list/add", strings.NewReader("id=fake"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := do(h, req); rec.Code != 400 {
		t.Fatalf("add unknown = %d, want 400", rec.Code)
	}
}

func TestCheckoutEmail(t *testing.T) {
	a := newTestApp(t)
	var to, text, html string
	var fail error
	a.send = func(t, _, tx, h string) error { to, text, html = t, tx, h; return fail }
	h := a.routes()
	post := func(email string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/list/send", strings.NewReader("email="+email))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "cart", Value: "trapiche-oak-cask-malbec=2&chateau-guiraud-1998=1"})
		return do(h, req)
	}

	if rec := post("not-an-email"); rec.Code != 422 || to != "" || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), "valid email") {
		t.Fatalf("bad address: want 422, no email, cart kept; got %d to=%q", rec.Code, to)
	}

	fail = errors.New("smtp down")
	if rec := post("ann%40example.com"); rec.Code != 503 || len(rec.Result().Cookies()) != 0 || !strings.Contains(rec.Body.String(), `value="ann@example.com"`) {
		t.Fatalf("send failure: want 503 with cart kept and email refilled; got %d", rec.Code)
	}

	fail = nil
	rec := post("Ann+%3Cann%40example.com%3E")
	if rec.Code != 303 || rec.Header().Get("Location") != "/list?sent=1" || rec.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("checkout: want redirect and cleared cart; got %d %v", rec.Code, rec.Header())
	}
	if to != "ann@example.com" {
		t.Errorf("sent to %q, want the bare address", to)
	}
	for _, bad := range []string{"Total", "your order", "Checkout", "×"} {
		if strings.Contains(text, bad) || strings.Contains(html, bad) {
			t.Errorf("email reads like a sale: contains %q", bad)
		}
	}
	for _, want := range []string{"== Red ==", "== Dessert ==", "Typical price: $12.00", "2 wines you saved", "Pairs with: ",
		"https://www.wine-searcher.com/find/trapiche+oak+cask+malbec", "http://localhost/wines/chateau-guiraud-1998"} {
		if !strings.Contains(text, want) {
			t.Errorf("text email missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(html, `src="http://localhost/static/img/trapiche-oak-cask-malbec.webp?v=`) || strings.Index(html, "Oak Cask") > strings.Index(html, "Sauternes") {
		t.Errorf("html email wants absolute image URLs and reds before dessert:\n%s", html)
	}
}

func TestFindURL(t *testing.T) {
	got := findURL(Wine{Producer: "Château Guiraud", Name: "Sauternes (375 ml)", Vintage: "1998"})
	if want := "https://www.wine-searcher.com/find/ch%C3%A2teau+guiraud+sauternes/1998"; got != want {
		t.Fatalf("findURL = %q, want %q", got, want)
	}
}

func TestBuildMessageHeaders(t *testing.T) {
	msg, err := buildMessage("The Cellar <shop@example.com>", "ann@example.com", "Your bottles · Santé", "hi", "<p>hi</p>")
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(msg)))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if m.Header.Get("To") != "<ann@example.com>" || subj != "Your bottles · Santé" || !strings.HasPrefix(m.Header.Get("Content-Type"), "multipart/alternative") {
		t.Fatalf("headers wrong: %v (subject %q)", m.Header, subj)
	}
}

// Every photo in the catalog needs both sizes `make photos` writes, or sharp screens get a broken image.
func TestPhotosExist(t *testing.T) {
	for _, w := range newTestApp(t).catalog.List(Filter{}) {
		if w.Img == "" {
			continue
		}
		for _, name := range []string{w.Img, hiRes(w.Img)} {
			if _, err := fs.Stat(files, "static/img/"+name); err != nil {
				t.Errorf("%s: %v", w.ID, err)
			}
		}
	}
}

func TestMailLimits(t *testing.T) {
	l := &mailLimits{sent: map[string][]time.Time{}}
	now := time.Now()
	for i := range 3 {
		if !l.allow("1.1.1.1", "Ann@example.com", now) {
			t.Fatalf("send %d to ann refused", i+1)
		}
	}
	if l.allow("2.2.2.2", "ann@EXAMPLE.com", now) {
		t.Fatal("4th email to the same address within an hour (any case, any visitor) was allowed")
	}
	if !l.allow("2.2.2.2", "bob@example.com", now) || !l.allow("1.1.1.1", "cat@example.com", now) || !l.allow("1.1.1.1", "eve@example.com", now) {
		t.Fatal("other addresses should still go through")
	}
	if l.allow("1.1.1.1", "dan@example.com", now) {
		t.Fatal("6th email from one visitor within an hour was allowed")
	}
	if !l.allow("1.1.1.1", "ann@example.com", now.Add(time.Hour)) {
		t.Fatal("limits should reset after an hour")
	}
	// Different visitors and addresses: only the whole-site cap of 100 a day stops them.
	for i := 0; l.allow("v"+strconv.Itoa(i), "guest"+strconv.Itoa(i)+"@example.com", now.Add(2*time.Hour)); i++ {
		if i > 500 {
			t.Fatal("no daily cap for the whole site")
		}
	}
	if n := len(l.sent["all"]); n != 100 {
		t.Fatalf("whole site sent %d in a day, want the cap of 100", n)
	}
}

func TestCheckoutBotTrapAndLimit(t *testing.T) {
	a := newTestApp(t)
	sent := 0
	a.send = func(_, _, _, _ string) error { sent++; return nil }
	h := a.routes()
	post := func(form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/list/send", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "cart", Value: "beronia-rioja=1"})
		return do(h, req)
	}
	if rec := post("email=ann%40example.com&website=http%3A%2F%2Fspam"); rec.Code != 303 || sent != 0 {
		t.Fatalf("bot trap: want a fake success and no email, got %d, %d sent", rec.Code, sent)
	}
	if rec := post("email=" + strings.Repeat("a", 250) + "%40example.com"); rec.Code != 422 || sent != 0 {
		t.Fatalf("overlong address: want 422, got %d", rec.Code)
	}
	for range 3 {
		post("email=ann%40example.com")
	}
	if rec := post("email=ann%40example.com"); rec.Code != 429 || sent != 3 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("4th email to ann: want 429 with cart kept, got %d (%d sent)", rec.Code, sent)
	}
}

func TestClientIP(t *testing.T) {
	a := &app{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := a.clientIP(r); got != "10.0.0.1" {
		t.Errorf("no CLIENT_IP_HEADER: got %q, want the connection's address", got)
	}
	a.ipHeader = "X-Forwarded-For"
	if got := a.clientIP(r); got != "203.0.113.9" {
		t.Errorf("behind a proxy: got %q, want the entry the proxy added (last)", got)
	}
	r.Header.Set("X-Forwarded-For", "2001:db8:1:2:aaaa::1")
	if got := a.clientIP(r); got != "2001:db8:1:2::" {
		t.Errorf("IPv6: got %q, want its /64", got)
	}
}

func TestSecurityBasics(t *testing.T) {
	h := newTestApp(t).routes()

	rec := do(h, httptest.NewRequest("GET", "/", nil))
	for k, want := range map[string]string{"X-Frame-Options": "DENY", "Content-Security-Policy": "frame-ancestors 'none'", "X-Content-Type-Options": "nosniff"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	for _, p := range []string{"/static/", "/static/img/"} {
		if rec := do(h, httptest.NewRequest("GET", p, nil)); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404 (no folder listings)", p, rec.Code)
		}
	}

	// A huge body is cut off at the cap: the form can't be read, so nothing is added.
	big := "id=beronia-rioja&junk=" + strings.Repeat("a", 100<<10)
	req := httptest.NewRequest("POST", "/list/add", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := do(h, req); rec.Code != 400 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("100KB body to /list/add: want 400 and no cookie, got %d", rec.Code)
	}
}

func TestResendSender(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		if got["subject"] == "fail" {
			http.Error(w, `{"message":"bad"}`, 422)
		}
	}))
	defer srv.Close()
	send, err := resendSender(srv.URL, "re_key", "The Cellar <hello@wowthecellar.xyz>")
	if err != nil {
		t.Fatal(err)
	}
	if err := send("a@b.co", "Hi", "text", "<p>html</p>"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_key" || got["from"] != "The Cellar <hello@wowthecellar.xyz>" || got["to"].([]any)[0] != "a@b.co" || got["html"] != "<p>html</p>" {
		t.Fatalf("auth=%q body=%v", auth, got)
	}
	if err := send("a@b.co", "fail", "", ""); err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatalf("want 422 error, got %v", err)
	}
	if _, err := resendSender(srv.URL, "k", "not an address"); err == nil {
		t.Fatal("bad MAIL_FROM accepted")
	}
}
