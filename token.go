package bdvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// getAccessToken 获取有效的 access_token。
//
// 内部逻辑：
//  1. 先检查缓存，若 token 仍有效则直接返回（atomic 无锁读）
//  2. 若 token 过期或不存在，通过 singleflight 发起刷新
//  3. singleflight 确保并发场景下只有一个请求真正发出
func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	// 快路径：缓存有效直接返回
	if cached := c.token.Load(); cached.valid() {
		return cached.AccessToken, nil
	}

	// 慢路径：singleflight 去重刷新
	v, err, _ := c.tokenGroup.Do("token", func() (any, error) {
		// double-check：进入 singleflight 后再次检查，
		// 可能在排队期间已被其他 goroutine 刷新。
		if cached := c.token.Load(); cached.valid() {
			return cached.AccessToken, nil
		}
		return c.refreshToken(ctx)
	})
	if err != nil {
		return "", err
	}

	return v.(string), nil
}

// refreshToken 向百度 OAuth 服务请求新的 access_token。
func (c *Client) refreshToken(ctx context.Context) (string, error) {
	params := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}

	reqURL := c.baseURL + tokenEndpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("bdvoice: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bdvoice: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("bdvoice: read token response: %w", err)
	}

	// 检查 OAuth 错误响应
	if resp.StatusCode != http.StatusOK {
		var oauthErr OAuthError
		if jsonErr := json.Unmarshal(body, &oauthErr); jsonErr == nil && oauthErr.ErrorCode != "" {
			oauthErr.StatusCode = resp.StatusCode
			return "", &oauthErr
		}
		return "", fmt.Errorf("bdvoice: token request returned status %d: %s",
			resp.StatusCode, string(body))
	}

	// 解析成功响应
	var cache tokenCache
	if err := json.Unmarshal(body, &cache); err != nil {
		return "", fmt.Errorf("bdvoice: decode token response: %w", err)
	}

	if cache.AccessToken == "" {
		return "", fmt.Errorf("bdvoice: token response missing access_token")
	}

	// 计算本地过期时间
	cache.ExpiresAt = time.Now().Add(time.Duration(cache.ExpiresIn) * time.Second)

	// 原子写入缓存
	c.token.Store(&cache)

	return cache.AccessToken, nil
}

// buildAuthQuery 构建带鉴权参数的 URL query string。
// access_token 模式：追加 access_token=xxx
// API Key 模式：返回空（鉴权通过 header 完成）
func (c *Client) buildAuthQuery(ctx context.Context) (url.Values, error) {
	q := url.Values{}
	switch c.authMode {
	case AuthAccessToken:
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		q.Set("access_token", token)
	case AuthAPIKey:
		// API Key 模式不需要 query 参数，通过 header 鉴权
	}
	return q, nil
}

// setAuthHeader 为 HTTP 请求设置 API Key 鉴权头。
// 仅在 AuthAPIKey 模式下生效。
func (c *Client) setAuthHeader(req *http.Request) {
	if c.authMode == AuthAPIKey {
		req.Header.Set("Authorization", c.apiKey)
	}
}
