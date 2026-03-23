package bdvoice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// Client 创建测试
// ============================================================================

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr error
	}{
		{
			name:    "no auth configured",
			opts:    nil,
			wantErr: ErrNoAuth,
		},
		{
			name: "api key mode",
			opts: []Option{WithAPIKey("test-key")},
		},
		{
			name: "client credentials mode",
			opts: []Option{WithClientCredentials("id", "secret")},
		},
		{
			name: "api key takes precedence when both set",
			opts: []Option{
				WithClientCredentials("id", "secret"),
				WithAPIKey("test-key"),
			},
		},
		{
			name: "incomplete credentials - missing secret",
			opts: []Option{
				WithClientCredentials("id", ""),
			},
			wantErr: ErrNoAuth,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.opts...)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.wantErr)
				}
				if err != tt.wantErr {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected non-nil client")
			}
		})
	}
}

func TestOptions(t *testing.T) {
	t.Run("custom http client", func(t *testing.T) {
		hc := &http.Client{Timeout: 60 * time.Second}
		c, err := New(WithAPIKey("key"), WithHTTPClient(hc))
		if err != nil {
			t.Fatal(err)
		}
		if c.httpClient != hc {
			t.Error("http client not set")
		}
	})

	t.Run("nil http client ignored", func(t *testing.T) {
		c, err := New(WithAPIKey("key"), WithHTTPClient(nil))
		if err != nil {
			t.Fatal(err)
		}
		if c.httpClient == nil {
			t.Error("http client should not be nil")
		}
	})

	t.Run("custom base url", func(t *testing.T) {
		c, err := New(WithAPIKey("key"), WithBaseURL("https://custom.api.com"))
		if err != nil {
			t.Fatal(err)
		}
		if c.baseURL != "https://custom.api.com" {
			t.Errorf("base url = %q, want %q", c.baseURL, "https://custom.api.com")
		}
	})

	t.Run("empty base url ignored", func(t *testing.T) {
		c, err := New(WithAPIKey("key"), WithBaseURL(""))
		if err != nil {
			t.Fatal(err)
		}
		if c.baseURL != defaultBaseURL {
			t.Errorf("base url = %q, want default", c.baseURL)
		}
	})

	t.Run("idle timeout valid range", func(t *testing.T) {
		c, err := New(WithAPIKey("key"), WithIdleTimeout(120))
		if err != nil {
			t.Fatal(err)
		}
		if c.idleTimeout != 120 {
			t.Errorf("idle timeout = %d, want 120", c.idleTimeout)
		}
	})

	t.Run("idle timeout out of range ignored", func(t *testing.T) {
		c, err := New(WithAPIKey("key"), WithIdleTimeout(1000))
		if err != nil {
			t.Fatal(err)
		}
		if c.idleTimeout != defaultIdleTimeout {
			t.Errorf("idle timeout = %d, want default %d", c.idleTimeout, defaultIdleTimeout)
		}
	})
}

// ============================================================================
// Token 管理测试
// ============================================================================

