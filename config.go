package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var defaultConfig = Config{
	BaseURL:             "https://mzf.mapay.cc",
	PrimaryCode:         "yun_alipay_xd",
	FallbackCode:        "alipay_pc",
	PollIntervalSec:     10,
	HTTPTimeoutSec:      5,
	DownConfirmTimes:    2,
	RecoverConfirmTimes:  3,
	MaxRetry:             3,
	BackoffMaxSec:       60,
	BarkBaseURL:         "https://api.day.app",
	BarkGroup:           "mzf-fallback",
}

var configPath = filepath.Join(getWorkingDir(), "config.json")

var configMu sync.RWMutex

type Config struct {
	BaseURL             string `json:"base_url"`
	Authorization       string `json:"authorization"`
	PrimaryCode         string `json:"primary_code"`
	FallbackCode        string `json:"fallback_code"`
	PollIntervalSec     int    `json:"poll_interval_sec"`
	HTTPTimeoutSec      int    `json:"http_timeout_sec"`
	DownConfirmTimes    int    `json:"down_confirm_times"`
	RecoverConfirmTimes int    `json:"recover_confirm_times"`
	MaxRetry            int    `json:"max_retry"`
	BackoffMaxSec       int    `json:"backoff_max_sec"`
	BarkBaseURL         string `json:"bark_base_url"`
	BarkDeviceKey       string `json:"bark_device_key"`
	BarkGroup           string `json:"bark_group"`
	BarkSound           string `json:"bark_sound"`
}

type ConfigStore struct {
	Path string
}

func NewConfigStore() *ConfigStore {
	return &ConfigStore{Path: configPath}
}

func (s *ConfigStore) Load() (Config, error) {
	configMu.RLock()
	defer configMu.RUnlock()

	b, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := defaultConfig
			if err := cfg.validate(); err != nil {
				return Config{}, err
			}
			if err := writeAtomic(s.Path, mustJSON(cfg)); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, err
	}

	cfg := defaultConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.normalize()
	return cfg, nil
}

func (s *ConfigStore) Save(cfg Config) error {
	configMu.Lock()
	defer configMu.Unlock()

	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(s.Path, b)
}

func (s *ConfigStore) Update(patch Config) (Config, error) {
	current, err := s.Load()
	if err != nil {
		return Config{}, err
	}
	mergeConfig(&current, patch)
	if err := s.Save(current); err != nil {
		return Config{}, err
	}
	return current, nil
}

func (cfg *Config) normalize() {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Authorization = strings.TrimSpace(cfg.Authorization)
	cfg.PrimaryCode = strings.TrimSpace(cfg.PrimaryCode)
	cfg.FallbackCode = strings.TrimSpace(cfg.FallbackCode)
	cfg.BarkBaseURL = strings.TrimSpace(cfg.BarkBaseURL)
	cfg.BarkDeviceKey = strings.TrimSpace(cfg.BarkDeviceKey)
	cfg.BarkGroup = strings.TrimSpace(cfg.BarkGroup)
	cfg.BarkSound = strings.TrimSpace(cfg.BarkSound)
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultConfig.BaseURL
	}
	if cfg.PrimaryCode == "" {
		cfg.PrimaryCode = defaultConfig.PrimaryCode
	}
	if cfg.FallbackCode == "" {
		cfg.FallbackCode = defaultConfig.FallbackCode
	}
	if cfg.PollIntervalSec <= 0 {
		cfg.PollIntervalSec = defaultConfig.PollIntervalSec
	}
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = defaultConfig.HTTPTimeoutSec
	}
	if cfg.DownConfirmTimes <= 0 {
		cfg.DownConfirmTimes = defaultConfig.DownConfirmTimes
	}
	if cfg.RecoverConfirmTimes <= 0 {
		cfg.RecoverConfirmTimes = defaultConfig.RecoverConfirmTimes
	}
	if cfg.MaxRetry <= 0 {
		cfg.MaxRetry = defaultConfig.MaxRetry
	}
	if cfg.BackoffMaxSec <= 0 {
		cfg.BackoffMaxSec = defaultConfig.BackoffMaxSec
	}
	if cfg.BarkBaseURL == "" {
		cfg.BarkBaseURL = defaultConfig.BarkBaseURL
	}
	if cfg.BarkGroup == "" {
		cfg.BarkGroup = defaultConfig.BarkGroup
	}
}

func (cfg Config) validate() error {
	if cfg.BaseURL == "" {
		return errors.New("base_url 不能为空")
	}
	if cfg.PrimaryCode == "" {
		return errors.New("primary_code 不能为空")
	}
	if cfg.FallbackCode == "" {
		return errors.New("fallback_code 不能为空")
	}
	if cfg.PollIntervalSec < 1 {
		return errors.New("poll_interval_sec 必须大于等于 1")
	}
	if cfg.HTTPTimeoutSec < 1 {
		return errors.New("http_timeout_sec 必须大于等于 1")
	}
	if cfg.DownConfirmTimes < 1 {
		return errors.New("down_confirm_times 必须大于等于 1")
	}
	if cfg.RecoverConfirmTimes < 1 {
		return errors.New("recover_confirm_times 必须大于等于 1")
	}
	if cfg.MaxRetry < 1 {
		return errors.New("max_retry 必须大于等于 1")
	}
	if cfg.BackoffMaxSec < 1 {
		return errors.New("backoff_max_sec 必须大于等于 1")
	}
	return nil
}

func mergeConfig(dst *Config, patch Config) {
	if patch.BaseURL != "" {
		dst.BaseURL = patch.BaseURL
	}
	if patch.Authorization != "" {
		dst.Authorization = patch.Authorization
	}
	if patch.PrimaryCode != "" {
		dst.PrimaryCode = patch.PrimaryCode
	}
	if patch.FallbackCode != "" {
		dst.FallbackCode = patch.FallbackCode
	}
	if patch.PollIntervalSec != 0 {
		dst.PollIntervalSec = patch.PollIntervalSec
	}
	if patch.HTTPTimeoutSec != 0 {
		dst.HTTPTimeoutSec = patch.HTTPTimeoutSec
	}
	if patch.DownConfirmTimes != 0 {
		dst.DownConfirmTimes = patch.DownConfirmTimes
	}
	if patch.RecoverConfirmTimes != 0 {
		dst.RecoverConfirmTimes = patch.RecoverConfirmTimes
	}
	if patch.MaxRetry != 0 {
		dst.MaxRetry = patch.MaxRetry
	}
	if patch.BackoffMaxSec != 0 {
		dst.BackoffMaxSec = patch.BackoffMaxSec
	}
	if patch.BarkBaseURL != "" {
		dst.BarkBaseURL = patch.BarkBaseURL
	}
	if patch.BarkDeviceKey != "" {
		dst.BarkDeviceKey = patch.BarkDeviceKey
	}
	if patch.BarkGroup != "" {
		dst.BarkGroup = patch.BarkGroup
	}
	if patch.BarkSound != "" {
		dst.BarkSound = patch.BarkSound
	}
}

func getWorkingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func mustJSON(cfg Config) []byte {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return append(b, '\n')
}
