PORT   ?= 8080
PHOTOS ?= photos
IMG    ?= static/img

# Email settings and the SMTP password live in .env (git ignores it; see .env.example).
-include .env
export

.PHONY: run stop test build photos clean

run:    ## start the site at http://localhost:$(PORT)
	PORT=$(PORT) go run .

# Only stops a process named "cellar" (go run's binary or ./cellar), never another project's server on the port.
stop:   ## stop the site running on $(PORT)
	@pid=$$(ss -ltnpH "sport = :$(PORT)" | grep -oP '"cellar",pid=\K[0-9]+'); \
	if [ -n "$$pid" ]; then kill $$pid && echo "stopped The Cellar on port $(PORT)"; \
	else echo "The Cellar isn't running on port $(PORT)"; fi

test:   ## vet + run all tests
	go vet ./...
	go test ./...

build:  ## single self-contained binary ./cellar (Linux, ready to deploy)
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o cellar .

# Drop bottle photos (.jpg/.png) in photos/, named like the wine (e.g. chianti.jpg), then `make photos`.
# Photos are scaled to 1500px tall (cmd/cutout is tuned for that size), cmd/cutout removes the plain studio
# background, then ImageMagick softens the edge, trims, and saves two sizes:
# chianti.webp (700px tall: cards, shelf, cart) and chianti@2x.webp (1400px: detail page and quick look on sharp screens).
photos: ## photos/* -> static/img/*.webp + *@2x.webp (needs ImageMagick 7)
	@mkdir -p $(IMG)
	@for f in $(PHOTOS)/*.jpg $(PHOTOS)/*.jpeg $(PHOTOS)/*.png; do \
		[ -f "$$f" ] || continue; \
		out="$(IMG)/$$(basename "$${f%.*}")"; \
		tmp=$$(mktemp --suffix=.png); \
		magick "$$f" -colorspace sRGB -resize 'x1500>' "$$tmp" \
			&& go run ./cmd/cutout "$$tmp" "$$tmp" \
			&& magick "$$tmp" -channel A -morphology Erode Disk:1 -blur 0x0.6 +channel -trim +repage \
				\( +clone -resize 'x1400>' -quality 85 -write "$$out@2x.webp" +delete \) \
				-resize 'x700>' -quality 85 "$$out.webp" \
			&& echo "$$out.webp"; \
		rm -f "$$tmp"; \
	done

clean:
	rm -f cellar
