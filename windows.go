//go:build windows && amd64

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var registerClass = user32.NewProc("RegisterClassExW")
var createWindow = user32.NewProc("CreateWindowExW")
var defWindowProc = user32.NewProc("DefWindowProcW")
var registerMessage = user32.NewProc("RegisterWindowMessageW")
var registerShell = user32.NewProc("RegisterShellHookWindow")
var deregisterShell = user32.NewProc("DeregisterShellHookWindow")
var findWindow = user32.NewProc("FindWindowW")
var postMessage = user32.NewProc("PostMessageW")
var destroyWindow = user32.NewProc("DestroyWindow")
var postQuit = user32.NewProc("PostQuitMessage")
var getMessage = user32.NewProc("GetMessageW")
var translateMessage = user32.NewProc("TranslateMessage")
var dispatchMessage = user32.NewProc("DispatchMessageW")
var getWindowPID = user32.NewProc("GetWindowThreadProcessId")

func wide(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }
func className() string {
	return fmt.Sprintf("FlashNotifyDaemon-%x", sha256.Sum256([]byte(strings.ToLower(dataDir()))))
}
func daemonWindow() uintptr {
	h, _, _ := findWindow.Call(uintptr(unsafe.Pointer(wide(className()))), 0)
	return h
}
func sessionID() uint32 {
	var id uint32
	kernel32.NewProc("ProcessIdToSessionId").Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&id)))
	return id
}

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type windowMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

func processName(hwnd uintptr) string {
	var pid uint32
	getWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	process, _, _ := kernel32.NewProc("OpenProcess").Call(0x1000, 0, uintptr(pid))
	if process == 0 {
		return ""
	}
	defer kernel32.NewProc("CloseHandle").Call(process)
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	ok, _, _ := kernel32.NewProc("QueryFullProcessImageNameW").Call(process, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size]))
}

