package bdvoice_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gtkit/bdvoice"
)

// ExampleNew_withClientCredentials 演示使用 client_id + client_secret 创建客户端。
// 这是推荐的鉴权方式，SDK 会自动管理 access_token 的获取和续期。
func ExampleNew_withClientCredentials() {
	client, err := bdvoice.New(
		bdvoice.WithClientCredentials("your-client-id", "your-client-secret"),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = client
}

// ExampleNew_withAPIKey 演示使用 API Key 创建客户端。
func ExampleNew_withAPIKey() {
	client, err := bdvoice.New(
		bdvoice.WithAPIKey("your-api-key"),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = client
}

// ExampleClient_CreateVoice 演示通过音频 URL 创建音色。
func ExampleClient_CreateVoice() {
	client, err := bdvoice.New(
		bdvoice.WithClientCredentials("your-client-id", "your-client-secret"),
	)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.CreateVoice(context.Background(), &bdvoice.CreateVoiceRequest{
		VoiceName: "my-voice",
		VoiceDesc: "温柔细腻的音色",
		AudioURL:  "https://example.com/audio.wav",
		Lang:      bdvoice.LangChinese,
	})
	if err != nil {
		// 可以使用类型判断获取更详细的错误信息
		if apiErr, ok := bdvoice.IsAPIError(err); ok {
			log.Fatalf("API error: code=%d, message=%s", apiErr.Code, apiErr.Message)
		}
		log.Fatal(err)
	}

	fmt.Printf("音色创建成功，voice_id: %d\n", resp.Data.VoiceID)
}

// ExampleClient_NewTTSSession_read 演示使用 Read（拉模式）读取合成音频。
// 适合需要精细控制的场景：边接收边写入文件、边接收边转发。
func ExampleClient_NewTTSSession_read() {
	client, err := bdvoice.New(
		bdvoice.WithClientCredentials("your-client-id", "your-client-secret"),
		bdvoice.WithIdleTimeout(120), // 自定义空闲超时
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	voiceID := 12345 // 通过 CreateVoice 获取

	// 如果需要显式发送 0，可使用 setter：
	// cfg := (&bdvoice.TTSConfig{MediaType: bdvoice.MediaMP3}).SetPitch(0).SetSpeed(0)

	// 创建 TTS 会话
	session, err := client.NewTTSSession(ctx, voiceID, &bdvoice.TTSConfig{
		MediaType: bdvoice.MediaMP3,
		Speed:     7,
		Pitch:     5,
		Volume:    8,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// 发送待合成的文本（可多次调用）
	if err := session.SendText(ctx, "你好世界，这是一段测试文本。"); err != nil {
		log.Fatal(err)
	}
	if err := session.SendText(ctx, "第二段文本，继续合成。"); err != nil {
		log.Fatal(err)
	}

	// 通知服务端所有文本已发送
	if err := session.Finish(ctx); err != nil {
		log.Fatal(err)
	}

	// 拉模式读取音频数据
	f, _ := os.Create("output.mp3")
	defer f.Close()

	for {
		data, err := session.Read()
		if err == io.EOF {
			break // 合成结束
		}
		if err != nil {
			log.Fatal(err)
		}
		f.Write(data)
	}

	fmt.Println("音频合成完成")
}

// ExampleTTSSession_Stream 演示使用 Stream（推模式）读取合成音频。
// 适合"接收即转发"的流水线场景：HTTP streaming、gRPC stream 等。
func ExampleTTSSession_Stream() {
	client, err := bdvoice.New(
		bdvoice.WithClientCredentials("your-client-id", "your-client-secret"),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	session, err := client.NewTTSSession(ctx, 12345, &bdvoice.TTSConfig{
		MediaType: bdvoice.MediaMP3,
		Lang:      bdvoice.LangChinese,
		Dialect:   bdvoice.DialectSichuan, // 四川话
	})
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	_ = session.SendText(ctx, "这是一段四川话合成测试。")
	_ = session.Finish(ctx)

	// 推模式：回调处理每帧音频
	f, _ := os.Create("output_sichuan.mp3")
	defer f.Close()

	err = session.Stream(ctx, func(audio []byte) error {
		_, err := f.Write(audio)
		return err
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("方言音频合成完成")
}
