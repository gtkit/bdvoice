package bdvoice

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"
)

// NewStreamTTSSession 创建一个公有云流式文本在线合成会话。
//
// 与 NewTTSSession（声音复刻 TTS）不同，此方法对接的是百度公有云的
// 流式文本在线合成服务，使用预置发音人（per）而非自定义复刻音色（voice_id）。
//
// 此方法会：
//  1. 建立 WebSocket 连接（地址：wss://aip.baidubce.com/ws/2.0/speech/publiccloudspeech/v1/tts）
//  2. 发送 system.start 初始化帧（携带合成参数）
//  3. 等待 system.started 确认响应
//  4. 启动后台 goroutine 接收音频数据
//
// per 是百度 TTS 发音人标识（如 "0" 表示度小美），通过 query 参数传递。
// cfg 可为 nil，使用服务端默认参数（语速5、音调5、音量5、mp3格式）。
//
// 返回的 TTSSession 与声音复刻 TTS 共享相同的操作接口：
// SendText、Finish、Read、Stream、Close。
//
// 注意事项：
//   - 单次 SendText 不超过 1000 个字符
//   - 文本建议不超过 2000 GBK 字节（约 1000 个汉字）
//   - 文本必须采用 UTF-8 编码
//   - 若发送文本无标点隔断，服务端会等待标点或至少 60 字后才开始合成
//   - 客户端超过 1 分钟不发送消息，服务端会主动断开连接
//   - 支持多音字标注，格式如：重(chong2)报集团
//
// 使用示例：
//
//	session, err := client.NewStreamTTSSession(ctx, "0", &bdvoice.StreamTTSConfig{
//	    Spd: 5,                          // 语速 0-15
//	    Pit: 5,                          // 音调 0-15
//	    Vol: 5,                          // 音量 0-15
//	    Aue: bdvoice.AudioEncodingMP3,   // 音频格式
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer session.Close()
//
//	// 发送文本（可多次调用）
//	_ = session.SendText(ctx, "你好，这是流式文本在线合成。")
//	_ = session.Finish(ctx)
//
//	// 读取音频（与 NewTTSSession 完全相同）
//	err = session.Stream(ctx, func(audio []byte) error {
//	    _, err := audioFile.Write(audio)
//	    return err
//	})
func (c *Client) NewStreamTTSSession(ctx context.Context, per string, cfg *StreamTTSConfig) (*TTSSession, error) {
	if per == "" {
		return nil, &ValidationError{Field: "per", Reason: "per (speaker ID) is required"}
	}

	// 构建 WebSocket URL
	wsURL, err := c.buildStreamTTSWSURL(ctx, per)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: build stream tts ws url: %w", err)
	}

	// 构建连接 header（API Key 模式需要 Authorization）
	header := make(http.Header)
	if c.authMode == AuthAPIKey {
		header.Set("Authorization", c.apiKey)
	}

	// 建立 WebSocket 连接
	dialer := websocket.Dialer{
		HandshakeTimeout: c.httpClient.Timeout,
	}
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		dialer.Proxy = transport.Proxy
		dialer.NetDialContext = transport.DialContext
		dialer.TLSClientConfig = transport.TLSClientConfig
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: stream tts websocket dial: %w", err)
	}

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64), // 缓冲 64 帧，避免阻塞接收
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}

	// 发送 system.start 初始化帧
	if err := session.sendStreamTTSStart(cfg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bdvoice: send stream tts start frame: %w", err)
	}

	// 等待 system.started 确认
	if err := session.waitStarted(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bdvoice: stream tts wait started: %w", err)
	}

	// 启动后台接收 goroutine
	session.readLoopStarted.Store(true)
	go session.readLoop()

	return session, nil
}

// buildStreamTTSWSURL 构建公有云流式 TTS 的 WebSocket URL。
//
// URL 格式：wss://aip.baidubce.com/ws/2.0/speech/publiccloudspeech/v1/tts?access_token=xxx&per=xxx
// 或（API Key 模式）：wss://aip.baidubce.com/ws/2.0/speech/publiccloudspeech/v1/tts?per=xxx
func (c *Client) buildStreamTTSWSURL(ctx context.Context, per string) (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	u.Scheme = "wss"
	u.Path = streamTTSWSEndpoint

	q := u.Query()
	q.Set("per", per)

	// 鉴权参数
	if c.authMode == AuthAccessToken {
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return "", err
		}
		q.Set("access_token", token)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// sendStreamTTSStart 发送公有云流式 TTS 的 system.start 初始化帧。
func (s *TTSSession) sendStreamTTSStart(cfg *StreamTTSConfig) error {
	frame := streamTTSStartFrame{
		Type:    wsTypeSystemStart,
		Payload: cfg,
	}
	return s.conn.WriteJSON(frame)
}
