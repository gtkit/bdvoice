package bdvoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// sessionState 表示 TTS 会话的状态机。
type sessionState int32

const (
	stateActive   sessionState = iota // 连接已建立，可发送文本
	stateFinished                     // 已发送 finish 帧，等待剩余音频
	stateClosed                       // 连接已关闭
)

// TTSSession 代表一次 WebSocket TTS 流式合成会话。
//
// 使用流程：
//  1. 通过 Client.NewTTSSession 创建会话（自动完成连接和初始化）
//  2. 调用 SendText 发送待合成的文本（可多次调用，每次 ≤1000 字符）
//  3. 调用 Finish 通知服务端所有文本已发送
//  4. 通过 Read 或 Stream 读取合成的音频数据
//  5. 调用 Close 关闭连接
//
// TTSSession 不是并发安全的，不要在多个 goroutine 中同时操作同一个会话。
// 如果需要并发合成，请创建多个会话。
type TTSSession struct {
	conn            *websocket.Conn
	state           atomic.Int32  // sessionState
	sessionID       string        // 服务端返回的 session_id
	audioCh         chan []byte   // 音频数据通道
	done            chan struct{} // 读取 goroutine 退出信号
	closeCh         chan struct{} // Close 发出的停止信号
	closeOnce       sync.Once     // 确保 Close 只执行一次
	readLoopStarted atomic.Bool
	readErr         atomic.Pointer[sessionError]
}

type sessionError struct {
	err error
}

// NewTTSSession 创建一个新的 TTS 流式合成会话。
//
// 此方法会：
//  1. 建立 WebSocket 连接
//  2. 发送 system.start 初始化帧
//  3. 等待 system.started 确认
//  4. 启动后台 goroutine 接收音频数据
//
// voiceID 是通过 CreateVoice 获取的音色 ID。
// cfg 可为 nil，使用服务端默认参数。
//
// 使用示例：
//
//	session, err := client.NewTTSSession(ctx, 12345, &bdvoice.TTSConfig{
//	    MediaType: bdvoice.MediaMP3,
//	    Speed:     7,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer session.Close()
func (c *Client) NewTTSSession(ctx context.Context, voiceID int, cfg *TTSConfig) (*TTSSession, error) {
	// 构建 WebSocket URL
	wsURL, err := c.buildWSURL(ctx, voiceID)
	if err != nil {
		return nil, fmt.Errorf("bdvoice: build ws url: %w", err)
	}

	// 构建连接 header（API Key 模式需要 Authorization）
	header := make(map[string][]string)
	if c.authMode == AuthAPIKey {
		header["Authorization"] = []string{c.apiKey}
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
		return nil, fmt.Errorf("bdvoice: websocket dial: %w", err)
	}

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64), // 缓冲 64 帧，避免阻塞接收
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}

	// 发送初始化帧
	if err := session.sendStart(cfg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bdvoice: send start frame: %w", err)
	}

	// 等待初始化确认
	if err := session.waitStarted(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bdvoice: wait started: %w", err)
	}

	// 启动后台接收 goroutine
	session.readLoopStarted.Store(true)
	go session.readLoop()

	return session, nil
}

// buildWSURL 构建带鉴权参数的 WebSocket URL。
func (c *Client) buildWSURL(ctx context.Context, voiceID int) (string, error) {
	// 将 https:// 替换为 wss://
	baseURL := c.baseURL
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	u.Scheme = "wss"
	u.Path = ttsWSEndpoint

	q := u.Query()
	q.Set("voice_id", strconv.Itoa(voiceID))

	if c.idleTimeout != defaultIdleTimeout {
		q.Set("idle_timeout", strconv.Itoa(c.idleTimeout))
	}

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

// sendStart 发送 system.start 初始化帧。
func (s *TTSSession) sendStart(cfg *TTSConfig) error {
	frame := wsStartFrame{
		Type:    wsTypeSystemStart,
		Payload: cfg,
	}
	return s.conn.WriteJSON(frame)
}

// waitStarted 阻塞等待 system.started 响应。
func (s *TTSSession) waitStarted() error {
	_, msg, err := s.conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read started response: %w", err)
	}

	var resp wsResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		return fmt.Errorf("decode started response: %w", err)
	}

	if resp.Type != wsTypeSystemStarted || resp.Code != 0 {
		return &WebSocketError{
			Type:    resp.Type,
			Code:    resp.Code,
			Message: resp.Message,
		}
	}

	s.sessionID = resp.Headers["session_id"]
	return nil
}

// readLoop 后台持续读取 WebSocket 消息。
//
// 消息类型判断：
//   - 二进制消息 → 音频数据，写入 audioCh
//   - 文本消息 → 控制帧（system.finished / system.error）
//
// readLoop 在以下情况退出：
//   - 收到 system.finished 帧
//   - 收到 system.error 帧
//   - WebSocket 连接断开
func (s *TTSSession) readLoop() {
	s.readLoopStarted.Store(true)
	// setReadError 发生在 return 之前；defer close(audioCh) 在 return 时执行。
	// 读方一旦观察到 audioCh 已关闭，再读取 readErr 就能看到 return 前写入的错误。
	defer close(s.done)
	defer close(s.audioCh)

	for {
		msgType, data, err := s.conn.ReadMessage()
		if err != nil {
			// 连接关闭不视为错误（正常结束）
			if !websocket.IsCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway) {
				s.setReadError(fmt.Errorf("bdvoice: ws read: %w", err))
			}
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			// 音频数据
			audioCopy := make([]byte, len(data))
			copy(audioCopy, data)
			select {
			case s.audioCh <- audioCopy:
			case <-s.closeCh:
				return
			}

		case websocket.TextMessage:
			// 控制帧
			var resp wsResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				s.setReadError(fmt.Errorf("bdvoice: decode ws message: %w", err))
				return
			}

			switch resp.Type {
			case wsTypeSystemFinished:
				// 合成完成，正常退出
				return

			case wsTypeSystemError:
				s.setReadError(&WebSocketError{
					Type:    resp.Type,
					Code:    resp.Code,
					Message: resp.Message,
				})
				return
			}
		}
	}
}

