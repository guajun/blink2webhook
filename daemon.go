//go:build windows && amd64

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type State struct {
	PID          int       `json:"pid"`
	Session      uint32    `json:"session"`
	Started      time.Time `json:"started"`
	CaptureReady bool      `json:"capture_ready"`
	WebhookHash  string    `json:"webhook_hash,omitempty"`
	LastCapture  *Event    `json:"last_capture,omitempty"`
	LastAccepted string    `json:"last_webhook_accepted,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
	Captured     uint64    `json:"captured"`
	Delivered    uint64    `json:"delivered"`
}

func readState() (State, error) {
	var s State
	b, e := os.ReadFile(filepath.Join(dataDir(), "state.json"))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	return s, e
}

func runDaemon() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if err = validateOutput(c); err != nil {
		return err
	}
	return runCaptureReload(c, webhookClient(), loadConfig)
}

func runCapture(c Config, client *http.Client) error {
	return runCaptureReload(c, client, nil)
}

func runCaptureReload(c Config, client *http.Client, reload func() (Config, error)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if sessionID() == 0 {
		return errors.New("daemon 必须运行在交互式桌面会话，不能从 SSH / Session 0 启动")
	}
	if err := os.MkdirAll(dataDir(), 0700); err != nil {
		return err
	}
	mutex, _, mutexErr := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(wide(`Local\`+className()))))
	if mutex == 0 {
		return errors.New("无法创建 daemon 互斥锁")
	}
	defer kernel32.NewProc("CloseHandle").Call(mutex)
	if mutexErr == syscall.Errno(183) {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(dataDir(), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	logger := log.New(file, "", log.LstdFlags)
	state := State{PID: os.Getpid(), Session: sessionID(), Started: time.Now(), WebhookHash: outputHash(c)}
	var stateMu sync.Mutex
	update := func(change func(*State)) {
		stateMu.Lock()
		defer stateMu.Unlock()
		change(&state)
		if e := writeJSON(filepath.Join(dataDir(), "state.json"), state); e != nil {
			logger.Print("状态保存失败")
		}
	}
	events := make(chan Event, 128)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var worker sync.WaitGroup
	reloadRequests := make(chan struct{}, 1)
	var reloadEvent uintptr
	if reload != nil {
		reloadEvent, _, _ = kernel32.NewProc("CreateEventW").Call(0, 0, 0, uintptr(unsafe.Pointer(wide(reloadEventName()))))
		if reloadEvent == 0 {
			return errors.New("创建配置通知事件失败")
		}
		defer kernel32.NewProc("CloseHandle").Call(reloadEvent)
		worker.Add(1)
		go func() {
			defer worker.Done()
			for {
				r, _, _ := kernel32.NewProc("WaitForSingleObject").Call(reloadEvent, 0xffffffff)
				if r != 0 || ctx.Err() != nil {
					return
				}
				select {
				case reloadRequests <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	worker.Add(1)
	go func() { defer worker.Done(); dispatch(ctx, c, events, client, logger, update, reload, reloadRequests) }()
	defer func() {
		cancel()
		if reloadEvent != 0 {
			kernel32.NewProc("SetEvent").Call(reloadEvent)
		}
		worker.Wait()
		update(func(s *State) { s.CaptureReady = false })
		logger.Print("daemon 已退出")
	}()
	shell, _, _ := registerMessage.Call(uintptr(unsafe.Pointer(wide("SHELLHOOK"))))
	if shell == 0 {
		return errors.New("注册 Shell 消息失败")
	}
	allowed := map[string]string{}
	for _, app := range c.Apps {
		allowed[strings.ToLower(app)] = app
	}
	callback := syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
		if msg == uint32(shell) && wparam == 0x8006 {
			if app, ok := allowed[strings.ToLower(processName(lparam))]; ok {
				select {
				case events <- Event{App: appLabel(app), Device: c.Device, Time: time.Now()}:
				default:
					logger.Print("事件队列已满，忽略一条闪烁")
				}
			}
		}
		if msg == 0x10 {
			destroyWindow.Call(hwnd)
			return 0
		}
		if msg == 0x2 {
			postQuit.Call(0)
			return 0
		}
		result, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
		return result
	})
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	class := windowClass{Proc: callback, Instance: instance, Name: wide(className())}
	class.Size = uint32(unsafe.Sizeof(class))
	atom, _, _ := registerClass.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return errors.New("注册隐藏窗口类失败")
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(class.Name)), instance)
	hwnd, _, _ := createWindow.Call(0x80, uintptr(unsafe.Pointer(class.Name)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return errors.New("创建隐藏窗口失败")
	}
	ok, _, _ := registerShell.Call(hwnd)
	if ok == 0 {
		destroyWindow.Call(hwnd)
		return errors.New("注册 Shell Hook 失败")
	}
	defer deregisterShell.Call(hwnd)
	update(func(s *State) { s.CaptureReady = true })
	logger.Printf("daemon 就绪 pid=%d session=%d apps=%s", state.PID, state.Session, strings.Join(c.Apps, ","))
	var msg windowMessage
	for {
		result, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			return errors.New("Windows 消息循环失败")
		}
		if result == 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}

func dispatch(ctx context.Context, c Config, events <-chan Event, client *http.Client, logger *log.Logger, update func(func(*State)), reload func() (Config, error), reloadRequests <-chan struct{}) {
	d := Debouncer{Last: map[string]time.Time{}, Quiet: time.Duration(c.QuietSeconds) * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case <-reloadRequests:
			if reload != nil {
				latest, loadErr := reload()
				if loadErr == nil {
					loadErr = validateOutput(latest)
				}
				if loadErr != nil {
					logger.Print("配置更新失败，保留原运行配置；请修复后重新执行 config 或 restart")
					update(func(s *State) { s.LastError = "读取最新 Webhook 配置失败" })
					continue
				}
				if latest.Webhook != c.Webhook || latest.Output != c.Output {
					c.Webhook = latest.Webhook
					c.Output = latest.Output
					d.Last = map[string]time.Time{}
					logger.Print("已应用新的 Webhook 配置")
				}
				update(func(s *State) { s.WebhookHash = outputHash(c); s.LastError = "" })
			}
		case e := <-events:
			update(func(s *State) { s.Captured++; s.LastCapture = &e })
			if !d.Accept(e) {
				continue
			}
			logger.Printf("捕获提醒 app=%s", e.App)
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				err = sendEvent(ctx, client, c, e)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return
				}
				retry, ok := err.(*DeliveryError)
				if !ok || !retry.Retry {
					break
				}
				if attempt == 2 {
					break
				}
				delay := time.Duration(2<<attempt) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
			}
			if err != nil {
				logger.Printf("投递失败 app=%s error=%s", e.App, err)
				update(func(s *State) { s.LastError = fmt.Sprint(err) })
			} else {
				logger.Printf("Webhook 已接受 app=%s", e.App)
				update(func(s *State) { s.Delivered++; s.LastAccepted = time.Now().Format(time.RFC3339); s.LastError = "" })
			}
		}
	}
}
