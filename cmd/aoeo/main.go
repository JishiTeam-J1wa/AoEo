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

func loadClient() (*aoeo.Client, error) {
	cfg := aoeo.LoadConfigFromEnv()
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("no providers configured; set AOEO_PROVIDER_0_NAME etc.")
	}
	return aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
}

// ---------- list-models ----------

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

// ---------- test ----------

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
		// Find the actual provider to call HealthCheck
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

// ---------- status ----------

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

// ---------- chat ----------

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

// ---------- stream ----------

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

// ---------- privacy ----------

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
	fmt.Printf("Pseudonymized: %d\n", stats.RequestsPseudonymized)
	fmt.Printf("Restored:      %d\n", stats.RequestsRestored)
	fmt.Printf("Failed:        %d\n", stats.RequestsFailed)
	fmt.Printf("Spans detected: %d\n", stats.SpansDetected)
}
