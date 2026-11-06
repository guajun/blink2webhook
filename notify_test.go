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

func TestNotificationPreviewStartsWithApp(t *testing.T) {
	for process, label := range map[string]string{"QQ.exe": "QQNT", "Weixin.exe": "微信", "WeChat.exe": "微信"} {
		text := eventText(Config{Device: strings.Repeat("long-device-name", 20)}, Event{App: process, Time: time.Now()})
		first := strings.SplitN(text, "\n", 2)[0]
		if first != "【"+label+"】请求注意" {
			t.Fatalf("app missing from first preview line: %s", first)
		}
		if !strings.Contains(text, "电脑闪烁提醒") {
			t.Fatal("existing keyword missing")
		}
	}
}

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
	t.Setenv("BLINK2WEBHOOK_HOME", t.TempDir())
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

func TestGenericWebhook(t *testing.T) {
	event := Event{App: "微信", Device: "home-pc", Time: time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))}
	for _, status := range []int{200, 201, 204, 302, 400, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					t.Error("invalid request")
				}
				var got Event
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if got.App != event.App || got.Device != event.Device || !got.Time.Equal(event.Time) {
					t.Errorf("wrong event: %+v", got)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			c := defaults()
			c.Output = "json"
			c.Webhook = server.URL
			if err := validateOutput(c); err != nil {
				t.Fatal(err)
			}
			err := sendEvent(context.Background(), webhookClient(), c, event)
			if (err == nil) != (status >= 200 && status < 300) {
				t.Fatalf("status %d: %v", status, err)
			}
			if err != nil {
				d, ok := err.(*DeliveryError)
				if !ok || d.Retry != (status == 429 || status >= 500) {
					t.Fatalf("wrong retry: %v", err)
				}
			}
		})
	}
}

func TestOutputConfigCompatibility(t *testing.T) {
	t.Setenv("BLINK2WEBHOOK_HOME", t.TempDir())
	c := defaults()
	c.Output = ""
	c.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/legacy"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig()
	if err != nil || validateOutput(loaded) != nil {
		t.Fatalf("legacy config: %v", err)
	}
	c.Output = "json"
	c.Webhook = "http://localhost:8080/private?token=secret"
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(maskedWebhook(c.Webhook), "secret") {
		t.Fatal("leaked query")
	}
	for _, endpoint := range []string{"file:///tmp/a", "https://user:secret@example.com/a", "https://example.com/a#secret", "http://"} {
		c.Webhook = endpoint
		if validateOutput(c) == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	c.Output = "unknown"
	if validateOutput(c) == nil {
		t.Fatal("unknown output accepted")
	}
}
