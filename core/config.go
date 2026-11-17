package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 配置文件（exe 同目录 config.json）。
type Config struct {
	Socks5Port          int  `json:"socks5_port"`
	HTTPPort            int  `json:"http_port"`
	CheckerConcurrency  int  `json:"checker_concurrency"`
	CheckTimeoutSec     int  `json:"check_timeout_sec"`
	DirectWhenInactive  bool `json:"direct_when_inactive"`
}

var Defaults = Config{
	Socks5Port:         55558,
	HTTPPort:           55559,
	CheckerConcurrency: 32,
	CheckTimeoutSec:    5,
	DirectWhenInactive: false,
}

// LoadConfig 读取配置，不存在或损坏则写回默认值。
func LoadConfig(dir string) (Config, string) {
	cfg := Defaults
	path := filepath.Join(dir, "config.json")
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	SaveConfig(dir, cfg)
	return cfg, path
}

func SaveConfig(dir string, cfg Config) error {
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644)
}
