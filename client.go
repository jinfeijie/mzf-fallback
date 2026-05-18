package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type PaymentChannel struct {
	ID        int64                  `json:"id"`
	UID       int64                  `json:"uid"`
	Name      string                 `json:"name"`
	Code      string                 `json:"code"`
	Status    int                    `json:"status"`
	Online    int                    `json:"online"`
	Channel   string                 `json:"channel_name"`
	PayType   string                 `json:"pay_type"`
	Extra     map[string]any         `json:"-"`
	Raw       map[string]interface{} `json:"-"`
}

type ChannelClient struct {
	baseURL string
	client  *http.Client
}

func NewChannelClient(baseURL string, timeoutSec int) *ChannelClient {
	return &ChannelClient{
		baseURL: baseURL,
		client: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

type channelListResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		List []map[string]any `json:"list"`
	} `json:"data"`
}

type switchResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func (c *ChannelClient) ListChannels(cfg Config) ([]PaymentChannel, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/api/channel/account/list"
	q := u.Query()
	q.Set("query", "")
	q.Set("status", "0")
	q.Set("pay_type", "")
	q.Set("channel_code", "")
	q.Set("online", "0")
	q.Set("page", "1")
	q.Set("limit", "20")
	q.Set("_t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("authorization", cfg.Authorization)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("列表接口鉴权失败: %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("列表接口状态异常: %d %s", resp.StatusCode, string(body))
	}

	var out channelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 200 {
		return nil, errors.New(out.Message)
	}

	channels := make([]PaymentChannel, 0, len(out.Data.List))
	for _, item := range out.Data.List {
		channels = append(channels, decodeChannel(item))
	}
	return channels, nil
}

func (c *ChannelClient) SwitchStatus(cfg Config, id int64, status int) error {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return err
	}
	u.Path = "/api/channel/account/switch-status"
	q := u.Query()
	q.Set("_t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	u.RawQuery = q.Encode()

	payload, err := json.Marshal(map[string]any{"id": id, "status": status})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", cfg.Authorization)
	req.Header.Set("content-type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("开关接口鉴权失败: %d", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("开关接口状态异常: %d %s", resp.StatusCode, string(body))
	}

	var out switchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.Code != 200 {
		return errors.New(out.Message)
	}
	return nil
}

func decodeChannel(item map[string]any) PaymentChannel {
	return PaymentChannel{
		ID:      toInt64(item["id"]),
		UID:     toInt64(item["uid"]),
		Name:    toString(item["name"]),
		Code:    toString(item["code"]),
		Status:  int(toInt64(item["status"])),
		Online:  int(toInt64(item["online"])),
		Channel: toString(item["channel_name"]),
		PayType: toString(item["pay_type"]),
		Raw:     item,
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	case int:
		return int64(t)
	case int64:
		return t
	case int32:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}
