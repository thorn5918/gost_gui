//go:build ignore

// genicon 生成简单的程序图标（蓝色圆角方块 + 白色闪电）。
// 用法: go run tools/genicon/main.go assets/app.ico
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	out := "assets/app.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	sizes := []int{16, 32, 48, 64, 256}
	var blobs [][]byte
	for _, s := range sizes {
		blobs = append(blobs, pngBytes(draw(s)))
	}

	buf := &bytes.Buffer{}
	binary.Write(buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1)) // type icon
	binary.Write(buf, binary.LittleEndian, uint16(len(blobs)))

	offset := 6 + 16*len(blobs)
	for i, s := range sizes {
		w, h := byte(s), byte(s)
		if s == 256 {
			w, h = 0, 0
		}
		buf.WriteByte(w)
		buf.WriteByte(h)
		buf.WriteByte(0) // colors
		buf.WriteByte(0) // reserved
		binary.Write(buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(buf, binary.LittleEndian, uint16(32)) // bitcount
		binary.Write(buf, binary.LittleEndian, uint32(len(blobs[i])))
		binary.Write(buf, binary.LittleEndian, uint32(offset))
		offset += len(blobs[i])
	}
	for _, b := range blobs {
		buf.Write(b)
	}
	if err := os.MkdirAll("assets", 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Println("icon written:", out)
}

func draw(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{28, 108, 220, 255}
	edge := color.RGBA{16, 80, 170, 255}
	white := color.RGBA{255, 255, 255, 255}
	r := size / 5 // 圆角半径
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if inRounded(x, y, size, r) {
				// 简单渐变：左上亮右下暗
				t := float32(x+y) / float32(2*size)
				c := lerp(bg, edge, t)
				img.Set(x, y, c)
			}
		}
	}
	// 闪电多边形（0-100 坐标系）
	bolt := [][2]float32{
		{58, 8}, {30, 52}, {47, 52}, {38, 92}, {72, 42}, {52, 42},
	}
	n := len(bolt)
	verts := make([][2]float32, n)
	for i, p := range bolt {
		verts[i] = [2]float32{p[0] * float32(size) / 100, p[1] * float32(size) / 100}
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if pointInPoly(float32(x)+0.5, float32(y)+0.5, verts) {
				img.Set(x, y, white)
			}
		}
	}
	return img
}

// pointInPoly 标准射线法。
func pointInPoly(px, py float32, poly [][2]float32) bool {
	inside := false
	n := len(poly)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > py) != (yj > py) &&
			px < (xj-xi)*(py-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

func inRounded(x, y, size, r int) bool {
	if x < r && y < r {
		return (x-r)*(x-r)+(y-r)*(y-r) <= r*r
	}
	if x >= size-r && y < r {
		return (x-(size-1-r))*(x-(size-1-r))+(y-r)*(y-r) <= r*r
	}
	if x < r && y >= size-r {
		return (x-r)*(x-r)+(y-(size-1-r))*(y-(size-1-r)) <= r*r
	}
	if x >= size-r && y >= size-r {
		return (x-(size-1-r))*(x-(size-1-r))+(y-(size-1-r))*(y-(size-1-r)) <= r*r
	}
	return true
}

func lerp(a, b color.RGBA, t float32) color.RGBA {
	return color.RGBA{
		uint8(float32(a.R) + t*(float32(b.R)-float32(a.R))),
		uint8(float32(a.G) + t*(float32(b.G)-float32(a.G))),
		uint8(float32(a.B) + t*(float32(b.B)-float32(a.B))),
		255,
	}
}

func pngBytes(img image.Image) []byte {
	buf := &bytes.Buffer{}
	if err := png.Encode(buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
