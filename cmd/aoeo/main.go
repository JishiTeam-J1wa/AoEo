// main.go 是 AoEo CLI 命令行工具的入口，提供模型列表、健康检查、聊天补全等子命令。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	aoeo "github.com/JishiTeam-J1wa/AoEo"
	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy"
)

// main 是 AoEo CLI 的入口函数。
// 解析第一个命令行参数作为子命令，分发到对应的处理函数。
// 支持的子命令：list-models、test、status、chat、stream、help。
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "list-models", "models":
		cmdListModels(os.Args[2:])
	case "test":
		cmdTest(os.Args[2:])
	case "status":
		cmdStatus(os.Args[2:])
	case "chat":
		cmdChat(os.Args[2:])
	case "stream":
		cmdStream(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

// usage 打印 CLI 帮助信息，列出所有可用的子命令和使用示例。
func usage() {
	fmt.Println(`AoEo CLI — AI API gateway command line tool

Usage:
  aoeo <command> [flags]

Commands:
  list-models   List available models from all configured providers
  test          Run health checks against all providers
  status        Show provider status, latency, and success rate
  chat          Send a single chat completion
  stream        Send a streaming chat completion
  privacy       Check privacy filter sidecar status

Examples:
  aoeo test
  aoeo status
  aoeo chat -message "Hello, world!"
  aoeo stream -message "Explain Go routines" -provider deepseek`)
}

// loadClient 从环境变量加载配置并创建 AoEo 客户端实例。
//
// Return:
//   - *aoeo.Client: 初始化完成的客户端实例
//   - error: 配置加载失败或未配置任何 Provider 时返回错误
func loadClient() (*aoeo.Client, error) {
	cfg := aoeo.LoadConfigFromEnv()
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("no providers configured; set AOEO_PROVIDER_0_NAME etc.")
	}
	return aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
}

// cmdListModels 处理 list-models 子命令，列出所有已配置 Provider 支持的可用模型。
// 以表格形式输出 Provider 名称、模型 ID 和所属方。
func cmdListModels(args []string) {
	fs := flag.NewFlagSet("list-models", flag.ExitOnError)
	_ = fs.Parse(args)

	client, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tMODEL ID\tOWNED BY")

	for _, ps := range client.ProviderStatus() {
		if !ps.Available {
			fmt.Fprintf(w, "%s\t(unavailable)\t\n", ps.Name)
			continue
		}
		models, err := client.ListModels(ctx, ps.Name)
		if err != nil {
			fmt.Fprintf(w, "%s\t(error: %v)\t\n", ps.Name, err)
			continue
		}
		for _, m := range models {
			fmt.Fprintf(w, "%s\t%s\t%s\n", ps.Name, m.ID, m.OwnedBy)
		}
	}
	w.Flush()
}

// cmdTest 处理 test 子命令，对所有已配置的 Provider 执行健康检查。
// 以表格形式输出各 Provider 的状态、延迟和错误信息。
func cmdTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	_ = fs.Parse(args)

	client, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	statuses := client.ProviderStatus()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tSTATUS\tLATENCY\tMESSAGE")

	for _, ps := range statuses {
		// 查找实际的 Provider 并执行 HealthCheck
		err := client.TestProvider(ctx, ps.Name)
		latency := "—"
		if ps.Health.LastLatencyMs > 0 {
			latency = fmt.Sprintf("%dms", ps.Health.LastLatencyMs)
		}
		if err != nil {
			fmt.Fprintf(w, "%s\tFAIL\t%s\t%v\n", ps.Name, latency, err)
		} else {
			fmt.Fprintf(w, "%s\tOK\t%s\t-\n", ps.Name, latency)
		}
	}
	w.Flush()
}

// cmdStatus 处理 status 子命令，展示各 Provider 的运行状态、
// 平均延迟、成功率和连续失败次数等健康指标。
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	_ = fs.Parse(args)

	client, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tMODEL\tAVAILABLE\tAVG LATENCY\tSUCCESS RATE\tCONSECUTIVE FAILS")

	for _, ps := range client.ProviderStatus() {
		avail := "no"
		if ps.Available {
			avail = "yes"
		}
		latency := "—"
		if ps.Health.AvgLatencyMs > 0 {
			latency = fmt.Sprintf("%dms", ps.Health.AvgLatencyMs)
		}
		rate := fmt.Sprintf("%.0f%%", ps.Health.SuccessRate*100)
		if ps.Health.TotalChecks == 0 {
			rate = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
			ps.Name, ps.Model, avail, latency, rate, ps.Health.ConsecutiveFails)
	}
	w.Flush()
}

