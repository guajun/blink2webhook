package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebhookProtocol(t *testing.T) {
	cases := []struct {
		name, reply    string
		status         int
		wantErr, retry bool
	}{
		{"accepted", `{"code":0}`, 200, false, false},
		{"legacy", `{"StatusCode":0}`, 200, false, false},
		{"rejected", `{"code":19021,"msg":"private details"}`, 200, true, false},
		{"rate limited", `{"code":11232}`, 200, true, true},
		{"missing code", `{}`, 200, true, false},
		{"bad JSON", `no`, 200, true, false},
		{"server unavailable", ``, 503, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Error("not POST")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if len(payload) != 2 || string(payload["msg_type"]) != `"text"` {
					t.Error("unexpected payload or signature fields")
				}
				var content map[string]string
				json.Unmarshal(payload["content"], &content)
				if content["text"] != "微信提醒" {
					t.Error("text not preserved")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			err := sendText(context.Background(), webhookClient(), server.URL, "微信提醒")
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "private details") {
					t.Fatal("leaked response")
				}
				if e, ok := err.(*DeliveryError); !ok || e.Retry != tc.retry {
					t.Fatalf("retry=%v", err)
				}
			}
		})
	}
}

func TestNoRedirectOrURLLeak(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	err := sendText(context.Background(), webhookClient(), server.URL+"/private-hook", "test")
	if err == nil || targetCalled {
		t.Fatal("followed redirect")
	}
	if strings.Contains(err.Error(), "private-hook") {
		t.Fatal("leaked endpoint")
	}
}

func TestDebouncePerApplication(t *testing.T) {
	d := Debouncer{Last: map[string]time.Time{}, Quiet: 10 * time.Second}
	now := time.Now()
	for i, step := range []struct {
		app     string
		seconds int
		want    bool
	}{
		{"QQ.exe", 0, true}, {"QQ.exe", 1, false}, {"Weixin.exe", 2, true},
		{"QQ.exe", 9, false}, {"QQ.exe", 18, false}, {"QQ.exe", 28, true},
	} {
		if got := d.Accept(Event{App: step.app, Time: now.Add(time.Duration(step.seconds) * time.Second)}); got != step.want {
			t.Fatalf("step %d: got %v", i, got)
		}
	}
}

func TestWebhookValidationAndRedaction(t *testing.T) {
	for _, host := range []string{"open.feishu.cn", "open.larksuite.com"} {
		u := "https://" + host + "/open-apis/bot/v2/hook/private-value"
		if err := validateWebhook(u); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(maskedWebhook(u), "private-value") {
			t.Fatal("leaked secret")
		}
	}
	for _, u := range []string{"http://open.feishu.cn/open-apis/bot/v2/hook/a", "https://evil.example/a", "https://open.feishu.cn.evil.example/open-apis/bot/v2/hook/a", "https://open.feishu.cn/open-apis/bot/v2/hook/", "https://open.feishu.cn/open-apis/bot/v2/hook/a?x=y", "https://user@open.feishu.cn/open-apis/bot/v2/hook/a"} {
		if validateWebhook(u) == nil {
			t.Fatal("accepted", u)
		}
	}
}

func TestConfigReplacement(t *testing.T) {
	t.Setenv("FLASH_NOTIFY_HOME", t.TempDir())
	c := defaults()
	c.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/first"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	c.Webhook = "https://open.larksuite.com/open-apis/bot/v2/hook/second"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	read, err := loadConfig()
	if err != nil || read.Webhook != c.Webhook {
		t.Fatalf("replacement failed: %v", err)
	}
}