func launchHidden(executable string, args ...string) *exec.Cmd {
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

func startDaemon() error {
	if daemonWindow() != 0 {
		fmt.Println("daemon 已在运行。")
		return nil
	}
	if sessionID() == 0 {
		return errors.New("请在已登录的 Windows 桌面终端启动；SSH / Session 0 无法捕获桌面闪烁")
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err = validateWebhook(c.Webhook); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := launchHidden(exe, "daemon")
	if err = cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	for i := 0; i < 100; i++ {
		state, _ := readState()
		if daemonWindow() != 0 && state.PID == pid && state.CaptureReady {
			fmt.Printf("本次监测已开启（PID %d）。\n", pid)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("daemon 未就绪，请运行 doctor 并查看 daemon.log")
}

func stopDaemon() error {
	h := daemonWindow()
	if h == 0 {
		fmt.Println("daemon 已停止。")
		return nil
	}
	var pid uint32
	getWindowPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	process, _, _ := kernel32.NewProc("OpenProcess").Call(0x100000, 0, uintptr(pid))
	if process != 0 {
		defer kernel32.NewProc("CloseHandle").Call(process)
	}
	ok, _, _ := postMessage.Call(h, 0x10, 0, 0)
	if ok == 0 {
		return errors.New("无法向 daemon 发送停止请求")
	}
	for i := 0; i < 300; i++ {
		if daemonWindow() == 0 {
			if process != 0 {
				result, _, _ := kernel32.NewProc("WaitForSingleObject").Call(process, 15000)
				if result != 0 {
					return errors.New("daemon 退出尚未完成，请稍后再试")
				}
			}
			fmt.Println("本次监测已关闭；登录自启设置保持不变。")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("daemon 尚未退出，请查看日志")
}

func restartDaemon() error {
	if err := stopDaemon(); err != nil {
		return err
	}
	return startDaemon()
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "FlashNotify"

func setAutostart(on bool) error {
	args := []string{"delete", `HKCU\` + runKey, "/v", runValue, "/f"}
	if on {
		c, err := loadConfig()
		if err != nil {
			return err
		}
		if err = validateWebhook(c.Webhook); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		args = []string{"add", `HKCU\` + runKey, "/v", runValue, "/t", "REG_SZ", "/d", `"` + exe + `" daemon`, "/f"}
	} else if autostartCommand() == "" {
		fmt.Println("登录自启已关闭。")
		return nil
	}
	if err := launchHidden("reg.exe", args...).Run(); err != nil {
		return errors.New("无法更新当前用户的登录自启项")
	}
	if on {
		fmt.Println("登录自启已开启；登录后在用户桌面会话启动 daemon。")
	} else {
		fmt.Println("登录自启已关闭；本次运行状态保持不变。")
	}
	return nil
}

func autostartCommand() string {
	dll := syscall.NewLazyDLL("advapi32.dll")
	var key syscall.Handle
	r, _, _ := dll.NewProc("RegOpenKeyExW").Call(0x80000001, uintptr(unsafe.Pointer(wide(runKey))), 0, 0x20019, uintptr(unsafe.Pointer(&key)))
	if r != 0 {
		return ""
	}
	defer dll.NewProc("RegCloseKey").Call(uintptr(key))
	buf := make([]uint16, 32768)
	size := uint32(len(buf) * 2)
	var typ uint32
	r, _, _ = dll.NewProc("RegQueryValueExW").Call(uintptr(key), uintptr(unsafe.Pointer(wide(runValue))), 0, uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 || typ != 1 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func hideConsoleInput() func() {
	handle := uintptr(os.Stdin.Fd())
	var mode uint32
	getMode := kernel32.NewProc("GetConsoleMode")
	setMode := kernel32.NewProc("SetConsoleMode")
	ok, _, _ := getMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if ok == 0 {
		return func() {}
	}
	setMode.Call(handle, uintptr(mode&^4))
	return func() { setMode.Call(handle, uintptr(mode)) }
}

func diagnose(strict bool) error {
	healthy := true
	c, err := loadConfig()
	fmt.Println("版本：", version, "windows/amd64")
	fmt.Println("目录：", dataDir())
	fmt.Println("桌面会话：", sessionID(), "（必须非 0）")
	if sessionID() == 0 {
		healthy = false
	}
	if err != nil {
		fmt.Println("配置：异常：", err)
		healthy = false
	} else {
		fmt.Println("Webhook：", maskedWebhook(c.Webhook))
		if validateWebhook(c.Webhook) != nil {
			healthy = false
		}
		fmt.Println("签名校验：不发送签名；请在机器人端关闭签名校验")
		fmt.Println("监测进程：", strings.Join(c.Apps, ", "))
		if err := printProcessMatches(c.Apps); err != nil {
			fmt.Println("闪烁监测进程查询失败：", err)
			healthy = false
		}
	}
	cmd := autostartCommand()
	if cmd == "" {
		fmt.Println("登录自启：关闭")
	} else {
		fmt.Println("登录自启：", cmd)
		exe, _ := os.Executable()
		if cmd != `"`+exe+`" daemon` {
			fmt.Println("注意：自启路径与当前程序不一致；运行 autostart on 修复。")
			healthy = false
		}
	}
	if daemonWindow() == 0 {
		fmt.Println("daemon：停止")
		healthy = false
	} else {
		fmt.Println("daemon：运行中")
	}
	s, err := readState()
	if err == nil {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println("最近运行状态：\n" + string(b))
		if daemonWindow() != 0 && !s.CaptureReady {
			healthy = false
		}
	} else if daemonWindow() != 0 {
		healthy = false
		fmt.Println("运行状态：尚未就绪")
	}
	fmt.Println("日志：", filepath.Join(dataDir(), "daemon.log"))
	fmt.Println("实现：用户会话 daemon + HKCU Run 登录自启；不安装 Windows Session 0 服务。")
	fmt.Println("doctor 不会自动发消息；发送验证请使用 test。")
	if strict && !healthy {
		return errors.New("诊断发现未配置、停止或异常项目，见上方说明")
	}
	return nil
}
