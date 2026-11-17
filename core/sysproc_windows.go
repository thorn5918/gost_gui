//go:build windows

package core

import "syscall"

// CREATE_NO_WINDOW：为子进程隐藏控制台窗口。
// 主程序是 GUI（无控制台），启动 gost.exe 这类控制台程序时
// Windows 会默认为其分配新 cmd 窗口，必须显式抑制。
const createNoWindow = 0x08000000

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
