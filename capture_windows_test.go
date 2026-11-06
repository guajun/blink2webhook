//go:build windows && amd64

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Exercises a real Shell FLASH event through a real hidden daemon window and
// receives the resulting unsigned HTTPS-style request on a local test server.
func TestShellFlashIntegration(t *testing.T) {
	shell, _, _ := user32.NewProc("GetShellWindow").Call()
	if sessionID() == 0 || shell == 0 {
		t.Skip("requires an interactive Windows desktop")
	}
	t.Setenv("BLINK2WEBHOOK_HOME", t.TempDir())
	received := make(chan bool, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received <- true; w.Write([]byte(`{"code":0}`)) }))
	defer server.Close()
	c := defaults()
	exe, _ := os.Executable()
	c.Apps = []string{filepath.Base(exe)}
	c.Webhook = server.URL
	done := make(chan error, 1)
	go func() { done <- runCapture(c, webhookClient()) }()
	defer func() {
		if h := daemonWindow(); h != 0 {
			postMessage.Call(h, 0x10, 0, 0)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	}()
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		state, _ := readState()
		if state.CaptureReady {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("capture not ready")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	callback := syscall.NewCallback(func(h uintptr, m uint32, w, l uintptr) uintptr {
		r, _, _ := defWindowProc.Call(h, uintptr(m), w, l)
		return r
	})
	cls := windowClass{Proc: callback, Instance: instance, Name: wide("Blink2WebhookIntegrationWindow")}
	cls.Size = uint32(unsafe.Sizeof(cls))
	atom, _, _ := registerClass.Call(uintptr(unsafe.Pointer(&cls)))
	if atom == 0 {
		t.Fatal("register test window")
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(cls.Name)), instance)
	hwnd, _, _ := createWindow.Call(0x08040000, uintptr(unsafe.Pointer(cls.Name)), uintptr(unsafe.Pointer(wide("blink2webhook capture test"))), 0x00CF0000, 0, 0, 240, 120, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatal("create test window")
	}
	defer destroyWindow.Call(hwnd)
	user32.NewProc("ShowWindow").Call(hwnd, 4)
	type flashInfo struct {
		Size                  uint32
		Window                uintptr
		Flags, Count, Timeout uint32
	}
	fi := flashInfo{Window: hwnd, Flags: 2, Count: 3, Timeout: 150}
	fi.Size = uint32(unsafe.Sizeof(fi))
	user32.NewProc("FlashWindowEx").Call(uintptr(unsafe.Pointer(&fi)))
	forwarded := false
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline) && !forwarded; {
		var msg windowMessage
		for {
			ok, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
			if ok == 0 {
				break
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
		select {
		case <-received:
			forwarded = true
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !forwarded {
		t.Fatal("real taskbar FLASH was not forwarded")
	}
	select {
	case <-received:
		t.Fatal("duplicate notification from repeated flashing")
	case <-time.After(600 * time.Millisecond):
	}
}
