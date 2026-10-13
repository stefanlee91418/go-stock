//go:build linux

package main

import (
	"os"
	"strings"
)

// init 在 main / GTK 初始化之前执行：Wayland + NVIDIA 专有驱动下，WebKitGTK
// 易出现界面卡顿与拖影。用户实测 GDK_BACKEND=x11 可消除问题，故在此自动切换。
//
// 覆盖规则：
//   - 若用户已显式设置 GDK_BACKEND，完全尊重，不改写；
//   - 仅当会话为 Wayland 且检测到 /sys/module/nvidia 时默认改走 X11。
func init() {
	applyLinuxWebviewEnv()
}

func applyLinuxWebviewEnv() {
	if strings.TrimSpace(os.Getenv("GDK_BACKEND")) != "" {
		return
	}
	session := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	if session != "wayland" {
		return
	}
	if _, err := os.Stat("/sys/module/nvidia"); err != nil {
		return
	}
	_ = os.Setenv("GDK_BACKEND", "x11")
}
