//go:build windows && amd64

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMatchProcessesSessionAndCase(t *testing.T) {
	processes := []runningProcess{
		{Name: "qq.EXE", PID: 30, Session: 2, SessionKnown: true},
		{Name: "QQ.exe", PID: 10, Session: 2, SessionKnown: true},
		{Name: "QQ.exe", PID: 40, Session: 3, SessionKnown: true},
		{Name: "Weixin.exe", PID: 50, Session: 2, SessionKnown: false},
		{Name: "not-QQ.exe", PID: 60, Session: 2, SessionKnown: true},
	}
	matches := matchProcesses([]string{"QQ.exe", "Weixin.exe", "WeChat.exe"}, processes, 2)
	if !reflect.DeepEqual(matches[0].Current, []uint32{10, 30}) || !reflect.DeepEqual(matches[0].Other, []uint32{40}) {
		t.Fatalf("QQ matches: %+v", matches[0])
	}
	if !reflect.DeepEqual(matches[1].Unknown, []uint32{50}) || len(matches[1].Current) != 0 {
		t.Fatalf("unknown session treated as match: %+v", matches[1])
	}
	if len(matches[2].Current)+len(matches[2].Other)+len(matches[2].Unknown) != 0 {
		t.Fatal("missing process matched")
	}
}

func TestListProcessesIncludesSelf(t *testing.T) {
	processes, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	for _, p := range processes {
		if p.PID == uint32(os.Getpid()) {
			if !strings.EqualFold(p.Name, filepath.Base(exe)) || !p.SessionKnown || p.Session != sessionID() {
				t.Fatalf("incorrect process metadata: %+v", p)
			}
			return
		}
	}
	t.Fatal("Toolhelp process snapshot did not include test process")
}
