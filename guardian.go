package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type RuntimeState string

const (
	StatePrimaryActive      RuntimeState = "PRIMARY_ACTIVE"
	StateSwitchingFallback  RuntimeState = "SWITCHING_TO_FALLBACK"
	StateFallbackActive     RuntimeState = "FALLBACK_ACTIVE"
	StateSwitchingPrimary   RuntimeState = "SWITCHING_TO_PRIMARY"
)

type Guardian struct {
	store       *ConfigStore
	client      *ChannelClient
	mu          sync.RWMutex
	runtime     RuntimeSnapshot
	stopCh      chan struct{}
	running     bool
	downCount   int
	recoverCount int
	lastAlertAt map[string]time.Time
}

type RuntimeSnapshot struct {
	State        RuntimeState     `json:"state"`
	Running      bool             `json:"running"`
	LastCheckAt  string           `json:"last_check_at"`
	LastSwitchAt string           `json:"last_switch_at"`
	LastError    string           `json:"last_error"`
	LastAction   string           `json:"last_action"`
	Primary      *PaymentChannel  `json:"primary,omitempty"`
	Fallback     *PaymentChannel  `json:"fallback,omitempty"`
}

type StatusSnapshot struct {
	RuntimeSnapshot
	Config Config `json:"config"`
}

func NewGuardian(store *ConfigStore) *Guardian {
	cfg, _ := store.Load()
	return &Guardian{
		store:       store,
		client:      NewChannelClient(cfg.BaseURL, cfg.HTTPTimeoutSec),
		runtime:     RuntimeSnapshot{State: StatePrimaryActive},
		stopCh:      make(chan struct{}),
		lastAlertAt: map[string]time.Time{},
	}
}

func (g *Guardian) Start() {
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return
	}
	g.running = true
	g.runtime.Running = true
	g.mu.Unlock()
	go g.loop()
}

func (g *Guardian) Stop() {
	g.mu.Lock()
	if !g.running {
		g.mu.Unlock()
		return
	}
	g.running = false
	g.runtime.Running = false
	close(g.stopCh)
	g.stopCh = make(chan struct{})
	g.mu.Unlock()
}

func (g *Guardian) Snapshot() StatusSnapshot {
	cfg, _ := g.store.Load()
	g.mu.RLock()
	defer g.mu.RUnlock()
	return StatusSnapshot{RuntimeSnapshot: g.runtime, Config: cfg}
}

func (g *Guardian) RunOnce() (StatusSnapshot, error) {
	if err := g.checkOnce(false); err != nil {
		return g.Snapshot(), err
	}
	return g.Snapshot(), nil
}

func (g *Guardian) ManualSwitch(id int64, status int) (map[string]any, error) {
	cfg, err := g.store.Load()
	if err != nil {
		return nil, err
	}
	g.client = NewChannelClient(cfg.BaseURL, cfg.HTTPTimeoutSec)
	if err := g.client.SwitchStatus(cfg, id, status); err != nil {
		return nil, err
	}
	channels, err := g.client.ListChannels(cfg)
	if err != nil {
		return nil, err
	}
	primary, fallback, err := pickChannels(cfg, channels)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.runtime.LastCheckAt = nowString()
	g.runtime.LastAction = "manual_switch"
	g.runtime.LastError = ""
	g.runtime.Primary = &primary
	g.runtime.Fallback = &fallback
	if fallback.Status == 1 {
		g.runtime.State = StateFallbackActive
	} else {
		g.runtime.State = StatePrimaryActive
	}
	g.mu.Unlock()
	return map[string]any{
		"ok": true,
		"id": id,
		"status": status,
		"primary": primary,
		"fallback": fallback,
	}, nil
}

func (g *Guardian) TestBark(cfg Config) error {
	cfg.normalize()
	return g.sendBark(cfg, "支付守护测试通知", "Bark 测试通知发送成功。", "info", "test-bark")
}

func (g *Guardian) loop() {
	for {
		select {
		case <-g.stopCh:
			return
		default:
		}

		if err := g.checkOnce(false); err != nil {
			log.Printf("guardian loop error: %v", err)
			g.setError(err.Error())
			g.alertException(err)
		}

		cfg, err := g.store.Load()
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		time.Sleep(time.Duration(cfg.PollIntervalSec) * time.Second)
	}
}

func (g *Guardian) checkOnce(manual bool) error {
	cfg, err := g.store.Load()
	if err != nil {
		return err
	}
	g.client = NewChannelClient(cfg.BaseURL, cfg.HTTPTimeoutSec)

	channels, err := g.client.ListChannels(cfg)
	if err != nil {
		g.bumpBackoff(cfg)
		g.setError(err.Error())
		if isAuthErr(err) {
			_ = g.sendBark(cfg, "支付守护鉴权失败", err.Error(), "critical", "auth-failed")
		}
		return err
	}
	primary, fallback, err := pickChannels(cfg, channels)
	if err != nil {
		g.setError(err.Error())
		_ = g.sendBark(cfg, "支付守护渠道缺失", err.Error(), "critical", "channel-missing")
		return err
	}

	available := primary.Status == 1 && primary.Online == 1
	fallbackEnabled := fallback.Status == 1

	g.mu.Lock()
	g.runtime.LastCheckAt = nowString()
	g.runtime.Primary = &primary
	g.runtime.Fallback = &fallback
	g.runtime.LastError = ""
	g.mu.Unlock()

	if available {
		g.downCount = 0
		g.recoverCount++
	} else {
		g.recoverCount = 0
		g.downCount++
	}

	if !available && g.downCount >= cfg.DownConfirmTimes {
		if !fallbackEnabled {
			if err := g.switchToFallback(cfg, primary, fallback, manual); err != nil {
				g.setError(err.Error())
				return err
			}
		}
		g.setState(StateFallbackActive, "switch_to_fallback")
		return nil
	}

	if available && fallbackEnabled && g.recoverCount >= cfg.RecoverConfirmTimes {
		if err := g.switchToPrimary(cfg, primary, fallback, manual); err != nil {
			g.setError(err.Error())
			return err
		}
		g.setState(StatePrimaryActive, "switch_to_primary")
		return nil
	}

	if fallbackEnabled {
		g.setState(StateFallbackActive, "checked")
	} else {
		g.setState(StatePrimaryActive, "checked")
	}
	return nil
}