// cmdChat 处理 chat 子命令，发送一次同步聊天补全请求并输出响应内容。
// 支持 -message（必选）、-provider、-model、-temperature 等参数。
func cmdChat(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	msg := fs.String("message", "", "Message to send (required)")
	provider := fs.String("provider", "", "Target provider name (optional, uses router)")
	model := fs.String("model", "", "Target model (optional)")
	temp := fs.Float64("temperature", 0.7, "Sampling temperature")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *msg == "" {
		fmt.Fprintln(os.Stderr, "Error: -message is required")
		fs.Usage()
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req := aoeo.BuildRequest(
		[]aoeo.Message{{Role: "user", Content: *msg}},
		aoeo.WithTemperature(float32(*temp)),
	)
	if *model != "" {
		req = aoeo.BuildRequest(req.Messages, aoeo.WithModel(*model))
	}

	var resp *core.ChatCompletionResponse
	if *provider != "" {
		resp, err = client.ChatCompleteWithProvider(ctx, *provider, req)
	} else {
		resp, err = client.ChatComplete(ctx, req)
	}
	if err != nil {
		log.Fatalf("chat complete: %v", err)
	}
	fmt.Println(resp.Content())
}

// cmdStream 处理 stream 子命令，发送一次流式聊天补全请求并实时输出响应内容。
// 支持 -message（必选）、-provider、-model、-temperature 等参数。
func cmdStream(args []string) {
	fs := flag.NewFlagSet("stream", flag.ExitOnError)
	msg := fs.String("message", "", "Message to send (required)")
	provider := fs.String("provider", "", "Target provider name (optional)")
	model := fs.String("model", "", "Target model (optional)")
	temp := fs.Float64("temperature", 0.7, "Sampling temperature")
	if err := fs.Parse(args); err != nil {
		log.Fatal(err)
	}
	if *msg == "" {
		fmt.Fprintln(os.Stderr, "Error: -message is required")
		fs.Usage()
		os.Exit(1)
	}

	client, err := loadClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req := aoeo.BuildRequest(
		[]aoeo.Message{{Role: "user", Content: *msg}},
		aoeo.WithTemperature(float32(*temp)),
	)
	if *model != "" {
		req = aoeo.BuildRequest(req.Messages, aoeo.WithModel(*model))
	}

	var stream <-chan core.StreamCompletionResponse
	if *provider != "" {
		stream, err = client.ChatCompleteStreamWithProvider(ctx, *provider, req)
	} else {
		stream, err = client.ChatCompleteStream(ctx, req)
	}
	if err != nil {
		log.Fatalf("stream: %v", err)
	}
	for chunk := range stream {
		if chunk.Err != nil {
			log.Fatalf("stream error: %v", chunk.Err)
		}
		fmt.Print(chunk.Chunk.Delta.Content)
	}
	fmt.Println()
}

// cmdPrivacy 处理 privacy 子命令，检查隐私过滤 Sidecar 的运行状态和统计信息。
//
// 注意：该函数疑似死代码——main() 的 switch 语句中未注册 "privacy" 子命令，
// 因此当前无法通过 CLI 调用到此函数。usage() 的帮助文本中仍列出了 privacy 命令，
// 建议后续在 main() 的 switch 中补充注册或移除该函数。
func cmdPrivacy(args []string) {
	fs := flag.NewFlagSet("privacy", flag.ExitOnError)
	_ = fs.Parse(args)

	endpoint := os.Getenv("AOEO_PRIVACY_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8080"
	}
	enabled := os.Getenv("AOEO_PRIVACY_ENABLED") == "true"

	fmt.Println("AoEo Privacy Filter")
	fmt.Println("===================")
	fmt.Printf("Enabled:     %v\n", enabled)
	fmt.Printf("Endpoint:    %s\n", endpoint)
	fmt.Printf("Policy:      %s\n", os.Getenv("AOEO_PRIVACY_POLICY"))
	fmt.Printf("FailOpen:    %v\n", os.Getenv("AOEO_PRIVACY_FAILOPEN") == "true")
	fmt.Println()

	if !enabled {
		fmt.Println("Privacy filter is disabled. Set AOEO_PRIVACY_ENABLED=true to enable.")
		return
	}

	gw, err := privacy.NewGateway(privacy.GatewayConfig{ModelEndpoint: endpoint})
	if err != nil {
		fmt.Printf("Gateway init: %v\n", err)
		return
	}
	defer gw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if gw.HealthCheck(ctx) {
		fmt.Println("Sidecar:     OK")
	} else {
		fmt.Println("Sidecar:     UNREACHABLE")
	}

	stats := gw.Stats()
	fmt.Printf("Pseudonymized: %d\n", stats.RequestsPseudonymized.Load())
	fmt.Printf("Restored:      %d\n", stats.RequestsRestored.Load())
	fmt.Printf("Failed:        %d\n", stats.RequestsFailed.Load())
	fmt.Printf("Spans detected: %d\n", stats.SpansDetected.Load())
}