func TestTokenCache_Valid(t *testing.T) {
	tests := []struct {
		name  string
		cache *tokenCache
		want  bool
	}{
		{name: "nil cache", cache: nil, want: false},
		{name: "empty token", cache: &tokenCache{}, want: false},
		{
			name: "expired token",
			cache: &tokenCache{
				AccessToken: "tok",
				ExpiresAt:   time.Now().Add(-1 * time.Hour),
			},
			want: false,
		},
		{
			name: "expiring within 5min",
			cache: &tokenCache{
				AccessToken: "tok",
				ExpiresAt:   time.Now().Add(3 * time.Minute),
			},
			want: false,
		},
		{
			name: "valid token",
			cache: &tokenCache{
				AccessToken: "tok",
				ExpiresAt:   time.Now().Add(1 * time.Hour),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cache.valid(); got != tt.want {
				t.Errorf("valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAccessToken(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// 验证请求参数
		if r.URL.Query().Get("grant_type") != "client_credentials" {
			t.Error("missing grant_type")
		}
		if r.URL.Query().Get("client_id") != "test-id" {
			t.Error("wrong client_id")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token-123",
			"expires_in":   86400,
		})
	}))
	defer server.Close()

	client, err := New(
		WithClientCredentials("test-id", "test-secret"),
		WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	// 首次获取
	token, err := client.getAccessToken(ctx)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if token != "test-token-123" {
		t.Errorf("token = %q, want %q", token, "test-token-123")
	}

	// 第二次应走缓存，不发请求
	token2, err := client.getAccessToken(ctx)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if token2 != token {
		t.Error("cached token mismatch")
	}
	if callCount.Load() != 1 {
		t.Errorf("server called %d times, want 1 (cached)", callCount.Load())
	}
}

func TestGetAccessToken_Concurrent(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond) // 模拟网络延迟
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "concurrent-token",
			"expires_in":   86400,
		})
	}))
	defer server.Close()

	client, err := New(
		WithClientCredentials("id", "secret"),
		WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	const goroutines = 20
	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			token, err := client.getAccessToken(ctx)
			if err != nil {
				t.Errorf("getAccessToken: %v", err)
				return
			}
			if token != "concurrent-token" {
				t.Errorf("token = %q, want %q", token, "concurrent-token")
			}
		})
	}
	wg.Wait()

	// singleflight 确保只有一次真正的 HTTP 请求
	if callCount.Load() != 1 {
		t.Errorf("server called %d times, want 1 (singleflight)", callCount.Load())
	}
}

func TestGetAccessToken_OAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_client",
			"error_description": "unknown client id",
		})
	}))
	defer server.Close()

	client, err := New(
		WithClientCredentials("bad-id", "bad-secret"),
		WithBaseURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.getAccessToken(t.Context())
	if err == nil {
		t.Fatal("expected error")
	}

	oauthErr, ok := IsOAuthError(err)
	if !ok {
		t.Fatalf("expected OAuthError, got %T: %v", err, err)
	}
	if oauthErr.ErrorCode != "invalid_client" {
		t.Errorf("error code = %q, want %q", oauthErr.ErrorCode, "invalid_client")
	}
}

// ============================================================================
// CreateVoice 测试
// ============================================================================

func TestCreateVoiceRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     *CreateVoiceRequest
		wantErr bool
		field   string
	}{
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
			field:   "request",
		},
		{
			name:    "empty voice_name",
			req:     &CreateVoiceRequest{AudioURL: "http://x"},
			wantErr: true,
			field:   "voice_name",
		},
		{
			name:    "no audio source",
			req:     &CreateVoiceRequest{VoiceName: "test"},
			wantErr: true,
			field:   "audio",
		},
		{
			name: "invalid lang",
			req: &CreateVoiceRequest{
				VoiceName: "test",
				AudioURL:  "http://x",
				Lang:      "invalid",
			},
			wantErr: true,
			field:   "lang",
		},
		{
			name: "valid with url",
			req: &CreateVoiceRequest{
				VoiceName: "test",
				AudioURL:  "http://x",
			},
			wantErr: false,
		},
		{
			name: "valid with file",
			req: &CreateVoiceRequest{
				VoiceName: "test",
				AudioFile: "base64data",
			},
			wantErr: false,
		},
		{
			name: "valid with lang zh",
			req: &CreateVoiceRequest{
				VoiceName: "test",
				AudioURL:  "http://x",
				Lang:      "zh",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if vErr, ok := IsValidationError(err); ok && tt.field != "" {
					if vErr.Field != tt.field {
						t.Errorf("field = %q, want %q", vErr.Field, tt.field)
					}
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateVoice_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}

		// 验证请求体
		var req CreateVoiceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.VoiceName != "test-voice" {
			t.Errorf("voice_name = %q, want %q", req.VoiceName, "test-voice")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CreateVoiceResponse{
			Status:  0,
			Message: "success",
			Data:    &CreateVoiceData{VoiceID: 42},
		})
	}))
	defer server.Close()

	client, _ := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)

	resp, err := client.CreateVoice(t.Context(), &CreateVoiceRequest{
		VoiceName: "test-voice",
		AudioURL:  "https://example.com/audio.wav",
	})
	if err != nil {
		t.Fatalf("CreateVoice: %v", err)
	}
	if resp.Data.VoiceID != 42 {
		t.Errorf("voice_id = %d, want 42", resp.Data.VoiceID)
	}
}

