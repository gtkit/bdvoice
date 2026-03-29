package bdvoice

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestStreamTTSSession_FullFlow(t *testing.T) {
	audioData := []byte("stream-tts-audio-frame-1")
	audioData2 := []byte("stream-tts-audio-frame-2")

	server := mockWSServer(t, func(conn *websocket.Conn) {
		// 1. 读取 system.start 并验证 payload 结构
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read start: %v", err)
			return
		}
		var start streamTTSStartFrame
		json.Unmarshal(msg, &start)
		if start.Type != wsTypeSystemStart {
			t.Errorf("type = %q, want %q", start.Type, wsTypeSystemStart)
		}
		if start.Payload == nil {
			t.Error("payload is nil")
		} else {
			if start.Payload.Spd != 7 {
				t.Errorf("spd = %d, want 7", start.Payload.Spd)
			}
			if start.Payload.Aue != AudioEncodingMP3 {
				t.Errorf("aue = %d, want %d", start.Payload.Aue, AudioEncodingMP3)
			}
		}

		// 2. 回复 system.started
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
			Headers: map[string]string{"session_id": "stream-session-001"},
		})

		// 3. 读取文本帧
		_, msg, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read text: %v", err)
			return
		}
		var text wsTextFrame
		json.Unmarshal(msg, &text)
		if text.Payload.Text != "你好世界，欢迎使用流式TTS。" {
			t.Errorf("text = %q", text.Payload.Text)
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
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}

	// 发送 stream TTS start
	cfg := &StreamTTSConfig{
		Spd: 7,
		Aue: AudioEncodingMP3,
	}
	if err := session.sendStreamTTSStart(cfg); err != nil {
		t.Fatalf("sendStreamTTSStart: %v", err)
	}
	if err := session.waitStarted(); err != nil {
		t.Fatalf("waitStarted: %v", err)
	}
	if session.sessionID != "stream-session-001" {
		t.Errorf("session_id = %q, want %q", session.sessionID, "stream-session-001")
	}

	// 启动 readLoop
	go session.readLoop()

	ctx := t.Context()
	if err := session.SendText(ctx, "你好世界，欢迎使用流式TTS。"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
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

	session.Close()
}

func TestStreamTTSSession_Stream(t *testing.T) {
	frames := [][]byte{
		[]byte("stream-audio-1"),
		[]byte("stream-audio-2"),
		[]byte("stream-audio-3"),
	}

	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		conn.ReadMessage() // text
		conn.ReadMessage() // finish
		for _, f := range frames {
			conn.WriteMessage(websocket.BinaryMessage, f)
		}
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
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}

	session.sendStreamTTSStart(&StreamTTSConfig{Aue: AudioEncodingWAV})
	session.waitStarted()
	go session.readLoop()

	ctx := t.Context()
	session.SendText(ctx, "流式推模式测试")
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

func TestStreamTTSSession_InitError(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    216100,
			Message: "语速参数错误, 请输入0-15的整数",
		})
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}
	session.sendStreamTTSStart(&StreamTTSConfig{Spd: 99})
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

func TestStreamTTSSession_TextLimitError(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		conn.ReadMessage() // text
		// 返回文本过长错误
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemError,
			Code:    216103,
			Message: "文本过长, 请控制在1000字以内",
		})
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}
	session.sendStreamTTSStart(nil)
	session.waitStarted()
	go session.readLoop()

	session.SendText(t.Context(), "test")

	_, err := session.Read()
	if err == nil {
		t.Fatal("expected error")
	}
	wsErr, ok := IsWebSocketError(err)
	if !ok {
		t.Fatalf("expected WebSocketError, got %T: %v", err, err)
	}
	if wsErr.Code != 216103 {
		t.Errorf("code = %d, want 216103", wsErr.Code)
	}

	session.Close()
}

