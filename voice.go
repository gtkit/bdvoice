package bdvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CreateVoice 通过上传训练音频创建音色。
//
// 支持两种音频上传方式：
//   - AudioURL: 提供音频文件的公网链接
//   - AudioFile: 提供音频文件的 base64 编码内容
//
// 两者同时传入时，以 AudioFile 为准。
//
// 注意：通过此接口创建的音色，若 1 年内没有调用合成记录，该音色将被自动删除。
//
// 使用示例：
//
//	resp, err := client.CreateVoice(ctx, &bdvoice.CreateVoiceRequest{
//	    VoiceName: "my-voice",
//	    VoiceDesc: "温柔细腻的音色",
//	    AudioURL:  "https://example.com/audio.wav",
//	    Lang:      bdvoice.LangChinese,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("voice_id: %d\n", resp.Data.VoiceID)
func (c *Client) CreateVoice(ctx context.Context, req *CreateVoiceRequest) (*CreateVoiceResponse, error) {
	// 参数校验
	if err := req.validate(); err != nil {
		return nil, err
	}

	// 构建鉴权 query
	authQuery, err := c.buildAuthQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: auth failed: %w", err)
	}

	// 序列化请求体
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: marshal request: %w", err)
	}

	// 构建 URL
	reqURL := c.baseURL + createVoiceEndpoint
	if encoded := authQuery.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	// 发送请求
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bdvoice: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuthHeader(httpReq)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("bdvoice: read response: %w", err)
	}
	if len(respBody) > maxResponseBodyBytes {
		return nil, fmt.Errorf("bdvoice: create voice response exceeds %d bytes", maxResponseBodyBytes)
	}

	// HTTP 层面错误
	if httpResp.StatusCode != http.StatusOK {
		var apiErr APIError
		if jsonErr := json.Unmarshal(respBody, &apiErr); jsonErr == nil && apiErr.Code != 0 {
			apiErr.StatusCode = httpResp.StatusCode
			return nil, &apiErr
		}
		return nil, fmt.Errorf("bdvoice: unexpected status %d: %s",
			httpResp.StatusCode, string(respBody))
	}

	// 解析响应
	var resp CreateVoiceResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("bdvoice: decode response: %w", err)
	}

	// 业务层面错误（status != 0）
	if resp.Status != 0 {
		return &resp, &APIError{
			StatusCode: httpResp.StatusCode,
			Code:       resp.Status,
			Message:    resp.Message,
		}
	}

	return &resp, nil
}
