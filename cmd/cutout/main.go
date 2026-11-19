// Command cutout removes the plain studio background from a bottle photo and writes a PNG with
// transparency. `make photos` runs it, then resizes and converts the result to WebP.
//
//	go run ./cmd/cutout in.jpg out.png
//
// A flood fill from the edges finds the background. Because a bottle is one solid span per row and
// only gets wider from the cap down to the body, each row is then filled between its outer edges and
// never narrower than the row above. That keeps white labels that touch the bottle's outline, which a
// flood fill alone would eat.
package main

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"os"
	"slices"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cutout in.jpg out.png")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "cutout:", err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()
	img, _, err := image.Decode(in)
	if err != nil {
		return fmt.Errorf("%s: %w", inPath, err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := png.Encode(out, cutout(img)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func cutout(img image.Image) *image.NRGBA {
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	src := image.NewNRGBA(image.Rect(0, 0, W, H))
	draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	px := func(i int) []uint8 { return src.Pix[i*4 : i*4+4] }

	// Background colour = median of the border; tolerance follows how much the border itself varies
	// (gradients, noise), so clean white and grey-gradient backdrops both work.
	var border []int
	for x := range W {
		border = append(border, x, (H-1)*W+x)
	}
	for y := 1; y < H-1; y++ {
		border = append(border, y*W, y*W+W-1)
	}
	var bg [3]int
	vals := make([]int, len(border))
	for c := range 3 {
		for j, i := range border {
			vals[j] = int(px(i)[c])
		}
		slices.Sort(vals)
		bg[c] = vals[len(vals)/2]
	}
	dist := func(p []uint8) int { // summed RGB difference, 0..765
		if p[3] < 128 {
			return 0 // already transparent counts as background
		}
		return abs(int(p[0])-bg[0]) + abs(int(p[1])-bg[1]) + abs(int(p[2])-bg[2])
	}
	for j, i := range border {
		vals[j] = dist(px(i))
	}
	slices.Sort(vals)
	tol := min(max(vals[len(vals)*3/4]+40, 45), 170)

	// Flood fill the background from every border pixel that looks like it.
	isBG := make([]bool, W*H)
	var stack []int
	visit := func(i int) {
		if !isBG[i] && dist(px(i)) <= tol {
			isBG[i] = true
			stack = append(stack, i)
		}
	}
	for _, i := range border {
		visit(i)
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x := i % W; x > 0 {
			visit(i - 1)
		}
		if x := i % W; x < W-1 {
			visit(i + 1)
		}
		if i >= W {
			visit(i - W)
		}
		if i < W*(H-1) {
			visit(i + W)
		}
	}

	// Raw outer edges per row; first/last rows that contain any bottle.
	left, right := make([]int, H), make([]int, H)
	first, last := -1, -1
	for y := range H {
		left[y], right[y] = -1, -1
		for x := range W {
			if !isBG[y*W+x] {
				if left[y] < 0 {
					left[y] = x
				}
				right[y] = x
			}
		}
		if left[y] >= 0 {
			if first < 0 {
				first = y
			}
			last = y
		}
	}
	out := image.NewNRGBA(src.Rect)
	if first < 0 {
		return out // nothing but background
	}

	// A 5-row median removes one-off spikes (a stray reflection that escaped the fill).
	left, right = median5(left), median5(right)

	// A floor shadow shows up as rows wider than the body at the very bottom: drop them.
	var body []int
	for y := first + (last-first)/2; y <= last; y++ {
		if left[y] >= 0 {
			body = append(body, right[y]-left[y]+1)
		}
	}
	slices.Sort(body)
	for last > first && (left[last] < 0 || (right[last]-left[last]+1)*100 > body[len(body)/2]*106) {
		last--
	}

	envelope(left, right, first, last)

	// Trim the soft light halo a pale backdrop leaves around the glass: a light run at a row's edge
	// (up to a tenth of the row's width) that ends in glass. A label reaching the edge may get nicked
	// too, so the envelope runs again to restore those rows from their neighbours.
	// Then up to 2% of the height of light rows under the base (a reflection on the table).
	fringe := tol*3 + 60
	near := func(y, x int) bool { return dist(px(y*W+x)) <= fringe }
	for y := first; y <= last; y++ {
		maxRun := max(6, (right[y]-left[y])/10)
		k := 0
		for k < maxRun && left[y]+k < right[y] && near(y, left[y]+k) {
			k++
		}
		if k < maxRun {
			left[y] += k
		}
		k = 0
		for k < maxRun && right[y]-k > left[y] && near(y, right[y]-k) {
			k++
		}
		if k < maxRun {
			right[y] -= k
		}
	}
	envelope(left, right, first, last)

	// Bottles are round, so an upright bottle's outline mirrors itself around a vertical axis. Keeping the
	// narrower side of each row trims one-sided leftovers (a shadow beside the base, a halo on the lit side);
	// the envelope then restores rows a nick on one side made too narrow.
	var mids []int
	for y := first; y <= last; y++ {
		mids = append(mids, left[y]+right[y]) // twice the row's centre, so half pixels stay exact
	}
	slices.Sort(mids)
	c2 := mids[len(mids)/2]
	for y := first; y <= last; y++ {
		h2 := max(0, min(c2-2*left[y], 2*right[y]-c2)) // twice the half-width
		left[y], right[y] = (c2-h2+1)/2, (c2+h2)/2
	}
	envelope(left, right, first, last)

	for n := 0; n < H/50 && last > first && near(last, (left[last]+right[last])/2); n++ {
		last--
	}

	for y := first; y <= last; y++ {
		o := (y*W + left[y]) * 4
		n := (right[y] - left[y] + 1) * 4
		copy(out.Pix[o:o+n], src.Pix[o:o+n])
	}
	return out
}

// envelope makes each row span as far as the narrower of "widest above" and "widest below". That
// fills rows that were hollowed out (a white label), yet still lets the neck and the rounded base
// narrow, and a single odd row can't spread to the others.
func envelope(left, right []int, first, last int) {
	n := len(left)
	topL, topR := make([]int, n), make([]int, n)
	l, r := 1<<30, -1
	for y := first; y <= last; y++ {
		if left[y] >= 0 {
			l, r = min(l, left[y]), max(r, right[y])
		}
		topL[y], topR[y] = l, r
	}
	l, r = 1<<30, -1
	for y := last; y >= first; y-- {
		if left[y] >= 0 {
			l, r = min(l, left[y]), max(r, right[y])
		}
		left[y], right[y] = max(topL[y], l), min(topR[y], r)
	}
}

// median5 replaces each edge with the median of its row and two rows either side; -1 means "no bottle in this row".
func median5(v []int) []int {
	out := slices.Clone(v)
	for y := range v {
		if v[y] < 0 {
			continue
		}
		var w []int
		for d := -2; d <= 2; d++ {
			if y+d >= 0 && y+d < len(v) && v[y+d] >= 0 {
				w = append(w, v[y+d])
			}
		}
		slices.Sort(w)
		out[y] = w[len(w)/2]
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