func TestCreateVoice_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 百度 API 返回 200 但 status != 0 的业务错误
		json.NewEncoder(w).Encode(map[string]any{
			"status":  216404,
			"message": "Voice id not exists.",
		})
	}))
	defer server.Close()

	client, _ := New(
		WithAPIKey("test-key"),
		WithBaseURL(server.URL),
	)

	_, err := client.CreateVoice(t.Context(), &CreateVoiceRequest{
		VoiceName: "test",
		AudioURL:  "https://example.com/audio.wav",
	})
	if err == nil {
		t.Fatal("expected error")
	}

	apiErr, ok := IsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.Code != 216404 {
		t.Errorf("code = %d, want 216404", apiErr.Code)
	}
}

func TestCreateVoice_WithAccessToken(t *testing.T) {
	// 记录 token 请求和业务请求是否正确
	var tokenRequested, voiceRequested atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == tokenEndpoint {
			tokenRequested.Store(true)
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "my-token",
				"expires_in":   86400,
			})
			return
		}

		if r.URL.Path == createVoiceEndpoint {
			voiceRequested.Store(true)
			// 验证 token 传递
			if tok := r.URL.Query().Get("access_token"); tok != "my-token" {
				t.Errorf("access_token = %q, want %q", tok, "my-token")
			}
			json.NewEncoder(w).Encode(CreateVoiceResponse{
				Status: 0,
				Data:   &CreateVoiceData{VoiceID: 99},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, _ := New(
		WithClientCredentials("id", "secret"),
		WithBaseURL(server.URL),
	)

	resp, err := client.CreateVoice(t.Context(), &CreateVoiceRequest{
		VoiceName: "test",
		AudioURL:  "https://example.com/audio.wav",
	})
	if err != nil {
		t.Fatalf("CreateVoice: %v", err)
	}
	if !tokenRequested.Load() {
		t.Error("token endpoint not called")
	}
	if !voiceRequested.Load() {
		t.Error("voice endpoint not called")
	}
	if resp.Data.VoiceID != 99 {
		t.Errorf("voice_id = %d, want 99", resp.Data.VoiceID)
	}
}

// ============================================================================
// 错误类型判断测试
// ============================================================================

func TestErrorTypes(t *testing.T) {
	t.Run("APIError", func(t *testing.T) {
		err := &APIError{StatusCode: 400, Code: 216100, Message: "invalid"}
		got := err.Error()
		if got == "" {
			t.Error("empty error string")
		}
		if e, ok := IsAPIError(err); !ok || e.Code != 216100 {
			t.Error("IsAPIError failed")
		}
	})

	t.Run("OAuthError", func(t *testing.T) {
		err := &OAuthError{ErrorCode: "invalid_client", Description: "bad"}
		if e, ok := IsOAuthError(err); !ok || e.ErrorCode != "invalid_client" {
			t.Error("IsOAuthError failed")
		}
	})

	t.Run("ValidationError", func(t *testing.T) {
		err := &ValidationError{Field: "name", Reason: "required"}
		if e, ok := IsValidationError(err); !ok || e.Field != "name" {
			t.Error("IsValidationError failed")
		}
	})

	t.Run("WebSocketError", func(t *testing.T) {
		err := &WebSocketError{Type: "system.error", Code: 216101, Message: "missing text"}
		if e, ok := IsWebSocketError(err); !ok || e.Code != 216101 {
			t.Error("IsWebSocketError failed")
		}
	})
}
