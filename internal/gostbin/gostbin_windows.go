//go:build windows

package gostbin

import _ "embed"

//go:embed gost.exe.gz
var Bytes []byte

// ExeName 释放后的可执行文件名。
const ExeName = "gost.exe"
