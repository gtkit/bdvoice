// Package bdvoice 提供百度智能云大模型声音复刻 API 的 Go SDK。
//
// 支持两种核心能力：
//   - 创建音色（REST API）：上传音频创建自定义音色
//   - 流式语音合成（WebSocket TTS）：基于已创建音色进行实时语音合成
//
// 鉴权方式支持 access_token（OAuth）和 API Key 两种模式。
package bdvoice

import "time"

// ============================================================================
// 鉴权相关
// ============================================================================

// AuthMode 定义鉴权方式。
type AuthMode int

const (
	// AuthAccessToken 使用 client_id + client_secret 获取 access_token 鉴权。
	// SDK 自动管理 token 的获取和续期。
	AuthAccessToken AuthMode = iota

	// AuthAPIKey 使用 API Key 通过 Authorization header 鉴权。
	// 无需 token 管理，适合快速集成。
	AuthAPIKey
)

// tokenCache 是 access_token 的内部缓存结构。
type tokenCache struct {
	AccessToken string    `json:"access_token"`
	ExpiresIn   int       `json:"expires_in"` // 服务端返回的过期秒数
	ExpiresAt   time.Time `json:"-"`          // 本地计算的过期时间点
}

// valid 判断 token 是否仍然有效。
// 预留 5 分钟缓冲，避免在临界点使用即将过期的 token。
func (t *tokenCache) valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	return time.Until(t.ExpiresAt) > 5*time.Minute
}

// tokenErrorResponse 是 OAuth 请求失败时的错误响应。
type tokenErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// ============================================================================
// 创建音色
// ============================================================================

// Lang 定义支持的语种常量。
const (
	LangChinese  = "zh" // 中英语（默认）
	LangJapanese = "ja" // 日语
)

// CreateVoiceRequest 是创建音色的请求参数。
//
// AudioURL 和 AudioFile 二选一：
//   - AudioURL: 音频文件的公网链接，支持 wav/mp3/m4a/ogg/aac，5M 以内，5~20 秒
//   - AudioFile: 音频文件的 base64 编码内容
//
// 两者同时传入时，以 AudioFile 为准。
type CreateVoiceRequest struct {
	// VoiceName 音色名称，同一用户下不可重复。必填。
	VoiceName string `json:"voice_name"`

	// VoiceDesc 音色说明。可选。
	VoiceDesc string `json:"voice_desc,omitzero"`

	// Lang 音色语种，支持 "zh"（中英语）和 "ja"（日语）。
	// 不填默认为 "zh"。日语音色建议使用 10~30 秒日语音频。
	Lang string `json:"lang,omitzero"`

	// AudioURL 音频文件链接。与 AudioFile 二选一。
	AudioURL string `json:"audio_url,omitzero"`

	// AudioFile 音频文件 base64 编码。与 AudioURL 二选一。
	// 支持方言复刻：河南话、上海话、四川话、湖南话、贵州话。
	AudioFile string `json:"audio_file,omitzero"`

	// TextID 文本 ID。使用自定义文本复刻时无需填写。
	TextID string `json:"text_id,omitzero"`
}

// validate 校验请求参数的基本合法性。
func (r *CreateVoiceRequest) validate() error {
	if r == nil {
		return &ValidationError{Field: "request", Reason: "request is nil"}
	}
	if r.VoiceName == "" {
		return &ValidationError{Field: "voice_name", Reason: "voice_name is required"}
	}
	if r.AudioURL == "" && r.AudioFile == "" {
		return &ValidationError{Field: "audio", Reason: "either audio_url or audio_file is required"}
	}
	if r.Lang != "" && r.Lang != LangChinese && r.Lang != LangJapanese {
		return &ValidationError{Field: "lang", Reason: "lang must be 'zh' or 'ja'"}
	}
	return nil
}

// CreateVoiceResponse 是创建音色的响应。
type CreateVoiceResponse struct {
	// Status 状态码，0 表示成功，其他为异常。
	Status int `json:"status"`

	// Message 错误信息（成功时可能为空）。
	Message string `json:"message"`

	// Data 成功时返回音色信息。
	Data *CreateVoiceData `json:"data,omitzero"`
}

