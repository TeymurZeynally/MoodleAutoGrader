package extract

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"  // screenshots in GIF
	_ "image/jpeg" // and JPEG
	"image/png"

	_ "golang.org/x/image/bmp"  // and BMP
	_ "golang.org/x/image/webp" // and WebP
)

const (
	maxImagePixels = 40_000_000 // more is a decompression bomb, not a screenshot
	modelImageSide = 1600       // the longest side of a picture given to the model
)

// ModelImage is a picture a student uploaded as the vision model gets it. It is decoded here, in Go, and encoded again
// as PNG: the model server never parses the student's own bytes. A picture with too many pixels is refused before it is
// decoded. A larger one is shrunk so that every pixel is the average of all the source pixels it covers: text hidden
// in the pixels that a scaler samples and shows only once the picture is scaled down has nowhere to appear.
func ModelImage(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a picture: %w", err)
	}

	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxImagePixels {
		return nil, fmt.Errorf("the picture is %d×%d pixels, too large for a screenshot", cfg.Width, cfg.Height)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode the picture: %w", err)
	}

	var out bytes.Buffer
	if err := png.Encode(&out, shrink(img, modelImageSide)); err != nil {
		return nil, fmt.Errorf("encode the picture: %w", err)
	}

	return out.Bytes(), nil
}

// shrink scales img down to at most side pixels on its longest side by area averaging.
func shrink(img image.Image, side int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	if w <= side && h <= side {
		return src
	}

	nw, nh := max(1, w*side/max(w, h)), max(1, h*side/max(w, h))
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for oy := range nh {
		y0, y1 := oy*h/nh, (oy+1)*h/nh
		for ox := range nw {
			x0, x1 := ox*w/nw, (ox+1)*w/nw
			var sum [4]int
			for y := y0; y < y1; y++ {
				row := src.Pix[y*src.Stride+x0*4 : y*src.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					sum[0], sum[1], sum[2], sum[3] = sum[0]+int(row[i]), sum[1]+int(row[i+1]), sum[2]+int(row[i+2]), sum[3]+int(row[i+3])
				}
			}
			n, o := (y1-y0)*(x1-x0), out.PixOffset(ox, oy)
			for c := range 4 {
				out.Pix[o+c] = uint8(sum[c] / n) //nolint:gosec // G115: an average of bytes is a byte
			}
		}
	}

	return out
}
