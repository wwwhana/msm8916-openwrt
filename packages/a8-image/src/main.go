// SPDX-License-Identifier: GPL-2.0-only
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxBytes = 8 << 20

func readPart(directory, id string) ([]byte, error) {
	if len(id) != 64 {
		return nil, errors.New("invalid attachment")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, errors.New("invalid attachment")
	}
	path := filepath.Join(directory, id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > maxBytes {
		return nil, errors.New("attachment unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("attachment unavailable")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("attachment changed")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, errors.New("attachment unavailable")
	}
	return data, nil
}

// Read the camera orientation without trusting TIFF offsets or entry counts.
func orientation(data []byte) int {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff || data[offset+1] == 0xda || data[offset+1] == 0xd9 {
			break
		}
		size := int(binary.BigEndian.Uint16(data[offset+2:]))
		if size < 2 || size > len(data)-offset-2 {
			break
		}
		segment := data[offset+4 : offset+2+size]
		if data[offset+1] == 0xe1 && len(segment) >= 14 && string(segment[:6]) == "Exif\x00\x00" {
			tiff := segment[6:]
			var order binary.ByteOrder
			switch string(tiff[:2]) {
			case "II":
				order = binary.LittleEndian
			case "MM":
				order = binary.BigEndian
			default:
				return 1
			}
			if order.Uint16(tiff[2:]) != 42 {
				return 1
			}
			start := uint64(order.Uint32(tiff[4:]))
			if start+2 > uint64(len(tiff)) {
				return 1
			}
			count := int(order.Uint16(tiff[start:]))
			for i, pos := 0, int(start)+2; i < count && pos+12 <= len(tiff); i, pos = i+1, pos+12 {
				entry := tiff[pos:]
				if order.Uint16(entry) == 0x112 && order.Uint16(entry[2:]) == 3 && order.Uint32(entry[4:]) == 1 {
					value := int(order.Uint16(entry[8:]))
					if value >= 1 && value <= 8 {
						return value
					}
				}
			}
		}
		offset += 2 + size
	}
	return 1
}

type oriented struct {
	image.Image
	value int
}

func (o oriented) Bounds() image.Rectangle {
	w, h := o.Image.Bounds().Dx(), o.Image.Bounds().Dy()
	if o.value >= 5 {
		w, h = h, w
	}
	return image.Rect(0, 0, w, h)
}

func (o oriented) At(x, y int) color.Color {
	w, h := o.Image.Bounds().Dx(), o.Image.Bounds().Dy()
	switch o.value {
	case 2:
		x = w - 1 - x
	case 3:
		x, y = w-1-x, h-1-y
	case 4:
		y = h - 1 - y
	case 5:
		x, y = y, x
	case 6:
		x, y = y, h-1-x
	case 7:
		x, y = w-1-y, h-1-x
	case 8:
		x, y = w-1-y, x
	}
	return o.Image.At(x+o.Image.Bounds().Min.X, y+o.Image.Bounds().Min.Y)
}

func thumbnail(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 ||
		int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, errors.New("unsupported image dimensions")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("image unavailable")
	}
	bounds := image.Rect(0, 0, config.Width, config.Height)
	if decoded.Bounds().Empty() || !decoded.Bounds().In(bounds) {
		return nil, errors.New("image unavailable")
	}
	if decoded.Bounds() != bounds {
		canvas := image.NewNRGBA(bounds)
		draw.Draw(canvas, decoded.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
		decoded = canvas
	}
	var source image.Image = decoded
	if value := orientation(data); value != 1 {
		source = oriented{decoded, value}
	}
	w, h := source.Bounds().Dx(), source.Bounds().Dy()
	width, height := 112, max(1, h*112/w)
	if height > 80 {
		height, width = 80, max(1, w*80/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, 112, 80))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	x, y := (112-width)/2, (80-height)/2
	draw.CatmullRom.Scale(dst, image.Rect(x, y, x+width, y+height), source, source.Bounds(), draw.Over, nil)
	result := []byte("P6\n112 80\n255\n")
	for y := 0; y < 80; y++ {
		for x := 0; x < 112; x++ {
			c := dst.RGBAAt(x, y)
			result = append(result, c.R, c.G, c.B)
		}
	}
	return result, nil
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(1)
	}
	data, err := readPart("/etc/modemconfig-ko/parts", os.Args[1])
	if err == nil {
		data, err = thumbnail(data)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "image preview unavailable")
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		os.Exit(1)
	}
}
