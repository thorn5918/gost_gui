//go:build !windows

// Package ui 图形界面（仅 Windows 实现，其他平台为占位）。
package ui

import "fmt"

// Run 非 Windows 平台的占位实现。
func Run(version string) {
	fmt.Println("图形界面仅支持 Windows；可使用 -test 命令行自测模式。")
}
