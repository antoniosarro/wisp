// Package assets embeds static files shipped inside the wisp binary.
package assets

import "embed"

//go:embed logo.png
var Logo []byte

// Mascot holds mascot/<state>.png: a strip of square frames per state,
// exported from mascot/mascot.aseprite by `just sprites`. The strips are
// indexed on the mascot/wisp.gpl palette, so they decode as *image.Paletted.
//
//go:embed mascot/*.png
var Mascot embed.FS
