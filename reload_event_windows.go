//go:build windows && amd64

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
	"unsafe"
)

func webhookHash(url string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(url))) }
func reloadEventName() string       { return `Global\` + className() + "-reload" }

func notifyConfigReload(c Config) error {
	h, _, _ := kernel32.NewProc("OpenEventW").Call(2, 0, uintptr(unsafe.Pointer(wide(reloadEventName()))))
	if h == 0 {
		if daemonWindow() != 0 {
			return restartDaemon()
		}
		s, _ := readState()
		if s.CaptureReady {
			return errors.New("配置已保存，但未找到可接收配置的 daemon；请在运行 daemon 的桌面执行 restart（旧版本需先更新）")
		}
		fmt.Println("daemon 未运行；下次 start 时加载配置。")
		return nil
	}
	defer kernel32.NewProc("CloseHandle").Call(h)
	ok, _, _ := kernel32.NewProc("SetEvent").Call(h)
	if ok == 0 {
		return errors.New("配置已保存，但通知 daemon 失败")
	}
	for i := 0; i < 100; i++ {
		s, _ := readState()
		if s.CaptureReady && s.WebhookHash == outputHash(c) {
			fmt.Println("daemon 已应用新 Webhook，无需重启。")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("配置已保存并通知 daemon，但尚未确认生效；可能正在投递，请稍后运行 doctor")
}

func outputHash(c Config) string { return webhookHash(c.Output + "\x00" + c.Webhook) }