func (g *Guardian) switchToFallback(cfg Config, primary, fallback PaymentChannel, manual bool) error {
	g.setState(StateSwitchingFallback, "switch_to_fallback")
	if err := g.client.SwitchStatus(cfg, fallback.ID, 1); err != nil {
		return err
	}
	channels, err := g.client.ListChannels(cfg)
	if err != nil {
		return err
	}
	_, refreshedFallback, err := pickChannels(cfg, channels)
	if err != nil {
		return err
	}
	if refreshedFallback.Status != 1 {
		return errors.New("开启企业码后状态未生效")
	}
	g.setSwitchTime()
	if !manual {
		_ = g.sendBark(cfg, "支付兜底已启用", fmt.Sprintf("个人码 status=%d online=%d；企业码已开启。", primary.Status, primary.Online), "warning", "switch-to-fallback")
	}
	return nil
}

func (g *Guardian) switchToPrimary(cfg Config, primary, fallback PaymentChannel, manual bool) error {
	g.setState(StateSwitchingPrimary, "switch_to_primary")
	if primary.Status != 1 {
		if err := g.client.SwitchStatus(cfg, primary.ID, 1); err != nil {
			return err
		}
	}
	if err := g.client.SwitchStatus(cfg, fallback.ID, 2); err != nil {
		return err
	}
	channels, err := g.client.ListChannels(cfg)
	if err != nil {
		return err
	}
	_, refreshedFallback, err := pickChannels(cfg, channels)
	if err != nil {
		return err
	}
	if refreshedFallback.Status != 2 {
		return errors.New("关闭企业码后状态未生效")
	}
	g.setSwitchTime()
	if !manual {
		_ = g.sendBark(cfg, "支付主路已恢复", fmt.Sprintf("个人码 status=%d online=%d；企业码已关闭。", primary.Status, primary.Online), "active", "switch-to-primary")
	}
	return nil
}

func (g *Guardian) sendBark(cfg Config, title, body, level, dedupeKey string) error {
	if cfg.BarkDeviceKey == "" {
		return nil
	}
	if dedupeKey != "" && !g.allowAlert(dedupeKey, 5*time.Minute) {
		return nil
	}
	u, err := url.Parse(strings.TrimRight(cfg.BarkBaseURL, "/") + "/" + url.PathEscape(cfg.BarkDeviceKey) + "/" + url.PathEscape(title) + "/" + url.PathEscape(body))
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("group", cfg.BarkGroup)
	q.Set("level", level)
	if cfg.BarkSound != "" {
		q.Set("sound", cfg.BarkSound)
	}
	u.RawQuery = q.Encode()
	resp, err := http.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bark 返回异常: %d", resp.StatusCode)
	}
	return nil
}

func (g *Guardian) alertException(err error) {
	cfg, loadErr := g.store.Load()
	if loadErr != nil {
		return
	}
	_ = g.sendBark(cfg, "支付守护异常", err.Error(), "critical", "loop-exception")
}

func (g *Guardian) allowAlert(key string, cooldown time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if last, ok := g.lastAlertAt[key]; ok && now.Sub(last) < cooldown {
		return false
	}
	g.lastAlertAt[key] = now
	return true
}

func (g *Guardian) setError(msg string) {
	g.mu.Lock()
	g.runtime.LastError = msg
	g.mu.Unlock()
}

func (g *Guardian) setState(state RuntimeState, action string) {
	g.mu.Lock()
	g.runtime.State = state
	g.runtime.LastAction = action
	g.mu.Unlock()
}

func (g *Guardian) setSwitchTime() {
	g.mu.Lock()
	g.runtime.LastSwitchAt = nowString()
	g.mu.Unlock()
}

func (g *Guardian) bumpBackoff(cfg Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runtime.State == StateSwitchingFallback || g.runtime.State == StateSwitchingPrimary {
		return
	}
}

func pickChannels(cfg Config, channels []PaymentChannel) (PaymentChannel, PaymentChannel, error) {
	var primary, fallback *PaymentChannel
	for i := range channels {
		c := channels[i]
		if c.Code == cfg.PrimaryCode {
			copy := c
			primary = &copy
		}
		if c.Code == cfg.FallbackCode {
			copy := c
			fallback = &copy
		}
	}
	if primary == nil || fallback == nil {
		return PaymentChannel{}, PaymentChannel{}, errors.New("未找到个人码或企业码记录")
	}
	return *primary, *fallback, nil
}

func isAuthErr(err error) bool {
	return strings.Contains(err.Error(), "鉴权失败")
}

func nowString() string {
	return time.Now().Format(time.RFC3339)
}
