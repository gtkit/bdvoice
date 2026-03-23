package bdvoice

import (
	"errors"
	"fmt"
)

// ============================================================================
// 自定义错误类型
// ============================================================================

// APIError 封装百度 API 返回的业务错误。
// 包含 HTTP 状态码和 API 层面的错误码/消息。
type APIError struct {
	// StatusCode HTTP 响应状态码。
	StatusCode int `json:"-"`

	// Code API 错误码（如 216100、216404 等）。
	Code int `json:"code"`

	// Message API 错误消息。
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bdvoice: api error (http=%d, code=%d): %s",
		e.StatusCode, e.Code, e.Message)
}

// OAuthError 封装 OAuth 鉴权过程中返回的错误。
type OAuthError struct {
	// StatusCode HTTP 响应状态码。
	StatusCode int `json:"-"`

	// ErrorCode OAuth 错误码（如 "invalid_client"）。
	ErrorCode string `json:"error"`

	// Description 错误描述。
	Description string `json:"error_description"`
}

func (e *OAuthError) Error() string {
	return fmt.Sprintf("bdvoice: oauth error (http=%d, code=%s): %s",
		e.StatusCode, e.ErrorCode, e.Description)
}

// ValidationError 表示客户端参数校验失败。
type ValidationError struct {
	Field  string // 校验失败的字段名
	Reason string // 失败原因
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("bdvoice: validation error on field %q: %s", e.Field, e.Reason)
}

// WebSocketError 封装 WebSocket 通信过程中的服务端错误。
type WebSocketError struct {
	Type    string // 消息类型（如 system.error、system.started）
	Code    int    // 错误码
	Message string // 错误消息
}

func (e *WebSocketError) Error() string {
	return fmt.Sprintf("bdvoice: websocket error (type=%s, code=%d): %s",
		e.Type, e.Code, e.Message)
}

// ============================================================================
// 哨兵错误
// ============================================================================

var (
	// ErrSessionClosed 表示 TTS 会话已关闭。
	ErrSessionClosed = errors.New("bdvoice: tts session is closed")

	// ErrSessionFinished 表示 TTS 合成已结束（已发送 finish 帧）。
	ErrSessionFinished = errors.New("bdvoice: tts session already finished")

	// ErrTextTooLong 表示单次发送的文本超过 1000 字符。
	ErrTextTooLong = errors.New("bdvoice: text exceeds 1000 characters limit")

	// ErrNoAuth 表示未配置任何鉴权方式。
	ErrNoAuth = errors.New("bdvoice: no authentication configured, use WithAPIKey or WithClientCredentials")
)

// ============================================================================
// 错误类型判断辅助（Go 1.26 errors.AsType）
// ============================================================================

// IsAPIError 从 err 链中提取 *APIError。
// 用法：
//
//	if apiErr, ok := bdvoice.IsAPIError(err); ok {
//	    log.Printf("API error code: %d", apiErr.Code)
//	}
func IsAPIError(err error) (*APIError, bool) {
	return errors.AsType[*APIError](err)
}

// IsOAuthError 从 err 链中提取 *OAuthError。
func IsOAuthError(err error) (*OAuthError, bool) {
	return errors.AsType[*OAuthError](err)
}

// IsValidationError 从 err 链中提取 *ValidationError。
func IsValidationError(err error) (*ValidationError, bool) {
	return errors.AsType[*ValidationError](err)
}

// IsWebSocketError 从 err 链中提取 *WebSocketError。
func IsWebSocketError(err error) (*WebSocketError, bool) {
	return errors.AsType[*WebSocketError](err)
}
