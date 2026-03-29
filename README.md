# bdvoice

[![Go Reference](https://pkg.go.dev/badge/github.com/gtkit/bdvoice.svg)](https://pkg.go.dev/github.com/gtkit/bdvoice)
[![Go Report Card](https://goreportcard.com/badge/github.com/gtkit/bdvoice)](https://goreportcard.com/report/github.com/gtkit/bdvoice)

百度智能云语音合成 Go SDK，提供音色创建、声音复刻 TTS 和公有云流式文本在线合成能力。

## 功能特性

- **音色创建**：通过音频 URL 或 base64 编码创建自定义音色
- **声音复刻 TTS**：基于已创建音色的 WebSocket 实时语音合成
- **流式文本在线合成（新）**：公有云预置发音人的 WebSocket 实时语音合成，支持"边合成边播放"
- **统一会话接口**：两种 TTS 模式共享相同的 TTSSession（Read/Stream/Close），零学习成本
- **双鉴权模式**：支持 OAuth access_token 和 API Key 两种鉴权方式
- **自动 Token 管理**：singleflight 防并发刷新 + atomic 无锁缓存读取
- **多语种/方言**：中英语、日语、河南话、上海话、四川话、湖南话、贵州话
- **生产级质量**：完善的错误类型体系、context 传播、并发安全、资源泄漏防护

## 环境要求

- Go 1.26+

## 安装

```bash
go get github.com/gtkit/bdvoice@latest
```

## 集成测试

默认 `go test ./...` 不会访问百度真实服务。要执行端到端集成验证，设置环境变量后运行：

```bash
BDVOICE_RUN_INTEGRATION=1 \
BDVOICE_CLIENT_ID=your-client-id \
BDVOICE_CLIENT_SECRET=your-client-secret \
BDVOICE_VOICE_ID=12345 \
/usr/local/go/bin/go test -v -run '^TestIntegration_' ./...
```

或使用 Makefile：

```bash
BDVOICE_CLIENT_ID=your-client-id \
BDVOICE_CLIENT_SECRET=your-client-secret \
BDVOICE_VOICE_ID=12345 \
make integration
```

可选变量：

- `BDVOICE_AUTH_MODE=apikey`：改用 API Key 模式，并提供 `BDVOICE_API_KEY`
- `BDVOICE_BASE_URL`：覆盖默认百度域名，便于私有化/代理环境
- `BDVOICE_TTS_TEXT`：覆盖默认 TTS 测试文本
- `BDVOICE_AUDIO_URL`：提供训练音频 URL 后，额外执行 `CreateVoice` 集成测试
- `BDVOICE_VOICE_NAME`：覆盖创建音色时使用的名称
- `BDVOICE_LANG`：创建音色时的语种，默认 `zh`

## 代码结构

```
bdvoice/
├── client.go          # Client 定义 + Functional Options
│                      # - New() 构造函数，创建后不可变（immutable）
│                      # - WithHTTPClient / WithAPIKey / WithClientCredentials 等选项
│                      # - WithBaseURL / WithIdleTimeout 可选配置
│
├── token.go           # OAuth Token 自动管理
│                      # - getAccessToken(): atomic 读缓存 → singleflight 去重刷新
│                      # - refreshToken(): 向百度 OAuth 服务请求新 token
│                      # - buildAuthQuery() / setAuthHeader(): 双鉴权模式适配
│
├── voice.go           # 创建音色 REST API
│                      # - CreateVoice(): 参数校验 → 鉴权 → 序列化 → HTTP POST → 解析响应
│                      # - 支持 audio_url 和 audio_file 两种上传方式
│
├── tts.go             # WebSocket 流式 TTS 合成（声音复刻）
│                      # - NewTTSSession(): 建连 → system.start → 等待 started → 启动 readLoop
│                      # - SendText(): 发送文本帧（≤1000字/次，可多次调用）
│                      # - Finish(): 发送 system.finish 通知服务端
│                      # - Read(): 拉模式 — 逐帧读取音频，io.EOF 表示结束
│                      # - Stream(): 推模式 — 回调处理每帧音频，支持 context 取消
│                      # - Close(): 幂等关闭，sync.Once 保证安全
│                      # - readLoop(): 后台 goroutine，区分二进制(音频)/文本(控制帧)
│
├── stream_tts.go      # 公有云流式文本在线合成（新功能）
│                      # - NewStreamTTSSession(): 使用预置发音人的流式 TTS
│                      # - buildStreamTTSWSURL(): 构建 /v1/tts endpoint URL（含 per 参数）
│                      # - sendStreamTTSStart(): 发送 StreamTTSConfig 初始化帧
│                      # - 复用 TTSSession 的 SendText/Finish/Read/Stream/Close
│
├── types.go           # 所有类型定义和常量
│                      # - AuthMode: 鉴权方式枚举
│                      # - CreateVoiceRequest/Response: 创建音色请求/响应
│                      # - TTSConfig: 声音复刻 TTS 参数（语种/方言/格式/音调/音量/语速）
│                      # - StreamTTSConfig: 公有云流式 TTS 参数（spd/pit/vol/aue/audio_ctrl）
│                      # - AudioEncoding: 音频编码格式常量（MP3/PCM16K/PCM8K/WAV）
│                      # - Lang*/Dialect*/Media*: 语种/方言/格式常量
│                      # - WebSocket 帧类型定义（start/text/finish/response）
│                      # - tokenCache: token 缓存结构（含 5 分钟过期缓冲）
│
├── errors.go          # 错误类型体系
│                      # - APIError: 百度 API 业务错误（含 HTTP 状态码 + API 错误码）
│                      # - OAuthError: OAuth 鉴权错误
│                      # - ValidationError: 客户端参数校验错误
│                      # - WebSocketError: WebSocket 通信错误
│                      # - 哨兵错误: ErrSessionClosed / ErrSessionFinished / ErrTextTooLong / ErrNoAuth
│                      # - IsAPIError() / IsOAuthError() 等辅助函数（Go 1.26 errors.AsType）
│
├── example_test.go    # 完整使用示例
│                      # - ExampleNew_withClientCredentials: OAuth 模式创建客户端
│                      # - ExampleNew_withAPIKey: API Key 模式创建客户端
│                      # - ExampleClient_CreateVoice: 创建音色
│                      # - ExampleClient_NewTTSSession_read: 拉模式读取音频
│                      # - ExampleTTSSession_Stream: 推模式回调处理
│
├── client_test.go     # Client + Token + CreateVoice 单元测试
│                      # - TestNew: 各种鉴权配置组合
│                      # - TestOptions: functional options 测试
│                      # - TestTokenCache_Valid: token 过期判断
│                      # - TestGetAccessToken: 缓存命中 + 并发安全 + OAuth 错误
│                      # - TestCreateVoice_*: 成功/业务错误/token 联动
│
├── tts_test.go        # WebSocket TTS 单元测试（含 mock WebSocket server）
│                      # - TestTTSSession_FullFlow: 完整流程（start→text→finish→read）
│                      # - TestTTSSession_Stream: 推模式测试
│                      # - TestTTSSession_ServerError: 服务端错误处理
│                      # - TestTTSSession_SendTextValidation: 文本校验
│                      # - TestTTSSession_StateTransitions: 状态机测试
│                      # - TestTTSSession_StreamCancellation: context 取消
│                      # - TestTTSSession_CloseIdempotent: 并发 Close 安全
│
├── go.mod             # Go module 定义
└── go.sum             # 依赖校验（go mod tidy 生成）
```

## 架构设计图

```
┌─────────────────────────────────────────────────────────────────┐
│                         Client (入口)                           │
│  ┌──────────────────┐  ┌──────────────────┐                    │
│  │  AuthAccessToken  │  │    AuthAPIKey     │  ← 双鉴权模式     │
│  │  (auto token)     │  │  (header auth)   │                    │
│  └────────┬─────────┘  └──────────────────┘                    │
│           │                                                     │
│  ┌────────▼──────────────────────────────────┐                 │
│  │            Token Manager                   │                 │
│  │  ┌─────────────┐  ┌────────────────────┐  │                 │
│  │  │ atomic.      │  │  singleflight.     │  │                 │
│  │  │ Pointer      │  │  Group             │  │                 │
│  │  │ [tokenCache] │  │  (防并发刷新)       │  │                 │
│  │  │ (无锁读)     │  │                    │  │                 │
│  │  └─────────────┘  └────────────────────┘  │                 │
│  └───────────────────────────────────────────┘                 │
│           │                                                     │
│  ┌────────▼────────────┐  ┌──────────────────────────────────┐ │
│  │   CreateVoice()     │  │      NewTTSSession()             │ │
│  │   (REST API)        │  │      (WebSocket)                 │ │
│  │                     │  │                                  │ │
│  │  POST /create       │  │  wss://...tts                    │ │
│  │  → voice_id         │  │  ┌─────────────────────────┐    │ │
│  └─────────────────────┘  │  │    TTSSession            │    │ │
│                           │  │                           │    │ │
│                           │  │  SendText() ──→ text帧    │    │ │
│                           │  │  Finish()  ──→ finish帧   │    │ │
│                           │  │                           │    │ │
│                           │  │  readLoop (后台goroutine) │    │ │
│                           │  │    ├─ Binary → audioCh    │    │ │
│                           │  │    └─ Text   → 控制帧     │    │ │
│                           │  │                           │    │ │
│                           │  │  Read()   ← 拉模式        │    │ │
│                           │  │  Stream() ← 推模式        │    │ │
│                           │  │                           │    │ │
│                           │  │  Close()  (sync.Once)     │    │ │
│                           │  └─────────────────────────┘    │ │
│                           └──────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

## 快速开始

### 1. 创建客户端

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/gtkit/bdvoice"
)

func main() {
    // 方式一（推荐）：使用 client_id + client_secret
    // SDK 自动获取和续期 access_token，无需手动管理
    client, err := bdvoice.New(
        bdvoice.WithClientCredentials(
            "your-client-id",     // 百度 AI 应用的 API Key
            "your-client-secret", // 百度 AI 应用的 Secret Key
        ),
        // 以下为可选配置：
        bdvoice.WithHTTPClient(&http.Client{
            Timeout: 30 * time.Second, // 自定义 HTTP 超时
        }),
        bdvoice.WithIdleTimeout(120), // WebSocket 空闲超时 120 秒
    )
    if err != nil {
        log.Fatal(err)
    }

    // 方式二：使用 API Key（适合快速测试）
    // client, err := bdvoice.New(
    //     bdvoice.WithAPIKey("your-api-key"),
    // )

    _ = client
}
```

### 2. 创建音色

```go
import (
    "context"
    "fmt"
    "log"

    "github.com/gtkit/bdvoice"
)

func createVoice(client *bdvoice.Client) {
    ctx := context.Background()

    // ---- 方式一：通过音频 URL 创建 ----
    resp, err := client.CreateVoice(ctx, &bdvoice.CreateVoiceRequest{
        VoiceName: "my-voice",             // 必填：音色名称，同一用户下不可重复
        VoiceDesc: "温柔细腻的女声",          // 可选：音色描述
        AudioURL:  "https://example.com/audio.wav", // 音频链接，5M 以内，5~20 秒
        Lang:      bdvoice.LangChinese, // 可选：语种，默认 "zh"
    })

    // ---- 方式二：通过 base64 编码创建 ----
    // resp, err := client.CreateVoice(ctx, &bdvoice.CreateVoiceRequest{
    //     VoiceName: "my-voice",
    //     AudioFile: base64EncodedAudio, // 音频 base64 编码
    //     Lang:      bdvoice.LangJapanese, // 日语音色建议 10~30 秒日语音频
    // })

    if err != nil {
        // 精细化错误处理
        if apiErr, ok := bdvoice.IsAPIError(err); ok {
            // API 业务错误，如音色名重复、参数无效等
            fmt.Printf("API 错误: code=%d, message=%s\n", apiErr.Code, apiErr.Message)
        } else if oauthErr, ok := bdvoice.IsOAuthError(err); ok {
            // 鉴权失败，如 client_id/secret 错误
            fmt.Printf("鉴权错误: %s - %s\n", oauthErr.ErrorCode, oauthErr.Description)
        } else if valErr, ok := bdvoice.IsValidationError(err); ok {
            // 参数校验失败（客户端侧）
            fmt.Printf("参数错误: 字段 %s - %s\n", valErr.Field, valErr.Reason)
        }
        log.Fatal(err)
    }

    fmt.Printf("音色创建成功！voice_id: %d\n", resp.Data.VoiceID)
    // 保存 voice_id，后续 TTS 合成时使用
}
```

### 3. 流式语音合成（拉模式 — Read）

**适用场景**：需要精细控制每帧音频的处理流程，例如边接收边写入文件、边接收边转发给播放器、需要统计接收进度等。

```go
import (
    "context"
    "fmt"
    "io"
    "log"
    "os"

    "github.com/gtkit/bdvoice"
)

func synthesizeWithRead(client *bdvoice.Client, voiceID int) {
    ctx := context.Background()

    // 步骤 1：创建 TTS 会话
    // 此步骤会自动完成 WebSocket 连接建立和初始化握手
    session, err := client.NewTTSSession(ctx, voiceID, &bdvoice.TTSConfig{
        MediaType: bdvoice.MediaMP3,     // 输出 MP3 格式
        Speed:     7,                        // 语速稍快（0-15，默认5）
        Pitch:     5,                        // 音调（0-15，默认5）
        Volume:    8,                        // 音量稍大（0-15，默认5）
        Lang:      bdvoice.LangChinese,  // 中英语
    })
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close() // 确保连接释放

    // 如果需要显式发送 0 值（例如最低音调/最慢语速），使用 setter：
    // cfg := (&bdvoice.TTSConfig{MediaType: bdvoice.MediaMP3}).SetPitch(0).SetSpeed(0)

    // 可选：打印 session_id，用于问题排查
    fmt.Printf("Session ID: %s\n", session.SessionID())

    // 步骤 2：发送待合成的文本
    // 可以多次调用 SendText，每次不超过 1000 字符
    texts := []string{
        "大家好，欢迎收听今天的节目。",
        "今天我们来聊一聊人工智能技术的最新发展。",
        "首先，让我们回顾一下过去一年的重要进展。",
    }
    for _, text := range texts {
        if err := session.SendText(ctx, text); err != nil {
            log.Fatal(err)
        }
    }

    // 步骤 3：通知服务端所有文本已发送
    // Finish 后不能再调用 SendText，但可以继续 Read
    if err := session.Finish(ctx); err != nil {
        log.Fatal(err)
    }

    // 步骤 4：拉模式逐帧读取音频
    f, err := os.Create("output.mp3")
    if err != nil {
        log.Fatal(err)
    }
    defer f.Close()

    var totalBytes int
    for {
        data, err := session.Read()
        if err == io.EOF {
            break // 所有音频已接收完毕
        }
        if err != nil {
            // 可能是服务端错误或连接断开
            if wsErr, ok := bdvoice.IsWebSocketError(err); ok {
                log.Printf("服务端错误: code=%d, msg=%s", wsErr.Code, wsErr.Message)
            }
            log.Fatal(err)
        }

        n, _ := f.Write(data)
        totalBytes += n
    }

    fmt.Printf("合成完成，共接收 %d 字节音频数据\n", totalBytes)
}
```

### 4. 流式语音合成（推模式 — Stream）

**适用场景**：代码更简洁的流式处理，适合"接收即转发"的场景，例如 HTTP streaming response、gRPC stream、直接写入 io.Writer 等。

```go
import (
    "context"
    "log"
    "net/http"
    "os"

    "github.com/gtkit/bdvoice"
)

// 示例 1：直接写入文件
func synthesizeToFile(client *bdvoice.Client, voiceID int) {
    ctx := context.Background()

    session, err := client.NewTTSSession(ctx, voiceID, &bdvoice.TTSConfig{
        MediaType: bdvoice.MediaMP3,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    _ = session.SendText(ctx, "这是一段推模式的语音合成测试。")
    _ = session.Finish(ctx)

    f, _ := os.Create("output_stream.mp3")
    defer f.Close()

    // Stream 会持续接收音频直到合成结束
    // handler 返回 error 会中止接收
    err = session.Stream(ctx, func(audio []byte) error {
        _, err := f.Write(audio)
        return err
    })
    if err != nil {
        log.Fatal(err)
    }
}

// 示例 2：HTTP 流式响应（实时播放）
func handleTTS(w http.ResponseWriter, r *http.Request, client *bdvoice.Client, voiceID int) {
    ctx := r.Context()

    session, err := client.NewTTSSession(ctx, voiceID, &bdvoice.TTSConfig{
        MediaType: bdvoice.MediaMP3,
    })
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer session.Close()

    text := r.URL.Query().Get("text")
    _ = session.SendText(ctx, text)
    _ = session.Finish(ctx)

    // 设置流式响应头
    w.Header().Set("Content-Type", "audio/mpeg")
    w.Header().Set("Transfer-Encoding", "chunked")

    // 推模式直接写入 HTTP response
    // 客户端断开连接时 ctx 会取消，Stream 自动停止
    if err := session.Stream(ctx, func(audio []byte) error {
        _, err := w.Write(audio)
        if f, ok := w.(http.Flusher); ok {
            f.Flush() // 立即发送，不等缓冲
        }
        return err
    }); err != nil {
        log.Printf("stream error: %v", err)
    }
}
```

### 5. 方言合成

```go
func synthesizeDialect(client *bdvoice.Client, voiceID int) {
    ctx := context.Background()

    session, err := client.NewTTSSession(ctx, voiceID, &bdvoice.TTSConfig{
        Lang:      bdvoice.LangChinese,    // 方言必须选 "zh"
        Dialect:   bdvoice.DialectSichuan,  // 四川话
        MediaType: bdvoice.MediaMP3,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    // 支持的方言常量：
    // bdvoice.DialectShanghai  — 上海话
    // bdvoice.DialectHenan     — 河南话
    // bdvoice.DialectSichuan   — 四川话
    // bdvoice.DialectHunan     — 湖南话
    // bdvoice.DialectGuizhou   — 贵州话

    _ = session.SendText(ctx, "这是一段四川话合成测试。")
    _ = session.Finish(ctx)

    // ... 读取音频（同上）
}
```

### 6. 流式文本在线合成（公有云预置发音人）

与上述声音复刻 TTS（`NewTTSSession`）不同，公有云流式文本在线合成（`NewStreamTTSSession`）使用百度预置的发音人，无需先创建音色。适合不需要自定义音色、快速集成 TTS 能力的场景。

**两种 TTS 模式对比**：

| 特性 | 声音复刻 TTS (`NewTTSSession`) | 流式文本在线合成 (`NewStreamTTSSession`) |
|------|------|------|
| 发音人 | 自定义复刻音色 (`voice_id`) | 百度预置发音人 (`per`) |
| 前置步骤 | 需先调用 `CreateVoice` | 无，直接使用 |
| 音频格式参数 | `MediaType` (字符串) | `Aue` (数字编码) |
| 采样率控制 | `SampleRate` (整数) | `AudioCtrl` (JSON 字符串) |
| 方言支持 | 通过 `Dialect` 字段 | 通过不同 `per` 值 |
| 会话操作 | SendText/Finish/Read/Stream/Close | **完全相同** |

```go
import (
    "context"
    "fmt"
    "io"
    "log"
    "os"

    "github.com/gtkit/bdvoice"
)

func streamTTS(client *bdvoice.Client) {
    ctx := context.Background()

    // per 是百度预置发音人标识
    // 常用发音人示例：
    //   "0" — 度小美（女声）
    //   "1" — 度小宇（男声）
    //   "3" — 度逍遥（男声，情感合成）
    //   "4" — 度丫丫（女声，童声）
    //   具体发音人列表请参考百度语音合成文档
    session, err := client.NewStreamTTSSession(ctx, "0", &bdvoice.StreamTTSConfig{
        Spd: 5,                        // 语速 0-15，默认 5
        Pit: 5,                        // 音调 0-15，默认 5
        Vol: 5,                        // 音量 0-15（基础音库 0-9），默认 5
        Aue: bdvoice.AudioEncodingMP3, // 音频格式：3=mp3, 4=pcm-16k, 5=pcm-8k, 6=wav
    })
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    // 发送文本（与 NewTTSSession 完全相同的 API）
    // 注意：文本中加上标点有助于服务端及时切句合成，
    // 若无标点，服务端会等待至少 60 字或 5~6 秒超时后才合成
    if err := session.SendText(ctx, "你好，欢迎使用百度流式语音合成服务。"); err != nil {
        log.Fatal(err)
    }
    if err := session.SendText(ctx, "支持多音字标注，如：重(chong2)报集团。"); err != nil {
        log.Fatal(err)
    }

    // 通知服务端所有文本已发送（必须调用，否则可能丢失缓冲文本）
    if err := session.Finish(ctx); err != nil {
        log.Fatal(err)
    }

    // 拉模式读取音频（与 NewTTSSession 完全一致）
    f, _ := os.Create("stream_output.mp3")
    defer f.Close()

    for {
        data, err := session.Read()
        if err == io.EOF {
            break
        }
        if err != nil {
            log.Fatal(err)
        }
        f.Write(data)
    }

    fmt.Println("流式文本在线合成完成")
}

// 使用降采样和推模式的高级示例
func streamTTSAdvanced(client *bdvoice.Client) {
    ctx := context.Background()

    // 链式配置：设置音频格式 + 降采样到 16kHz
    cfg := (&bdvoice.StreamTTSConfig{
        Aue: bdvoice.AudioEncodingMP3,
        Spd: 7, // 稍快语速
    }).WithSampleRate16K() // 便捷方法：设置 audio_ctrl 降采样到 16k

    // 如果需要显式发送 0 值，使用 setter：
    // cfg := (&bdvoice.StreamTTSConfig{}).SetSpd(0).SetPit(0).SetVol(0)

    session, err := client.NewStreamTTSSession(ctx, "4103", cfg)
    if err != nil {
        log.Fatal(err)
    }
    defer session.Close()

    _ = session.SendText(ctx, "这是使用推模式的高级示例。")
    _ = session.Finish(ctx)

    // 推模式（同样与 NewTTSSession 完全一致）
    f, _ := os.Create("stream_advanced.mp3")
    defer f.Close()

    err = session.Stream(ctx, func(audio []byte) error {
        _, err := f.Write(audio)
        return err
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

## 错误处理

SDK 提供了完整的错误类型体系，支持精细化错误处理：

```go
resp, err := client.CreateVoice(ctx, req)
if err != nil {
    switch {
    case bdvoice.IsAPIError(err) != nil:
        // 百度 API 业务错误
        // 常见错误码：216100(参数错误) 216404(voice_id不存在) 282000(服务内部错误)
        apiErr, _ := bdvoice.IsAPIError(err)
        log.Printf("API error: http=%d, code=%d, msg=%s",
            apiErr.StatusCode, apiErr.Code, apiErr.Message)

    case bdvoice.IsOAuthError(err) != nil:
        // OAuth 鉴权错误（client_id/secret 无效等）
        oauthErr, _ := bdvoice.IsOAuthError(err)
        log.Printf("OAuth error: %s", oauthErr.Description)

    case bdvoice.IsValidationError(err) != nil:
        // 客户端参数校验错误（请求发出前就拦截）
        valErr, _ := bdvoice.IsValidationError(err)
        log.Printf("Validation: field=%s, reason=%s", valErr.Field, valErr.Reason)

    default:
        // 网络错误、JSON 解析错误等
        log.Printf("Unexpected error: %v", err)
    }
}
```

### 哨兵错误

```go
import "errors"

// TTS 会话相关
errors.Is(err, bdvoice.ErrSessionClosed)   // 会话已关闭
errors.Is(err, bdvoice.ErrSessionFinished)  // 已调用 Finish，不能再 SendText
errors.Is(err, bdvoice.ErrTextTooLong)      // 单次文本超过 1000 字符

// 客户端配置
errors.Is(err, bdvoice.ErrNoAuth)           // 未配置鉴权方式
```

## 注意事项

### 音色管理
- 同一用户下音色名称不可重复
- 创建的音色若 **1 年内没有调用合成记录**，将被自动删除
- `AudioURL` 和 `AudioFile` 同时传入时，以 `AudioFile` 为准

### 复刻 vs 迁移
- **复刻**：输入语种/方言 == 合成语种/方言（如普通话→普通话）
- **迁移**：输入语种/方言 ≠ 合成语种/方言（如普通话→河南话）
- 建议：音色创建和语音合成时使用一致的 `lang` 参数

### WebSocket TTS（通用）
- 单次 `SendText` 不能超过 1000 个字符
- 可以多次调用 `SendText` 发送长文本
- `TTSSession` **不是并发安全的**，不要在多个 goroutine 中操作同一个 session
- 需要并发合成时，创建多个独立的 session

### 声音复刻 TTS（`NewTTSSession`）
- 发送频率过快时服务端会返回 `216429` 错误
- `idle_timeout` 范围 [5, 600] 秒，默认 60 秒

### 流式文本在线合成（`NewStreamTTSSession`）
- 建议文本不超过 2000 GBK 字节（约 1000 个汉字或字母数字）
- 输入文本必须采用 UTF-8 编码
- 文本中**加上标点**有助于服务端及时切句合成；若无标点，服务端会等待至少 60 字或 5~6 秒超时后才合成
- 有效的隔断标点：`, . 、? ! : ; ， 。 ？ ！ ： ； — …`
- 客户端超过 **1 分钟**不发送消息，服务端会主动断开连接
- 发送完所有文本后**必须调用 `Finish()`**，否则服务端缓冲的文本可能丢失
- 支持多音字标注，格式如：`重(chong2)报集团`
- `Aue` 音频格式：`AudioEncodingMP3`(3)=mp3, `AudioEncodingPCM16K`(4)=pcm-16k, `AudioEncodingPCM8K`(5)=pcm-8k, `AudioEncodingWAV`(6)=wav
- 降采样到 16kHz 使用 `WithSampleRate16K()` 便捷方法

### Read vs Stream 选择指南

| 场景 | 推荐模式 | 原因 |
|------|----------|------|
| 写入本地文件 | Read 或 Stream | 都适合，Stream 代码更简洁 |
| HTTP 流式响应 | Stream | 直接写入 ResponseWriter，支持 context 取消 |
| gRPC stream | Stream | 回调模式天然匹配 stream 发送 |
| 需要统计进度 | Read | 精确控制每帧处理 |
| 边接收边处理 | Read | 可以在两次 Read 之间做其他操作 |
| 条件中断 | Stream | handler 返回 error 即可中止 |

## 依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| [gorilla/websocket](https://github.com/gorilla/websocket) | v1.5.3 | WebSocket 客户端 |
| [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | v0.12.0 | singleflight（token 去重刷新）|

## License

MIT
