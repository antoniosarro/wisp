package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// Answers end in BEL or ST and may arrive in pieces; the device attributes
// reply marks the end.
func TestParseTermColors(t *testing.T) {
	answers := "\x1b]10;rgb:ffff/eeee/dddd\x1b\\\x1b]11;rgb:1111/2222/3333\x07" +
		"\x1b]4;3;rgb:ffff/cccc/0000\x1b\\\x1b]4;8;rgb:8080/8080/8080\x07"
	got, done := parseTermColors(answers)
	if done {
		t.Fatal("done before the device attributes reply")
	}
	if got.fg != (color.RGBA{0xff, 0xee, 0xdd, 0xff}) || got.bg != (color.RGBA{0x11, 0x22, 0x33, 0xff}) {
		t.Errorf("fg %v bg %v", got.fg, got.bg)
	}
	if got.ansi[3] != (color.RGBA{0xff, 0xcc, 0x00, 0xff}) || got.ansi[8] != (color.RGBA{0x80, 0x80, 0x80, 0xff}) {
		t.Errorf("ansi 3 %v, 8 %v", got.ansi[3], got.ansi[8])
	}
	if got.ansi[1] != nil {
		t.Errorf("unanswered ansi 1 = %v", got.ansi[1])
	}
	if _, done := parseTermColors(answers + "\x1b[?62;22c"); !done {
		t.Error("not done after the device attributes reply")
	}
	if got, _ := parseTermColors("\x1b]10;rgb:ffff/ee"); got.fg != nil {
		t.Errorf("partial answer parsed as %v", got.fg)
	}
}

// Every role takes its terminal color; unanswered roles and the
// transparent entry keep theirs.
func TestMascotPaletteFollowsTerminal(t *testing.T) {
	orig := make(color.Palette, 11)
	for i := range orig {
		orig[i] = color.RGBA{uint8(i), 0, 0, 0xff}
	}
	orig[10] = color.RGBA{}
	fg, bg := color.RGBA{200, 200, 200, 0xff}, color.RGBA{0, 0, 100, 0xff}
	tc := termColors{fg: fg, bg: bg}
	tc.ansi[3] = color.RGBA{1, 2, 3, 0xff}
	p := tc.mascotPalette(orig)
	want := map[int]color.Color{
		0: bg, 1: fg, 2: color.RGBA{140, 140, 170, 0xff}, 9: tc.ansi[3],
		3: orig[3], 8: orig[8], 10: orig[10],
	}
	for i, c := range want {
		if p[i] != c {
			t.Errorf("entry %d = %v, want %v", i, p[i], c)
		}
	}
	if orig[1] != (color.RGBA{1, 0, 0, 0xff}) {
		t.Error("the strip's own palette was modified")
	}
}

// The uploaded frames carry the recolored palette.
func TestUploadMascotRecolors(t *testing.T) {
	fg := color.RGBA{10, 200, 30, 0xff}
	var b strings.Builder
	uploadMascot(&b, termColors{fg: fg})
	var enc strings.Builder
	for _, chunk := range strings.Split(b.String(), "\x1b_G")[1:] {
		payload := chunk[strings.IndexByte(chunk, ';')+1 : strings.Index(chunk, "\x1b\\")]
		enc.WriteString(payload)
		if strings.Contains(chunk[:strings.IndexByte(chunk, ';')], "m=0") {
			break
		}
	}
	data, err := base64.StdEncoding.DecodeString(enc.String())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := img.(*image.Paletted)
	if !ok {
		t.Fatalf("frame decodes as %T, want *image.Paletted", img)
	}
	if color.RGBAModel.Convert(p.Palette[1]) != fg {
		t.Errorf("body = %v, want %v", p.Palette[1], fg)
	}
}
