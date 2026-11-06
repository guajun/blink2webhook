//go:build windows && amd64

package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunningDispatcherSwitchesWebhookWithoutRestart(t *testing.T) {
	t.Setenv("FLASH_NOTIFY_HOME", t.TempDir())
	c := defaults()
	c.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/old"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	sent := make(chan string, 8)
	failed := make(chan bool, 8)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		sent <- r.URL.String()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0}`)), Header: make(http.Header)}, nil
	})}
	events := make(chan Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		dispatch(ctx, c, events, client, log.New(io.Discard, "", 0), func(change func(*State)) {
			s := State{}
			change(&s)
			if s.LastError != "" {
				failed <- true
			}
		}, loadConfig)
	}()
	emit := func() { events <- Event{App: "QQ.exe", Time: time.Now()} }
	expect := func(url string) {
		t.Helper()
		select {
		case got := <-sent:
			if got != url {
				t.Fatalf("sent to %s, want %s", got, url)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no notification")
		}
	}
	emit()
	expect(c.Webhook)
	c.Webhook = "https://open.larksuite.com/open-apis/bot/v2/hook/new"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	emit()
	expect(c.Webhook) // Same running worker, same app, within debounce interval.
	if err := os.WriteFile(filepath.Join(dataDir(), "config.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	emit()
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("invalid config not reported")
	}
	select {
	case <-sent:
		t.Fatal("fell back to stale endpoint")
	default:
	}
}
