package main

import (
	"net/http"
	"net/url"
	"strconv"
)

const maxQty = 99

// cart maps wine ID to quantity. It lives in a cookie, so the server stores nothing.
// shortcut: cookies cap at ~4KB (~150 different wines); move to server-side sessions if that's ever hit.
type cart map[string]int

// readCart trusts nothing: the cookie is user-editable, so bad quantities are dropped
// and unknown IDs are filtered out later against the catalog.
func readCart(r *http.Request) cart {
	c := cart{}
	ck, err := r.Cookie("cart")
	if err != nil {
		return c
	}
	vals, _ := url.ParseQuery(ck.Value)
	for id := range vals {
		if n, err := strconv.Atoi(vals.Get(id)); err == nil && n > 0 && n <= maxQty {
			c[id] = n
		}
	}
	return c
}

func writeCart(w http.ResponseWriter, c cart) {
	vals := url.Values{}
	for id, n := range c {
		vals.Set(id, strconv.Itoa(n))
	}
	ck := &http.Cookie{Name: "cart", Value: vals.Encode(), Path: "/", MaxAge: 30 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if len(c) == 0 {
		ck.MaxAge = -1
	}
	http.SetCookie(w, ck)
}
