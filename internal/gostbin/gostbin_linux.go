//go:build linux

package gostbin

import _ "embed"

// gost.gz 为 linux 版 gost，仅用于本机端到端测试。
//go:embed gost.gz
var Bytes []byte

// ExeName 释放后的可执行文件名。
const ExeName = "gost"
