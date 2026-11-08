package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThumbnailFormatsAndLetterbox(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	encoders := []struct {
		name   string
		encode func(*bytes.Buffer) error
	}{
		{"png", func(b *bytes.Buffer) error { return png.Encode(b, source) }},
		{"jpeg", func(b *bytes.Buffer) error { return jpeg.Encode(b, source, nil) }},
		{"gif", func(b *bytes.Buffer) error { return gif.Encode(b, source, nil) }},
	}
	for _, encoder := range encoders {
		t.Run(encoder.name, func(t *testing.T) {
			var input bytes.Buffer
			if err := encoder.encode(&input); err != nil {
				t.Fatal(err)
			}
			output, err := thumbnail(input.Bytes())
			if err != nil || len(output) != 26894 || !bytes.HasPrefix(output, []byte("P6\n112 80\n255\n")) {
				t.Fatalf("invalid output: %v", err)
			}
			if !bytes.Equal(output[14:17], []byte{255, 255, 255}) {
				t.Fatal("missing white letterbox")
			}
			center := 14 + (40*112+56)*3
			if output[center] < 240 || output[center+1] > 15 || output[center+2] > 15 {
				t.Fatal("wrong center color")
			}
		})
	}
}

func TestTransparentAndMalformedImages(t *testing.T) {
	var input bytes.Buffer
	png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	output, err := thumbnail(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output[14:], bytes.Repeat([]byte{255}, 112*80*3)) {
		t.Fatal("alpha was not composited on white")
	}
	for _, data := range [][]byte{nil, []byte("invalid"), input.Bytes()[:20]} {
		if _, err := thumbnail(data); err == nil {
			t.Fatal("accepted broken image")
		}
	}
	large := append([]byte(nil), input.Bytes()...)
	binary.BigEndian.PutUint32(large[16:20], 9000)
	binary.BigEndian.PutUint32(large[20:24], 9000)
	binary.BigEndian.PutUint32(large[29:33], crc32.ChecksumIEEE(large[12:29]))
	if _, err := thumbnail(large); err == nil {
		t.Fatal("accepted oversized dimensions")
	}
}

func TestOrientations(t *testing.T) {
	source := image.NewGray(image.Rect(0, 0, 3, 2))
	for i := 0; i < 6; i++ {
		source.SetGray(i%3, i/3, color.Gray{Y: uint8(i + 1)})
	}
	want := []string{"123456", "321654", "654321", "456123", "142536", "415263", "635241", "362514"}
	for value := 1; value <= 8; value++ {
		src := oriented{source, value}
		var got strings.Builder
		for y := 0; y < src.Bounds().Dy(); y++ {
			for x := 0; x < src.Bounds().Dx(); x++ {
				got.WriteByte(src.At(x, y).(color.Gray).Y + '0')
			}
		}
		if got.String() != want[value-1] {
			t.Fatalf("orientation %d: %s", value, got.String())
		}
	}
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for value := 1; value <= 8; value++ {
			tiff := make([]byte, 26)
			copy(tiff, "II")
			if order == binary.BigEndian {
				copy(tiff, "MM")
			}
			order.PutUint16(tiff[2:], 42)
			order.PutUint32(tiff[4:], 8)
			order.PutUint16(tiff[8:], 1)
			order.PutUint16(tiff[10:], 0x112)
			order.PutUint16(tiff[12:], 3)
			order.PutUint32(tiff[14:], 1)
			order.PutUint16(tiff[18:], uint16(value))
			data := []byte{0xff, 0xd8, 0xff, 0xe1, 0, 34}
			data = append(data, []byte("Exif\x00\x00")...)
			data = append(data, tiff...)
			if orientation(data) != value {
				t.Fatal("EXIF orientation not decoded")
			}
			order.PutUint32(data[16:], 0xffffffff)
			if orientation(data) != 1 {
				t.Fatal("accepted invalid TIFF offset")
			}
		}
	}
}

func TestPartAccess(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("a", 64)
	path := filepath.Join(dir, id)
	if err := os.WriteFile(path, []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := readPart(dir, id); err != nil || string(data) != "image" {
		t.Fatal("valid part not read")
	}
	for _, bad := range []string{"../" + id, strings.Repeat("z", 64), id + "/other", ""} {
		if _, err := readPart(dir, bad); err == nil {
			t.Fatal("invalid attachment ID accepted")
		}
	}
	os.Chmod(path, 0644)
	if _, err := readPart(dir, id); err == nil {
		t.Fatal("public attachment accepted")
	}
	os.Remove(path)
	os.Symlink("/does-not-exist", path)
	if _, err := readPart(dir, id); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestWebP(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRhwAAABXRUJQVlA4TA8AAAAvA0AAAAcQ/Y/+ByKi/wEA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thumbnail(data); err != nil {
		t.Fatal(err)
	}
}

func TestGIFPartialFirstFrame(t *testing.T) {
	frame := image.NewPaletted(image.Rect(2, 2, 12, 12), color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}})
	for y := 2; y < 12; y++ {
		for x := 2; x < 12; x++ {
			frame.SetColorIndex(x, y, 1)
		}
	}
	var input bytes.Buffer
	err := gif.EncodeAll(&input, &gif.GIF{Image: []*image.Paletted{frame}, Delay: []int{0}, Config: image.Config{ColorModel: frame.Palette, Width: 20, Height: 20}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thumbnail(input.Bytes()); err != nil {
		t.Fatal(err)
	}
}
