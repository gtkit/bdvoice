package bdvoice

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIntegration_TTSRead(t *testing.T) {
	t.Parallel()

	if os.Getenv("BDVOICE_RUN_INTEGRATION") != "1" {
		t.Skip("set BDVOICE_RUN_INTEGRATION=1 to run integration tests")
	}

	client := newIntegrationClient(t)
	voiceID := getenvRequiredInt(t, "BDVOICE_VOICE_ID")
	text := getenvDefault("BDVOICE_TTS_TEXT", "你好，这是一段来自 bdvoice SDK 集成测试的语音合成文本。")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	session, err := client.NewTTSSession(ctx, voiceID, &TTSConfig{
		MediaType: MediaMP3,
		Lang:      LangChinese,
	})
	if err != nil {
		t.Fatalf("NewTTSSession: %v", err)
	}
	defer session.Close()

	if err := session.SendText(ctx, text); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := session.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var totalBytes int
	for {
		frame, err := session.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		totalBytes += len(frame)
	}

	if totalBytes == 0 {
		t.Fatal("expected non-empty audio stream")
	}
	t.Logf("voice_id=%d session_id=%s audio_bytes=%d", voiceID, session.SessionID(), totalBytes)
}

func TestIntegration_StreamTTSRead(t *testing.T) {
	t.Parallel()

	if os.Getenv("BDVOICE_RUN_INTEGRATION") != "1" {
		t.Skip("set BDVOICE_RUN_INTEGRATION=1 to run integration tests")
	}

	per := os.Getenv("BDVOICE_STREAM_TTS_PER")
	if per == "" {
		t.Skip("set BDVOICE_STREAM_TTS_PER to run stream TTS integration test (e.g. 0)")
	}

	client := newIntegrationClient(t)
	text := getenvDefault("BDVOICE_TTS_TEXT", "你好，这是一段来自 bdvoice SDK 流式文本在线合成集成测试的语音。")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	session, err := client.NewStreamTTSSession(ctx, per, &StreamTTSConfig{
		Aue: AudioEncodingMP3,
		Spd: 5,
		Pit: 5,
		Vol: 5,
	})
	if err != nil {
		t.Fatalf("NewStreamTTSSession: %v", err)
	}
	defer session.Close()

	if err := session.SendText(ctx, text); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := session.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var totalBytes int
	for {
		frame, err := session.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		totalBytes += len(frame)
	}

	if totalBytes == 0 {
		t.Fatal("expected non-empty audio stream")
	}
	t.Logf("per=%s session_id=%s audio_bytes=%d", per, session.SessionID(), totalBytes)
}

func TestIntegration_CreateVoice(t *testing.T) {
	t.Parallel()

	if os.Getenv("BDVOICE_RUN_INTEGRATION") != "1" {
		t.Skip("set BDVOICE_RUN_INTEGRATION=1 to run integration tests")
	}

	audioURL := os.Getenv("BDVOICE_AUDIO_URL")
	if audioURL == "" {
		t.Skip("set BDVOICE_AUDIO_URL to run CreateVoice integration test")
	}

	client := newIntegrationClient(t)
	voiceName := getenvDefault("BDVOICE_VOICE_NAME", fmt.Sprintf("bdvoice-it-%d", time.Now().Unix()))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	resp, err := client.CreateVoice(ctx, &CreateVoiceRequest{
		VoiceName: voiceName,
		AudioURL:  audioURL,
		Lang:      getenvDefault("BDVOICE_LANG", LangChinese),
	})
	if err != nil {
		t.Fatalf("CreateVoice: %v", err)
	}
	if resp == nil || resp.Data == nil || resp.Data.VoiceID == 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	t.Logf("created voice_id=%d voice_name=%s", resp.Data.VoiceID, voiceName)
}

func newIntegrationClient(t *testing.T) *Client {
	t.Helper()

	baseURL := getenvDefault("BDVOICE_BASE_URL", defaultBaseURL)
	timeout := 30 * time.Second

	switch strings.ToLower(os.Getenv("BDVOICE_AUTH_MODE")) {
	case "", "credentials", "oauth":
		clientID := getenvRequired(t, "BDVOICE_CLIENT_ID")
		clientSecret := getenvRequired(t, "BDVOICE_CLIENT_SECRET")
		client, err := New(
			WithClientCredentials(clientID, clientSecret),
			WithBaseURL(baseURL),
			WithHTTPClient(&http.Client{Timeout: timeout}),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return client
	case "apikey", "api_key":
		apiKey := getenvRequired(t, "BDVOICE_API_KEY")
		client, err := New(
			WithAPIKey(apiKey),
			WithBaseURL(baseURL),
			WithHTTPClient(&http.Client{Timeout: timeout}),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return client
	default:
		t.Fatalf("unsupported BDVOICE_AUTH_MODE=%q", os.Getenv("BDVOICE_AUTH_MODE"))
		return nil
	}
}

func getenvRequired(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("set %s to run integration tests", key)
	}
	return v
}

func getenvRequiredInt(t *testing.T, key string) int {
	t.Helper()
	v := getenvRequired(t, key)
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", key, v)
	}
	return n
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
