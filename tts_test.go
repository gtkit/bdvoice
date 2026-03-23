package bdvoice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// mockWSServer 创建一个模拟 WebSocket TTS 服务端。
// handler 接收升级后的 WebSocket 连接，由调用者控制交互逻辑。
func mockWSServer(t *testing.T, handler func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
}

// clientWithWSServer 创建一个连接到 mock WebSocket 服务的 Client。
// 将 httptest server URL (http://) 转为 Client 可用的形式。
func clientWithWSServer(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	// httptest 使用 http://，我们需要让 buildWSURL 把 scheme 改为 wss://
	// 但实际连接时 gorilla/websocket 支持 ws://
	baseURL := strings.Replace(server.URL, "http://", "https://", 1)
	client, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(baseURL),
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestTTSSession_FullFlow(t *testing.T) {
	audioData := []byte("fake-audio-data-frame-1")
	audioData2 := []byte("fake-audio-data-frame-2")

	server := mockWSServer(t, func(conn *websocket.Conn) {
		// 1. 读取 system.start
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read start: %v", err)
			return
		}
		var start wsStartFrame
		json.Unmarshal(msg, &start)
		if start.Type != wsTypeSystemStart {
			t.Errorf("type = %q, want %q", start.Type, wsTypeSystemStart)
		}

		// 2. 回复 system.started
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
			Headers: map[string]string{"session_id": "test-session-001"},
		})

		// 3. 读取文本帧
		_, msg, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read text: %v", err)
			return
		}
		var text wsTextFrame
		json.Unmarshal(msg, &text)
		if text.Payload.Text != "你好世界" {
			t.Errorf("text = %q, want %q", text.Payload.Text, "你好世界")
		}

		// 4. 读取 system.finish
		_, msg, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read finish: %v", err)
			return
		}
		var finish wsFinishFrame
		json.Unmarshal(msg, &finish)
		if finish.Type != wsTypeSystemFinish {
			t.Errorf("type = %q, want %q", finish.Type, wsTypeSystemFinish)
		}

		// 5. 发送音频数据
		conn.WriteMessage(websocket.BinaryMessage, audioData)
		conn.WriteMessage(websocket.BinaryMessage, audioData2)

		// 6. 发送 system.finished
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemFinished,
			Code:    0,
			Message: "success",
			Headers: map[string]string{"session_id": "test-session-001"},
		})
	})
	defer server.Close()

	client := clientWithWSServer(t, server)

	// buildWSURL 会生成 wss:// URL，但 mock server 是 ws://
	// 我们需要 override buildWSURL 的行为。直接测试 session 流程：
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, err := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}

	// 发送 start
	if err := session.sendStart(&TTSConfig{MediaType: MediaMP3}); err != nil {
		t.Fatalf("sendStart: %v", err)
	}
	if err := session.waitStarted(); err != nil {
		t.Fatalf("waitStarted: %v", err)
	}
	if session.sessionID != "test-session-001" {
		t.Errorf("session_id = %q, want %q", session.sessionID, "test-session-001")
	}

	// 启动 readLoop
	go session.readLoop()

	// 发送文本
	ctx := t.Context()
	if err := session.SendText(ctx, "你好世界"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	// Finish
	if err := session.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// 读取音频
	var received [][]byte
	for {
		data, err := session.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		received = append(received, data)
	}

	if len(received) != 2 {
		t.Fatalf("received %d frames, want 2", len(received))
	}
	if string(received[0]) != string(audioData) {
		t.Errorf("frame 0 = %q, want %q", received[0], audioData)
	}
	if string(received[1]) != string(audioData2) {
		t.Errorf("frame 1 = %q, want %q", received[1], audioData2)
	}

	_ = client // 确保 client 被使用
	session.Close()
}

