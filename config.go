package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Output       string   `json:"output"`
	Webhook      string   `json:"webhook"`
	Device       string   `json:"device"`
	Apps         []string `json:"apps"`
	QuietSeconds int      `json:"quiet_seconds"`
}

func dataDir() string {
	if p := os.Getenv("BLINK2WEBHOOK_HOME"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Blink2Webhook")
}

func defaults() Config {
	name, _ := os.Hostname()
	return Config{Output: "feishu", Device: name, Apps: []string{"QQ.exe", "Weixin.exe", "WeChat.exe"}, QuietSeconds: 10}
}

func validateWebhook(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("Webhook URL 格式无效")
	}
	if u.Scheme != "https" || (u.Host != "open.feishu.cn" && u.Host != "open.larksuite.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("仅支持飞书或 Lark 官方 HTTPS 群机器人 Webhook")
	}
	const prefix = "/open-apis/bot/v2/hook/"
	if !strings.HasPrefix(u.Path, prefix) || strings.TrimPrefix(u.Path, prefix) == "" || strings.Contains(strings.TrimPrefix(u.Path, prefix), "/") {
		return errors.New("Webhook 路径应为 /open-apis/bot/v2/hook/<标识>")
	}
	return nil
}

func loadConfig() (Config, error) {
	c := defaults()
	b, err := os.ReadFile(filepath.Join(dataDir(), "config.json"))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, errors.New("config.json 无法解析，请修复配置文件")
	}
	if c.QuietSeconds < 1 || c.QuietSeconds > 3600 {
		return c, errors.New("quiet_seconds 必须介于 1 和 3600")
	}
	if len(c.Apps) == 0 {
		return c, errors.New("至少需要监测一个进程")
	}
	return c, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".blink2webhook-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func saveConfig(c Config) error {
	if err := validateOutput(c); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dataDir(), "config.json"), c); err != nil {
		return err
	}
	return nil
}

func maskedWebhook(raw string) string {
	if raw == "" {
		return "未配置"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "已配置（URL 无效）"
	}
	return fmt.Sprintf("%s://%s/***", u.Scheme, u.Host)
}
func validateOutput(c Config) error {
	switch c.Output {
	case "", "feishu":
		return validateWebhook(c.Webhook)
	case "json":
		u, err := url.Parse(c.Webhook)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return errors.New("通用 Webhook 必须为 HTTP(S) URL，不能包含用户信息或片段")
		}
		return nil
	default:
		return errors.New("output 必须为 feishu 或 json")
	}
}
