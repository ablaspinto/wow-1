package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Wine is one bottle. Prices are in cents so cart totals never pick up float rounding errors.
type Wine struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Producer    string  `json:"producer"`
	Grape       string  `json:"grape"`
	Style       string  `json:"style"`
	Country     string  `json:"country"`
	Region      string  `json:"region"`
	Vintage     string  `json:"vintage"` // "2019", "NV" for non-vintage, or "" if not known (then it's not shown)
	ABV         float64 `json:"abv"`
	PriceCents  int     `json:"price_cents"`
	Description string  `json:"description"` // a sentence or two, shown in the quick look and on the detail page
	Notes       string  `json:"notes"`
	Pairing     string  `json:"pairing"`
	Img         string  `json:"img"`        // optional file name under IMAGE_BASE_URL; empty draws an SVG bottle
	Credit      string  `json:"credit"`     // photo attribution, required when img is set
	CreditURL   string  `json:"credit_url"` // the photo's source page (e.g. on Wikimedia Commons)
	Featured    bool    `json:"featured"`
}

type Filter struct {
	Q        string
	Style    string
	Country  string
	MaxCents int
	Featured bool
}

// Catalog is the seam for swapping wines.json for a database later.
type Catalog interface {
	List(Filter) []Wine
	Get(id string) (Wine, bool)
}

type style struct{ Key, Label string }

var styles = []style{
	{"red", "Red"}, {"white", "White"}, {"rose", "Rosé"}, {"sparkling", "Sparkling"}, {"dessert", "Dessert"},
}

func styleLabel(key string) string {
	for _, s := range styles {
		if s.Key == key {
			return s.Label
		}
	}
	return ""
}

func money(cents int) string { return fmt.Sprintf("$%d.%02d", cents/100, cents%100) }

type jsonCatalog struct {
	wines []Wine
	byID  map[string]Wine
}

// IDs end up in URLs and CSS view-transition names, so keep them plain.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func loadCatalog(data []byte) (*jsonCatalog, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // catches typos like "price" instead of "price_cents"
	var wines []Wine
	if err := dec.Decode(&wines); err != nil {
		return nil, fmt.Errorf("wines.json: %w", err)
	}
	c := &jsonCatalog{wines: wines, byID: make(map[string]Wine, len(wines))}
	for i, w := range wines {
		_, dup := c.byID[w.ID]
		problem := ""
		switch {
		case !idPattern.MatchString(w.ID):
			problem = "id must be lowercase letters, digits and dashes, starting with a letter"
		case dup:
			problem = "duplicate id"
		case styleLabel(w.Style) == "":
			problem = "style must be one of red, white, rose, sparkling, dessert"
		case w.Name == "":
			problem = "missing name"
		case w.PriceCents <= 0:
			problem = "price_cents must be positive"
		case w.Img != "" && (w.Credit == "" || w.CreditURL == ""):
			problem = "a photo needs credit and credit_url (free licenses require attribution)"
		}
		if problem != "" {
			return nil, fmt.Errorf("wines.json entry %d (%q): %s", i+1, w.ID, problem)
		}
		c.byID[w.ID] = w
	}
	return c, nil
}

func (c *jsonCatalog) Get(id string) (Wine, bool) {
	w, ok := c.byID[id]
	return w, ok
}

func (c *jsonCatalog) List(f Filter) []Wine {
	q := strings.ToLower(f.Q)
	var out []Wine
	for _, w := range c.wines {
		switch {
		case f.Style != "" && w.Style != f.Style,
			f.Country != "" && w.Country != f.Country,
			f.MaxCents > 0 && w.PriceCents >= f.MaxCents,
			f.Featured && !w.Featured,
			q != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
				w.Name, w.Producer, w.Grape, w.Region, w.Country, w.Notes, w.Pairing}, " ")), q):
			continue
		}
		out = append(out, w)
	}
	return out
}
