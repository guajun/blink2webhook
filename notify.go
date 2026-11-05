package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type DeliveryError struct {
	Description string
	Retry       bool
}

func (e *DeliveryError) Error() string { return e.Description }

func webhookClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("禁止 Webhook 重定向") }}
}

func sendText(ctx context.Context, client *http.Client, endpoint, text string) error {
	payload := struct {
		Type    string `json:"msg_type"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}{Type: "text"}
	payload.Content.Text = text
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return errors.New("无法构建 Webhook 请求")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := client.Do(req)
	if err != nil {
		return &DeliveryError{"HTTPS 请求未完成（请检查网络、代理和证书；请求可能已被接收）", true}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &DeliveryError{fmt.Sprintf("Webhook HTTP 状态 %d", res.StatusCode), res.StatusCode == 429 || res.StatusCode >= 500}
	}
	var result struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&result); err != nil {
		return &DeliveryError{"Webhook 响应不是有效 JSON", false}
	}
	code := result.Code
	if code == nil {
		code = result.StatusCode
	}
	if code == nil {
		return &DeliveryError{"Webhook 响应缺少业务状态码", false}
	}
	if *code != 0 {
		return &DeliveryError{fmt.Sprintf("Webhook 业务错误码 %d（检查机器人安全设置和限流）", *code), *code == 11232 || *code == 99991400}
	}
	return nil
}

type Event struct {
	App  string
	Time time.Time
}
type Debouncer struct {
	Last  map[string]time.Time
	Quiet time.Duration
}

func (d *Debouncer) Accept(e Event) bool {
	previous, exists := d.Last[e.App]
	d.Last[e.App] = e.Time
	return !exists || e.Time.Sub(previous) >= d.Quiet
}

func appLabel(process string) string {
	switch process {
	case "QQ.exe":
		return "QQNT"
	case "Weixin.exe", "WeChat.exe":
		return "微信"
	}
	return process
}

func eventText(c Config, e Event) string {
	return fmt.Sprintf("电脑闪烁提醒\n设备：%s\n应用：%s\n时间：%s\n检测到应用请求注意，请查看原应用。", c.Device, appLabel(e.App), e.Time.Format("2006-01-02 15:04:05"))
}
