//go:build !windows && !linux

package gostbin

// 其他平台不嵌入 gost。
var Bytes []byte

const ExeName = "gost"