// CreateVoiceData 是创建音色成功后返回的数据。
type CreateVoiceData struct {
	// VoiceID 音色唯一 ID，后续合成时使用。
	VoiceID int `json:"voice_id"`
}

// ============================================================================
// WebSocket TTS 流式合成
// ============================================================================

// Dialect 定义支持的方言常量。
const (
	DialectShanghai = "wuu-CN-shanghai" // 上海话
	DialectHenan    = "zh-CN-henan"     // 河南话
	DialectSichuan  = "zh-CN-sichuan"   // 四川话
	DialectHunan    = "zh-CN-hunan"     // 湖南话
	DialectGuizhou  = "zh-CN-guizhou"   // 贵州话
)

// MediaType 定义支持的音频格式常量。
const (
	MediaWAV = "wav"
	MediaMP3 = "mp3"
	MediaPCM = "pcm"
)

// TTSConfig 是 WebSocket TTS 合成的参数配置。
// 所有字段均为可选，使用零值时 SDK 不会发送该参数，由服务端使用默认值。
type TTSConfig struct {
	// Lang 合成语种。不填默认为创建音色时选择的语种。
	// 合成方言必须选 "zh"，合成日语必须选 "ja"。
	Lang string `json:"lang,omitzero"`

	// Dialect 方言类型，如 DialectHenan、DialectShanghai 等。
	Dialect string `json:"dialect,omitzero"`

	// MediaType 音频格式：wav / mp3 / pcm。默认 wav。
	MediaType string `json:"media_type,omitzero"`

	// SampleRate 采样率：8000 / 16000 / 24000。
	SampleRate int `json:"sample_rate,omitzero"`

	// Pitch 音调，取值 0-15，默认 5。
	Pitch int `json:"pitch,omitzero"`

	// Volume 音量，取值 0-15，默认 5。
	Volume int `json:"volume,omitzero"`

	// Speed 语速，取值 0-15，默认 5。
	Speed int `json:"speed,omitzero"`
}

// wsStartFrame 是 WebSocket 初始化帧。
type wsStartFrame struct {
	Type    string     `json:"type"`
	Payload *TTSConfig `json:"payload,omitzero"`
}

// wsTextFrame 是 WebSocket 文本发送帧。
type wsTextFrame struct {
	Type    string        `json:"type"`
	Payload wsTextPayload `json:"payload"`
}

type wsTextPayload struct {
	Text string `json:"text"`
}

// wsFinishFrame 是 WebSocket 结束帧。
type wsFinishFrame struct {
	Type string `json:"type"`
}

// wsResponse 是 WebSocket 服务端响应的统一结构。
type wsResponse struct {
	Type    string            `json:"type"`
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Headers map[string]string `json:"headers,omitzero"`
}

// WebSocket 消息类型常量。
const (
	wsTypeSystemStart    = "system.start"
	wsTypeSystemStarted  = "system.started"
	wsTypeText           = "text"
	wsTypeSystemFinish   = "system.finish"
	wsTypeSystemFinished = "system.finished"
	wsTypeSystemError    = "system.error"
)

// ============================================================================
// 默认值
// ============================================================================

const (
	defaultBaseURL     = "https://aip.baidubce.com"
	defaultIdleTimeout = 60 // 秒

	// tokenEndpoint 是 OAuth 2.0 获取 access_token 的路径。
	tokenEndpoint = "/oauth/2.0/token"

	// createVoiceEndpoint 是创建音色的 API 路径。
	createVoiceEndpoint = "/rest/2.0/speech/publiccloudspeech/v1/voice/clone/create"

	// ttsWSEndpoint 是 WebSocket TTS 的路径。
	ttsWSEndpoint = "/ws/2.0/speech/publiccloudspeech/v1/voice/clone/tts"

	// maxTextLength 是单次发送文本的最大字符数。
	maxTextLength = 1000
)
