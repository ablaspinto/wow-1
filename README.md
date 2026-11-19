# The Cellar

A speakeasy-themed wine guide for the HSPT 161 project (Technology: E-Commerce Wine Storefront).
Nothing is sold: visitors save wines to a list and get it by email, with pairings and links to shops that stock each one.
It's built with Go (standard library only) and htmx, with one hand-written stylesheet. It stocks 13
real wines, with freely licensed photos from Wikimedia Commons.

```sh
make run     # http://localhost:8080   (PORT=3000 make run to change the port)
make stop    # stop it                 (PORT=3000 make stop for another port)
make test    # go vet + all tests
make build   # one self-contained Linux binary: ./cellar
make photos  # photos/*.jpg|png -> cut-out WebP files in static/img/ (two sizes each)
```

Templates, CSS, photos and `wines.json` are compiled into the binary, so restart `make run` after
editing them.

## What visitors see

- **The door:** a 21+ check styled as a speakeasy door that swings open.
- **Enter the cellar:** the home page button closes velvet stage curtains, which part on the wine list
  (Chrome, Edge and Safari; Firefox just follows the link).
- **Browse:** live filtering and search (htmx), and "My list" stored in a cookie (trash button to remove a wine).
- **Email me my list:** no sale, payment or order. The visitor enters their email and gets their wines, grouped by
  style, with region, grape, pairings, a "Where to buy" link (Wine-Searcher) and a link back to the wine.
- **Quick look:** click a bottle on any card. A dialog opens with a short description, tasting notes,
  pairing and price, and the bottle turns in 360°. Drag it to spin it yourself.
- **Detail page:** the same turning bottle, floating under a spotlight, with the photo credit below it.

The 360° view is made from a single photo: a bottle's outline doesn't change as it turns, so
`static/app.js` keeps the outline and slides the label around it as if wrapped on a cylinder. The far
side shows the label again, like a back label. A true 360° needs 24–36 turntable photos.

## Adding wines

Edit `wines.json`. If an entry is wrong (typo in a field name, duplicate id, unknown style, a photo
without a credit), the server refuses to start and tells you which entry. Notes:

- `id`: lowercase letters, digits and dashes. It's used in the URL.
- `style`: one of `red`, `white`, `rose`, `sparkling`, `dessert`.
- `vintage`: text: `"2019"`, `"NV"` for non-vintage, or `""` if unknown (it's then hidden).
- `price_cents`: `3800` means $38.00.
- `description`: a sentence or two, shown in the quick look and on the detail page.
- `img`: a photo's file name (see below), or empty to draw a bottle in the style's colors.
- `credit` and `credit_url`: required with a photo. Free licenses require attribution, and the
  `/credits` page lists them all.

Prices, ABV and tasting notes are typical figures written for this demo, not taken from the labels.

## The list email

Without settings nothing is sent: the email is printed in the terminal and the HTML version is saved to
`/tmp/cellar-last-email.html` so you can open it in a browser.

To send for real, copy `.env.example` to `.env` and fill it in (`make run` loads it; git ignores it).
On a host like Render, enter the same settings in its dashboard instead.

| Variable | What it is |
|---|---|
| `RESEND_API_KEY` | a Resend API key; sends over HTTPS instead of SMTP (use this on Render's free plan). Takes priority over `SMTP_HOST` |
| `SMTP_HOST` | the provider's server, e.g. `smtp.gmail.com`, `smtp.resend.com`, `email-smtp.us-east-1.amazonaws.com` |
| `SMTP_PORT` | `587` (the default) |
| `SMTP_USER`, `SMTP_PASS` | the provider's SMTP login (never commit these) |
| `MAIL_FROM` | `The Cellar <hello@yourdomain.com>`: an address the provider lets you send from |
| `SITE_URL` | where the site lives, e.g. `https://yourdomain.com`; links and images in the email point here. Required when sending email |
| `CLIENT_IP_HEADER` | only behind a proxy: the header holding the visitor's IP (Render: `X-Forwarded-For`, Fly.io: `Fly-Client-IP`, Cloudflare: `CF-Connecting-IP`). Without it, every visitor shares one rate limit |

Choosing a provider:

- **No domain yet (class demo):** a Gmail account made just for the site. Turn on 2-Step
  Verification, create an App Password (Google Account → Security → App passwords), and use
  `smtp.gmail.com`, the Gmail address as `SMTP_USER` and `MAIL_FROM`, and the app password as `SMTP_PASS`.
  Use a new account, not your personal one: the app password can read and send all of that account's mail.
- **With a domain:** Resend (free for 3,000 emails a month) or Amazon SES (pennies, if the site goes on
  AWS anyway). Both ask you to add a few DNS records (SPF and DKIM) for your domain; do it, or emails
  land in spam and anyone can fake mail from your domain. Also add a DMARC record (`v=DMARC1; p=none`
  to start). SES starts in a "sandbox" that only sends to addresses you've verified; request production
  access before launch.

What protects the form (anyone can type any address, so it must not be usable for spam):

- The email's content is fixed: nothing the visitor types goes into it except the address, which is
  validated (no line breaks, so no extra headers can be smuggled in).
- Rate limits: 3 emails an hour to one address, 5 an hour from one visitor, 100 a day for the whole
  site (under free-tier quotas). They're kept in memory, so they reset when the server restarts.
- A hidden field catches simple bots; other websites can't submit the form (cross-origin protection);
  oversized requests are refused.
- The connection to the mail server is always encrypted (TLS 1.2+, certificate checked), and a slow
  mail server can't hang the page (30 second limit).
- Email addresses aren't stored anywhere.

## Bottle photos

1. Put photos in `photos/`, named after the wine (e.g. `photos/chianti.jpg`). Straight-on shots on a
   plain, light background work best. The folder is in `.gitignore` because the originals are big,
   so keep them somewhere else; only the processed files in `static/img/` are needed to run the site.
2. Run `make photos`. It scales the photo to 1500px tall, `cmd/cutout` removes the background, and it
   saves `static/img/chianti.webp` (700px tall, for cards) and `chianti@2x.webp` (1400px, for the
   detail page and quick look on sharp screens). The cut-out handles white labels that touch the
   bottle's edge, rounded bases, light halos and shadows on one side (it keeps the outline
   symmetric, so the bottle must stand upright), but not busy backgrounds (a wooden shelf) or strong
   gradients. Check each result.
3. Set `"img"`, `"credit"` and `"credit_url"` on that wine and restart.

Only use images you're allowed to use, and credit the author:

- **Wikimedia Commons** (all photos here): search `<wine name> bottle`, check the license on the file page.
  Download the original (or the 500px+ thumbnail if Commons is rate-limiting you).
- **Open Food Facts** (openfoodfacts.org): crowd-sourced product photos, many wines, CC BY-SA. Quality
  varies; pick shots on a plain background.
- **The producer or importer:** most wineries have a press or trade page with studio bottle shots for
  shops. They aren't free licenses, so email and ask; that's how real wine shops get theirs.
- **Your own photos:** the best option for a real shop. Shoot on white paper, straight on, in soft light.

To serve images from S3 + CloudFront later, upload both sizes and set
`IMAGE_BASE_URL=https://<your-cloudfront-domain>/` (it defaults to `/static/img/`). You don't need
to change any code, but CloudFront must send an `Access-Control-Allow-Origin` header for your site
(set it in a CloudFront response-headers policy). The glint uses each photo as a CSS mask, and the
360° view reads its pixels. Browsers only allow either for images from another domain when that
header is present. Without it, photos still show, just without those two effects.

## Where things live

| File | What it does |
|---|---|
| `main.go` | Startup and config (`PORT`, `IMAGE_BASE_URL`) |
| `catalog.go` | `Wine` type, `Catalog` interface, the `wines.json` implementation |
| `handlers.go` | Routes and pages; htmx requests get HTML fragments back |
| `cart.go` | The visitor's list ("cart" in the code) stored in a cookie, nothing kept on the server |
| `mail.go` | Sends the list email over SMTP (or logs it in development), and its rate limits |
| `templates/` | `layout.html`, one file per page, `partials.html` for shared pieces (cards, quick look, door), `email.html` and `email.txt` |
| `static/` | `style.css`, `app.js` (quick look, 360° turn, tilt, hero lamp), `htmx.min.js` (2.0.4), `img/` |
| `photos/` | Original photos; `make photos` turns them into `static/img/` |
| `cmd/cutout/` | The background remover used by `make photos` |

To move to a database later, write a type with `List(Filter)` and `Get(id)` methods and pass it to
`newApp` in place of the JSON catalog.

## Deploying

Any host that runs a Go binary works. For example, on Render: build command `go build -o cellar .`,
start command `./cellar`. The host sets `PORT`. Render's free plan blocks SMTP ports (25, 465, 587), so
use `RESEND_API_KEY` there; it sends over HTTPS. Free instances sleep after 15 idle minutes, and the
first visit after that takes about a minute.