func TestNewStreamTTSSession_PerValidation(t *testing.T) {
	client, err := New(WithAPIKey("test-key"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.NewStreamTTSSession(t.Context(), "", nil)
	if err == nil {
		t.Fatal("expected error for empty per")
	}
	if _, ok := IsValidationError(err); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestNewStreamTTSSession_E2E(t *testing.T) {
	audioData := []byte("e2e-audio-data")

	server := mockWSServer(t, func(conn *websocket.Conn) {
		// 读取 start
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read start: %v", err)
			return
		}
		var start streamTTSStartFrame
		json.Unmarshal(msg, &start)
		if start.Type != wsTypeSystemStart {
			t.Errorf("start type = %q", start.Type)
		}

		// 回复 started
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
			Headers: map[string]string{"session_id": "e2e-stream-001"},
		})

		// 读取 text
		conn.ReadMessage()
		// 读取 finish
		conn.ReadMessage()

		// 发送音频 + finished
		conn.WriteMessage(websocket.BinaryMessage, audioData)
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemFinished,
			Code:    0,
			Message: "success",
		})
	})
	defer server.Close()

	// 创建客户端，指向 mock server
	baseURL := strings.Replace(server.URL, "http://", "https://", 1)
	client, err := New(
		WithAPIKey("test-key"),
		WithBaseURL(baseURL),
	)
	if err != nil {
		t.Fatal(err)
	}

	// 由于 mock 是 ws:// 而 buildStreamTTSWSURL 会生成 wss://，
	// 我们直接测试底层逻辑（和 tts_test.go 保持一致的模式）
	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, err := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}

	if err := session.sendStreamTTSStart(&StreamTTSConfig{
		Aue: AudioEncodingMP3,
		Spd: 5,
	}); err != nil {
		t.Fatalf("sendStreamTTSStart: %v", err)
	}
	if err := session.waitStarted(); err != nil {
		t.Fatalf("waitStarted: %v", err)
	}

	go session.readLoop()

	ctx := t.Context()
	if err := session.SendText(ctx, "端到端测试文本"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := session.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	data, err := session.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != string(audioData) {
		t.Errorf("audio = %q, want %q", data, audioData)
	}

	// 读取到 EOF
	_, err = session.Read()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	_ = client // 确保 client 被使用
	session.Close()
}

func TestStreamTTSSession_CancelledContext(t *testing.T) {
	server := mockWSServer(t, func(conn *websocket.Conn) {
		conn.ReadMessage() // start
		conn.WriteJSON(wsResponse{
			Type:    wsTypeSystemStarted,
			Code:    0,
			Message: "success",
		})
		// 不发送任何音频，让 Stream 阻塞
		select {}
	})
	defer server.Close()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1)
	conn, _, _ := websocket.DefaultDialer.DialContext(t.Context(), wsURL, nil)

	session := &TTSSession{
		conn:    conn,
		audioCh: make(chan []byte, 64),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}
	session.sendStreamTTSStart(nil)
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

// ============================================================================
// StreamTTSConfig 序列化测试
// ============================================================================

