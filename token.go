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

	// 慢路径：singleflight 去重刷新。
	// 刷新动作使用独立 context，避免首个调用者的取消信号连带失败其他等待者；
	// 每个调用方仍然通过自己的 ctx 决定是否继续等待结果。
	resultCh := c.tokenGroup.DoChan("token", func() (any, error) {
		// double-check：进入 singleflight 后再次检查，
		// 可能在排队期间已被其他 goroutine 刷新。
		if cached := c.token.Load(); cached.valid() {
			return cached.AccessToken, nil
		}
		refreshCtx, cancel := c.tokenRefreshContext(ctx)
		defer cancel()
		return c.refreshToken(refreshCtx)
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return "", result.Err
		}
		return result.Val.(string), nil
	}
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
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bdvoice: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("bdvoice: read token response: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return "", fmt.Errorf("bdvoice: token response exceeds %d bytes", maxResponseBodyBytes)
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

func (c *Client) tokenRefreshContext(ctx context.Context) (context.Context, context.CancelFunc) {
	refreshCtx := context.WithoutCancel(ctx)
	timeout := c.httpClient.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second // 兜底
	}
	// 与 http.Client.Timeout 对齐，并在 Timeout=0 时仍限制单次刷新时长，避免刷新协程悬挂。
	return context.WithTimeout(refreshCtx, timeout)
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
