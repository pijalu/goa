// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"

	_ "image/gif"  // register GIF decoder
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder (png.Encode is used below)
)

// Image attachment limits. A vision API rejects images that are too large in
// bytes (Anthropic ~5 MB) or too wide/tall (8000 px edge), so both are clamped
// before encoding. MaxImageBytes is the decoded budget; the Base64 that goes on
// the wire is ~4/3 of it.
const (
	MaxImageBytes     = 4 << 20 // 4 MiB decoded
	MaxImageDimension = 8000    // largest accepted edge in pixels
)

// ImageUnavailablePlaceholder replaces an image content block whose source
// cannot be read. It keeps the request well-formed (no empty Base64 part) and
// makes the loss visible in the transcript instead of silently dropping it.
const ImageUnavailablePlaceholder = "(image unavailable: the attachment could not be read)"

// EncodedImage is a resolved image attachment: Base64 payload plus MIME type,
// exactly what every vision API expects on the wire.
type EncodedImage struct {
	Base64   string
	MimeType string
}

// DataURL renders the attachment as a data: URL.
func (e EncodedImage) DataURL() string {
	if e.MimeType == "" || e.Base64 == "" {
		return ""
	}
	return "data:" + e.MimeType + ";base64," + e.Base64
}

// imageMagic identifies the accepted image formats by leading bytes. A declared
// MIME type or filename extension is never trusted.
var imageMagic = []struct {
	mime   string
	sig    string
	offset int
}{
	{mime: "image/png", sig: "\x89PNG\r\n\x1a\n"},
	{mime: "image/jpeg", sig: "\xff\xd8\xff"},
	{mime: "image/gif", sig: "GIF87a"},
	{mime: "image/gif", sig: "GIF89a"},
	{mime: "image/webp", sig: "WEBP", offset: 8},
}

// SniffImageMime returns the MIME type of data, or "" when the bytes are not a
// recognisable image.
func SniffImageMime(data []byte) string {
	for _, m := range imageMagic {
		sig := []byte(m.sig)
		end := m.offset + len(sig)
		if len(data) >= end && string(data[m.offset:end]) == m.sig {
			return m.mime
		}
	}
	return ""
}

// EncodeImage resolves an image content source into Base64 bytes plus MIME type.
// src is either a filesystem path or a data: URL. ok is false when the source
// cannot be read or is not a recognisable image; callers must then emit
// ImageUnavailablePlaceholder rather than an empty image block.
func EncodeImage(src string) (EncodedImage, bool) {
	if src == "" {
		return EncodedImage{}, false
	}
	if strings.HasPrefix(src, "data:") {
		return decodeDataURL(src)
	}
	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return EncodedImage{}, false
	}
	mime := SniffImageMime(data)
	if mime == "" {
		return EncodedImage{}, false
	}
	data, mime = FitImage(data, mime)
	return EncodedImage{Base64: base64.StdEncoding.EncodeToString(data), MimeType: mime}, true
}

// ImageToDataURL returns a data: URL for src, or "" when src cannot be resolved.
func ImageToDataURL(src string) string {
	enc, ok := EncodeImage(src)
	if !ok {
		return ""
	}
	return enc.DataURL()
}

// decodeDataURL parses a data:<mime>;base64,<payload> URL without re-encoding.
func decodeDataURL(src string) (EncodedImage, bool) {
	rest := strings.TrimPrefix(src, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return EncodedImage{}, false
	}
	meta, payload := rest[:comma], rest[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return EncodedImage{}, false
	}
	mime := strings.TrimSuffix(meta, ";base64")
	if mime == "" || payload == "" {
		return EncodedImage{}, false
	}
	return EncodedImage{Base64: payload, MimeType: mime}, true
}

// FitImage downscales and re-encodes data when it exceeds the byte or dimension
// limits, using only the standard library. Undecodable formats (WebP without a
// registered decoder) are returned unchanged — the provider surfaces its own
// error rather than the image being dropped here.
func FitImage(data []byte, mime string) ([]byte, string) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return data, mime
	}
	if cfg.Width <= MaxImageDimension && cfg.Height <= MaxImageDimension && len(data) <= MaxImageBytes {
		return data, mime
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime
	}
	targetW, targetH := fitDimensions(cfg.Width, cfg.Height, len(data))
	resized := downscale(img, targetW, targetH)
	out := encodePNG(resized)
	// A byte-driven target can still miss for already-compressed sources; halve
	// until it fits or the image degenerates to a single pixel.
	for len(out) > MaxImageBytes && targetW > 1 && targetH > 1 {
		targetW /= 2
		targetH /= 2
		out = encodePNG(downscale(img, targetW, targetH))
	}
	if len(out) == 0 {
		return data, mime
	}
	return out, "image/png"
}

// fitDimensions returns the largest dimensions within both limits. The byte
// limit is treated as an area ratio, which is a good enough estimate for the
// uncompressed-ish screenshots this guards against.
func fitDimensions(w, h, byteLen int) (int, int) {
	scale := 1.0
	if w > MaxImageDimension {
		scale = minFloat(scale, float64(MaxImageDimension)/float64(w))
	}
	if h > MaxImageDimension {
		scale = minFloat(scale, float64(MaxImageDimension)/float64(h))
	}
	if byteLen > MaxImageBytes {
		ratio := float64(MaxImageBytes) / float64(byteLen)
		scale = minFloat(scale, math.Sqrt(ratio))
	}
	tw := int(float64(w) * scale)
	th := int(float64(h) * scale)
	return maxInt(tw, 1), maxInt(th, 1)
}

// downscale resamples src to targetW×targetH with a box (area-average) filter.
// Quality is adequate for the large reduction factors this is used for, and it
// keeps the image path free of third-party dependencies.
func downscale(src image.Image, targetW, targetH int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		y0, y1 := pixelSpan(y, targetH, sh, b.Min.Y)
		for x := 0; x < targetW; x++ {
			x0, x1 := pixelSpan(x, targetW, sw, b.Min.X)
			dst.Set(x, y, boxAverage(src, x0, y0, x1, y1))
		}
	}
	return dst
}

// pixelSpan returns the source range [lo,hi) whose pixels contribute to
// destination index i, widened to at least one pixel so no destination pixel
// can be backed by an empty span.
func pixelSpan(i, targetLen, srcLen, srcMin int) (int, int) {
	lo := srcMin + i*srcLen/targetLen
	hi := srcMin + (i+1)*srcLen/targetLen
	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// boxAverage averages the source rectangle into one destination pixel. RGBA()
// returns alpha-premultiplied 16-bit values, so the mean is already
// premultiplied: color.RGBA is the matching representation.
func boxAverage(src image.Image, x0, y0, x1, y1 int) color.RGBA {
	var r, g, bl, a, n uint64
	for sy := y0; sy < y1; sy++ {
		for sx := x0; sx < x1; sx++ {
			cr, cg, cb, ca := src.At(sx, sy).RGBA()
			r, g, bl, a = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca)
			n++
		}
	}
	if n == 0 {
		n = 1
	}
	return color.RGBA{
		R: uint8(r / n >> 8),
		G: uint8(g / n >> 8),
		B: uint8(bl / n >> 8),
		A: uint8(a / n >> 8),
	}
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