func TestTTSSession_Stream(t *testing.T) {
	frames := [][]byte{
		[]byte("audio-1"),
		[]byte("audio-2"),
		[]byte("audio-3"),
	}

	server := mockWSServer(t, func(conn *websocket.Conn) {
		// 读取 start
		conn.ReadMessage()
		// 回复 started
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		// 读取 text
		conn.ReadMessage()
		// 读取 finish
		conn.ReadMessage()
		// 发送音频
		for _, f := range frames {
			conn.WriteMessage(websocket.BinaryMessage, f)
		}
		// 发送 finished
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemFinished,
			Code:    0,
			Message: "success",
		})
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, err := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}

	session.sendStart(nil)
	session.waitStarted()
	go session.readLoop()

	ctx := t.Context()
	session.SendText(ctx, "test")
	session.Finish(ctx)

	var received [][]byte
	err = session.Stream(ctx, func(audio []byte) error {
		received = append(received, audio)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(received) != len(frames) {
		t.Errorf("received %d frames, want %d", len(received), len(frames))
	}

	session.Close()
}

func TestTTSSession_ServerError(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		conn.ReadMessage() // text
		// 返回服务端错误
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemError,
			Code:    216604,
			Message: "Open api usage limit reached",
		})
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}

	session.sendStart(nil)
	session.waitStarted()
	go session.readLoop()

	session.SendText(t.Context(), "test")

	// 读取应返回错误
	_, err := session.Read()
	if err == nil {
		t.Fatal("expected error")
	}
	wsErr, ok := IsWebSocketError(err)
	if !ok {
		t.Fatalf("expected WebSocketError, got %T: %v", err, err)
	}
	if wsErr.Code != 216604 {
		t.Errorf("code = %d, want 216604", wsErr.Code)
	}

	session.Close()
}

func TestTTSSession_SendTextValidation(t *testing.T) {
	// 创建一个不需要真实连接的 session 来测试校验逻辑
	session := &TTSSession{
		audioCh: make(chan []byte, 1),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}
	// 状态设为 active
	session.state.Store(int32(stateActive))

	ctx := t.Context()

	t.Run("empty text", func(t *testing.T) {
		err := session.SendText(ctx, "")
		if err == nil {
			t.Fatal("expected error for empty text")
		}
		if _, ok := IsValidationError(err); !ok {
			t.Errorf("expected ValidationError, got %T", err)
		}
	})

	t.Run("text too long", func(t *testing.T) {
		longText := strings.Repeat("测", 1001)
		err := session.SendText(ctx, longText)
		if err != ErrTextTooLong {
			t.Errorf("expected ErrTextTooLong, got %v", err)
		}
	})
}

func TestTTSSession_StateTransitions(t *testing.T) {
	session := &TTSSession{
		audioCh: make(chan []byte, 1),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}

	t.Run("finished state rejects SendText", func(t *testing.T) {
		session.state.Store(int32(stateFinished))
		err := session.SendText(t.Context(), "test")
		if err != ErrSessionFinished {
			t.Errorf("expected ErrSessionFinished, got %v", err)
		}
	})

	t.Run("closed state rejects SendText", func(t *testing.T) {
		session.state.Store(int32(stateClosed))
		err := session.SendText(t.Context(), "test")
		if err != ErrSessionClosed {
			t.Errorf("expected ErrSessionClosed, got %v", err)
		}
	})

	t.Run("closed state rejects Finish", func(t *testing.T) {
		session.state.Store(int32(stateClosed))
		err := session.Finish(t.Context())
		if err != ErrSessionClosed {
			t.Errorf("expected ErrSessionClosed, got %v", err)
		}
	})
}

func TestTTSSession_StreamCancellation(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		// 不发送任何音频，让 Stream 阻塞直到 context cancel
		select {}
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}
	session.sendStart(nil)
	session.waitStarted()
	go session.readLoop()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // 立即取消

	err := session.Stream(ctx, func(audio []byte) error {
		return nil
	})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	session.Close()
}

func TestTTSSession_CloseIdempotent(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage()
		conn.WriteJSON(wsResponse{
			Type: wsTypeSystemStarted, Code: 0, Message: "success",
		})
		// 等待关闭
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}
	session.sendStart(nil)
	session.waitStarted()
	go session.readLoop()

	// 并发调用 Close 应该安全
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			session.Close()
		})
	}
	wg.Wait()
}

func TestTTSSession_InitError(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		// 返回初始化错误
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    216100,
			Message: "Invalid system.start payload.",
		})
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		errCh:   make(chan error, 1),
		done:    make(chan struct{}),
	}
	session.sendStart(&TTSConfig{SampleRate: 99999})
	err := session.waitStarted()
	if err == nil {
		t.Fatal("expected error")
	}
	wsErr, ok := IsWebSocketError(err)
	if !ok {
		t.Fatalf("expected WebSocketError, got %T: %v", err, err)
	}
	if wsErr.Code != 216100 {
		t.Errorf("code = %d, want 216100", wsErr.Code)
	}
}
