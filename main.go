//go:build windows && amd64

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		help()
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		help()
		return nil
	case "version", "--version":
		fmt.Println("flash-notify", version, "windows/amd64")
		return nil
	case "daemon":
		kernel32.NewProc("FreeConsole").Call()
		return runDaemon()
	case "start":
		return startDaemon()
	case "stop":
		return stopDaemon()
	case "restart":
		if err := stopDaemon(); err != nil {
			return err
		}
		return startDaemon()
	case "autostart":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return errors.New("用法：flash-notify autostart on|off")
		}
		return setAutostart(args[1] == "on")
	case "status", "doctor":
		return diagnose(args[0] == "doctor")
	case "test":
		c, err := loadConfig()
		if err != nil {
			return err
		}
		if err = validateWebhook(c.Webhook); err != nil {
			return err
		}
		err = sendText(context.Background(), webhookClient(), c.Webhook, "电脑闪烁提醒 · 测试消息\n设备："+c.Device+"\n时间："+time.Now().Format("2006-01-02 15:04:05"))
		if err == nil {
			fmt.Println("飞书 / Lark 已接受测试消息；请检查手机通知。")
		}
		return err
	case "config":
		return configure(args[1:])
	default:
		return fmt.Errorf("未知命令 %q；运行 flash-notify help 查看帮助", args[0])
	}
}

func configure(args []string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "show" {
		fmt.Println("Webhook：", maskedWebhook(c.Webhook))
		fmt.Println("设备名：", c.Device)
		fmt.Println("监测进程：", strings.Join(c.Apps, ", "))
		fmt.Println("连续闪烁合并间隔：", c.QuietSeconds, "秒")
		fmt.Println("配置目录：", dataDir())
		return nil
	}
	switch args[0] {
	case "webhook":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--stdin") {
			return errors.New("用法：flash-notify config webhook [--stdin]；URL 从输入读取，不作为命令参数")
		}
		if len(args) == 1 {
			fmt.Print("输入 Webhook URL（输入隐藏）：")
			restore := hideConsoleInput()
			defer restore()
		}
		text, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if readErr != nil && text == "" {
			return errors.New("没有读取到 Webhook URL")
		}
		c.Webhook = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
		if len(args) == 1 {
			fmt.Println()
		}
	default:
		return errors.New("目前支持：config show 或 config webhook")
	}
	if err = saveConfig(c); err != nil {
		return err
	}
	fmt.Println("Webhook 已保存：", maskedWebhook(c.Webhook))
	return notifyConfigReload(c)
}

func help() {
	fmt.Print(`Flash Notify · QQNT / 微信闪烁 → 飞书 / Lark 群机器人

  flash-notify start                   此次开启后台监测
  flash-notify stop                    此次关闭（不修改登录自启）
  flash-notify restart                 重启并重新读取配置
  flash-notify autostart on            开启用户登录自启
  flash-notify autostart off           关闭用户登录自启（不停止本次运行）
  flash-notify config webhook          隐藏输入并配置单个 Webhook
  flash-notify config webhook --stdin  从标准输入读取 Webhook
  flash-notify config show             查看脱敏配置
  flash-notify test                    发送测试消息
  flash-notify status                  查看运行及最近投递状态
  flash-notify doctor                  诊断配置、会话、自启和 daemon
  flash-notify version                 查看版本

仅支持 Windows amd64。无需管理员权限，直接走 HTTPS，不使用签名。
daemon 必须在用户桌面会话中运行；用户登录后自启，不是 Session 0 服务。
Webhook 所属机器人请关闭签名校验和关键词/IP 限制（或自行确保请求符合限制）。
`)
}
