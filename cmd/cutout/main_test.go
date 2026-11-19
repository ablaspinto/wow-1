package main

import (
	"image"
	"image/color"
	"testing"
)

// A dark bottle on white with a white label band that touches its outline, and a shadow on one side:
// the label must survive, the background and the shadow must go.
func TestCutoutKeepsWhiteLabel(t *testing.T) {
	white, red, grey := color.NRGBA{255, 255, 255, 255}, color.NRGBA{90, 10, 30, 255}, color.NRGBA{150, 150, 150, 255}
	img := image.NewNRGBA(image.Rect(0, 0, 40, 100))
	for y := range 100 {
		for x := range 40 {
			c := white
			neck := y >= 2 && y < 10 && x >= 16 && x < 24
			body := y >= 10 && y < 90 && x >= 10 && x < 30
			base := y >= 90 && y < 95 && x >= 12 && x < 28 // rounded base, a little narrower
			label := y >= 50 && y < 70
			if neck || base || (body && !label) {
				c = red
			}
			if y >= 82 && y < 90 && (x == 30 || x == 31) { // shadow beside the lower body
				c = grey
			}
			img.SetNRGBA(x, y, c)
		}
	}
	out := cutout(img)
	for _, tc := range []struct {
		x, y   int
		opaque bool
	}{
		{12, 60, true},  // label, right at the bottle's edge
		{20, 60, true},  // middle of the label
		{5, 60, false},  // background beside the label
		{20, 5, true},   // neck
		{12, 5, false},  // background beside the neck
		{10, 93, false}, // background beside the narrower base
		{20, 93, true},  // base
		{20, 98, false}, // background below the bottle
		{30, 85, false}, // one-sided shadow
		{29, 85, true},  // body next to it
	} {
		if got := out.NRGBAAt(tc.x, tc.y).A == 255; got != tc.opaque {
			t.Errorf("(%d,%d) opaque=%v, want %v", tc.x, tc.y, got, tc.opaque)
		}
	}
}
