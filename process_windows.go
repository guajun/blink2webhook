//go:build windows && amd64

package main

import (
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

type processEntry struct {
	Size, Usage, PID             uint32
	DefaultHeap                  uintptr
	ModuleID, Threads, ParentPID uint32
	Priority                     int32
	Flags                        uint32
	Exe                          [260]uint16
}

type runningProcess struct {
	Name         string
	PID, Session uint32
	SessionKnown bool
}

func listProcesses() ([]runningProcess, error) {
	snapshot, _, err := kernel32.NewProc("CreateToolhelp32Snapshot").Call(2, 0)
	if snapshot == ^uintptr(0) {
		return nil, fmt.Errorf("无法枚举进程：%v", err)
	}
	defer kernel32.NewProc("CloseHandle").Call(snapshot)
	entry := processEntry{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	first := kernel32.NewProc("Process32FirstW")
	next := kernel32.NewProc("Process32NextW")
	ok, _, err := first.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	var processes []runningProcess
	for ok != 0 {
		p := runningProcess{Name: syscall.UTF16ToString(entry.Exe[:]), PID: entry.PID}
		result, _, _ := kernel32.NewProc("ProcessIdToSessionId").Call(uintptr(p.PID), uintptr(unsafe.Pointer(&p.Session)))
		p.SessionKnown = result != 0
		processes = append(processes, p)
		ok, _, err = next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	if err != syscall.Errno(18) {
		return nil, fmt.Errorf("进程枚举失败：%v", err)
	}
	return processes, nil
}

type appMatch struct {
	Name                    string
	Current, Other, Unknown []uint32
}

func matchProcesses(apps []string, processes []runningProcess, session uint32) []appMatch {
	matches := make([]appMatch, 0, len(apps))
	for _, app := range apps {
		m := appMatch{Name: app}
		for _, p := range processes {
			if !strings.EqualFold(p.Name, app) {
				continue
			}
			switch {
			case !p.SessionKnown:
				m.Unknown = append(m.Unknown, p.PID)
			case p.Session == session:
				m.Current = append(m.Current, p.PID)
			default:
				m.Other = append(m.Other, p.PID)
			}
		}
		for _, ids := range [][]uint32{m.Current, m.Other, m.Unknown} {
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		}
		matches = append(matches, m)
	}
	return matches
}

func printProcessMatches(apps []string) error {
	processes, err := listProcesses()
	if err != nil {
		return err
	}
	fmt.Printf("闪烁监测进程匹配（当前会话 %d，按进程名，不区分大小写）：\n", sessionID())
	matched := false
	for _, m := range matchProcesses(apps, processes, sessionID()) {
		switch {
		case len(m.Current) > 0:
			fmt.Printf("  [已匹配] %s：%d 个进程，PID %v\n", m.Name, len(m.Current), m.Current)
			matched = true
		case len(m.Unknown) > 0:
			fmt.Printf("  [无法确认会话] %s：PID %v，请检查运行权限或稍后重试\n", m.Name, m.Unknown)
		case len(m.Other) > 0:
			fmt.Printf("  [其他会话] %s：PID %v；请在 daemon 所在桌面会话启动应用\n", m.Name, m.Other)
		default:
			fmt.Printf("  [未匹配] %s：当前未发现此进程\n", m.Name)
		}
	}
	if !matched {
		fmt.Println("提示：当前会话尚未匹配任何监测应用，请启动 QQNT / 微信。")
	}
	fmt.Println("提示：QQNT 对应 QQ.exe；微信通常是 Weixin.exe 或 WeChat.exe，匹配其中一种即可。")
	fmt.Println("进程匹配只确认应用已启动；是否产生任务栏闪烁仍需发送消息实测。")
	return nil
}