func TestStreamTTSConfigMarshalJSON(t *testing.T) {
	t.Run("omits unset zero values", func(t *testing.T) {
		data, err := json.Marshal(&StreamTTSConfig{Spd: 5})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(data)
		// 只应包含 spd，不应包含 pit/vol/aue/audio_ctrl
		if strings.Contains(got, "pit") || strings.Contains(got, "vol") ||
			strings.Contains(got, "aue") || strings.Contains(got, "audio_ctrl") {
			t.Fatalf("unexpected fields in %s", got)
		}
		if !strings.Contains(got, `"spd":5`) {
			t.Fatalf("missing spd in %s", got)
		}
	})

	t.Run("includes explicit zero values", func(t *testing.T) {
		cfg := (&StreamTTSConfig{}).
			SetSpd(0).
			SetPit(0).
			SetVol(0)

		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		for _, field := range []string{"spd", "pit", "vol"} {
			if got[field] != float64(0) {
				t.Fatalf("%s = %v, want 0; json=%s", field, got[field], string(data))
			}
		}
	})

	t.Run("audio_ctrl included", func(t *testing.T) {
		cfg := (&StreamTTSConfig{
			Aue: AudioEncodingMP3,
		}).WithSampleRate16K()

		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(data)
		if !strings.Contains(got, `"audio_ctrl":"{\"sampling_rate\":16000}"`) {
			t.Fatalf("missing audio_ctrl in %s", got)
		}
		if !strings.Contains(got, `"aue":3`) {
			t.Fatalf("missing aue in %s", got)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		var cfg *StreamTTSConfig
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(data) != "null" {
			t.Fatalf("expected null, got %s", data)
		}
	})

	t.Run("all audio encodings", func(t *testing.T) {
		tests := []struct {
			enc  AudioEncoding
			want int
		}{
			{AudioEncodingMP3, 3},
			{AudioEncodingPCM16K, 4},
			{AudioEncodingPCM8K, 5},
			{AudioEncodingWAV, 6},
		}
		for _, tt := range tests {
			cfg := &StreamTTSConfig{Aue: tt.enc}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got map[string]any
			json.Unmarshal(data, &got)
			if int(got["aue"].(float64)) != tt.want {
				t.Errorf("aue = %v, want %d; json=%s", got["aue"], tt.want, data)
			}
		}
	})
}

func TestStreamTTSConfig_SetterChaining(t *testing.T) {
	cfg := (&StreamTTSConfig{}).
		SetSpd(3).
		SetPit(7).
		SetVol(9).
		SetAue(AudioEncodingWAV).
		WithSampleRate16K()

	if cfg.Spd != 3 {
		t.Errorf("Spd = %d, want 3", cfg.Spd)
	}
	if cfg.Pit != 7 {
		t.Errorf("Pit = %d, want 7", cfg.Pit)
	}
	if cfg.Vol != 9 {
		t.Errorf("Vol = %d, want 9", cfg.Vol)
	}
	if cfg.Aue != AudioEncodingWAV {
		t.Errorf("Aue = %d, want %d", cfg.Aue, AudioEncodingWAV)
	}
	if cfg.AudioCtrl != `{"sampling_rate":16000}` {
		t.Errorf("AudioCtrl = %q", cfg.AudioCtrl)
	}
}

func TestStreamTTSConfig_NilReceiverSafety(t *testing.T) {
	var cfg *StreamTTSConfig
	if cfg.SetSpd(1) != nil {
		t.Error("SetSpd on nil should return nil")
	}
	if cfg.SetPit(1) != nil {
		t.Error("SetPit on nil should return nil")
	}
	if cfg.SetVol(1) != nil {
		t.Error("SetVol on nil should return nil")
	}
	if cfg.SetAue(AudioEncodingMP3) != nil {
		t.Error("SetAue on nil should return nil")
	}
	if cfg.WithSampleRate16K() != nil {
		t.Error("WithSampleRate16K on nil should return nil")
	}
}

func TestBuildStreamTTSWSURL(t *testing.T) {
	t.Run("api key mode", func(t *testing.T) {
		client, err := New(WithAPIKey("test-key"))
		if err != nil {
			t.Fatal(err)
		}

		u, err := client.buildStreamTTSWSURL(t.Context(), "4103")
		if err != nil {
			t.Fatal(err)
		}

		// 应包含 per 参数，不包含 access_token
		if !strings.Contains(u, "per=4103") {
			t.Errorf("url missing per: %s", u)
		}
		if strings.Contains(u, "access_token") {
			t.Errorf("url should not contain access_token in API key mode: %s", u)
		}
		if !strings.HasPrefix(u, "wss://") {
			t.Errorf("url should start with wss://: %s", u)
		}
		if !strings.Contains(u, streamTTSWSEndpoint) {
			t.Errorf("url missing stream tts endpoint: %s", u)
		}
	})
}