// SendText 向服务端发送待合成的文本。
//
// 可多次调用以发送长文本，每次调用的文本不能超过 1000 个字符。
// 所有文本发送完毕后，必须调用 Finish 通知服务端。
//
// 返回 ErrSessionClosed 表示会话已关闭，
// 返回 ErrSessionFinished 表示已调用 Finish，不能再发送文本。
func (s *TTSSession) SendText(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkState(stateActive); err != nil {
		return err
	}

	// 字符数校验（按 rune 计算，中文算一个字符）
	if len([]rune(text)) > maxTextLength {
		return ErrTextTooLong
	}

	if text == "" {
		return &ValidationError{Field: "text", Reason: "text is empty"}
	}

	frame := wsTextFrame{
		Type:    wsTypeText,
		Payload: wsTextPayload{Text: text},
	}
	if err := s.conn.WriteJSON(frame); err != nil {
		return fmt.Errorf("bdvoice: send text: %w", err)
	}
	return nil
}

// Finish 通知服务端所有文本已发送完毕。
//
// 调用后，服务端会继续返回剩余的音频数据，
// 直到所有文本合成完成并发送 system.finished 帧。
//
// Finish 后不能再调用 SendText，但可以继续通过 Read 读取音频。
func (s *TTSSession) Finish(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkState(stateActive); err != nil {
		return err
	}

	frame := wsFinishFrame{Type: wsTypeSystemFinish}
	if err := s.conn.WriteJSON(frame); err != nil {
		return fmt.Errorf("bdvoice: send finish: %w", err)
	}

	s.state.Store(int32(stateFinished))
	return nil
}

// Read 读取一帧音频数据。
//
// 这是「拉模式」接口，适合需要精细控制音频处理流程的场景，
// 例如边接收边写入文件、边接收边转发给播放器等。
//
// 返回值：
//   - (data, nil): 成功读取一帧音频
//   - (nil, io.EOF): 所有音频已读取完毕（合成结束）
//   - (nil, error): 发生错误
//
// 使用示例：
//
//	for {
//	    data, err := session.Read()
//	    if err == io.EOF {
//	        break // 合成结束
//	    }
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    audioFile.Write(data)
//	}
func (s *TTSSession) Read() ([]byte, error) {
	data, ok := <-s.audioCh
	if !ok {
		if err := s.loadReadError(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return data, nil
}

// Stream 以回调方式持续读取音频数据，直到合成结束。
//
// 这是「推模式」接口，适合简单的流式处理场景，
// 例如直接将音频写入 HTTP response、gRPC stream 等。
// handler 返回 error 将中止读取并关闭连接。
//
// 与 Read 相比，Stream 的优势：
//   - 代码更简洁，不需要手动循环和 io.EOF 判断
//   - 适合"接收即转发"的流水线场景
//
// 使用示例：
//
//	err := session.Stream(ctx, func(audio []byte) error {
//	    _, err := w.Write(audio) // w 可以是 http.ResponseWriter
//	    return err
//	})
func (s *TTSSession) Stream(ctx context.Context, handler func(audio []byte) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case data, ok := <-s.audioCh:
			if !ok {
				if err := s.loadReadError(); err != nil {
					return err
				}
				return nil // 正常结束
			}
			if err := handler(data); err != nil {
				return fmt.Errorf("bdvoice: stream handler: %w", err)
			}
		}
	}
}

// SessionID 返回服务端分配的会话 ID。
// 可用于日志记录和问题排查。
func (s *TTSSession) SessionID() string {
	return s.sessionID
}

// Close 关闭 WebSocket 连接并释放资源。
//
// Close 是幂等的，多次调用是安全的。
// 调用 Close 后，Read 和 Stream 会立即返回。
func (s *TTSSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.state.Store(int32(stateClosed))
		if s.closeCh != nil {
			close(s.closeCh)
		}

		// 发送 WebSocket 关闭帧
		var writeErr error
		if s.conn != nil {
			writeErr = s.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)

			// 关闭底层连接
			closeErr = errors.Join(writeErr, s.conn.Close())
		}

		// 等待 readLoop 退出，避免 goroutine 泄漏
		if s.done != nil && s.readLoopStarted.Load() {
			<-s.done
		}
	})
	return closeErr
}

func (s *TTSSession) setReadError(err error) {
	if err == nil {
		return
	}
	s.readErr.CompareAndSwap(nil, &sessionError{err: err})
}

func (s *TTSSession) loadReadError() error {
	if err := s.readErr.Load(); err != nil {
		return err.err
	}
	return nil
}

// checkState 检查当前会话状态是否符合预期。
func (s *TTSSession) checkState(expected sessionState) error {
	current := sessionState(s.state.Load())
	switch current {
	case expected:
		return nil
	case stateClosed:
		return ErrSessionClosed
	case stateFinished:
		return ErrSessionFinished
	default:
		return fmt.Errorf("bdvoice: unexpected session state %d", current)
	}
}
