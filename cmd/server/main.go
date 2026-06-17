// AoEo Gateway 服务入口，将 AoEo SDK 包装为 OpenAI 兼容的 HTTP API 网关。
//
// 启动流程：
//  1. 解析命令行参数（--config, --addr）
//  2. 从 YAML 文件加载配置
//  3. 创建 AoEo SDK 客户端
//  4. 注册所有 HTTP 路由（聊天补全、模型列表、健康检查、管理接口）
//  5. 应用认证/日志中间件
//  6. 启动 HTTP 服务器
//  7. 等待 SIGINT/SIGTERM 信号后优雅关闭
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	aoeo "github.com/JishiTeam-J1wa/AoEo"
	"github.com/JishiTeam-J1wa/AoEo/config"
	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/server"
)

// version 网关版本号，在启动横幅中显示。
const version = "0.1.0"

func main() {
	// 解析命令行参数
	configPath := flag.String("config", "aoeo.yaml", "配置文件路径")
	addrOverride := flag.String("addr", "", "覆盖配置中的监听地址（如 :8081）")
	flag.Parse()

	// 加载 YAML 配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("[AoEo] 配置加载失败: %v", err)
	}

	// 覆盖监听地址
	if *addrOverride != "" {
		cfg.Server.Addr = *addrOverride
	}

	// 转换为 SDK 核心配置并创建客户端
	coreCfg := cfg.ToCoreConfig()
	router := cfg.BuildRouter()

	opts := []aoeo.SchedulerOption{
		aoeo.WithRouter(router),
	}

	// 配置重试策略
	if cfg.Retry.MaxRetries > 0 {
		retryCfg := core.RetryConfig{
			MaxRetries: cfg.Retry.MaxRetries,
			BaseDelay:  cfg.Retry.BaseDelay,
			MaxDelay:   cfg.Retry.MaxDelay,
			Multiplier: cfg.Retry.Multiplier,
			Retryable:  core.IsRetryableError,
		}
		opts = append(opts, aoeo.WithRetry(retryCfg))
	}

	// 配置历史记录
	if cfg.History.RingSize > 0 {
		history := aoeo.NewHistory(cfg.History.RingSize)
		opts = append(opts, aoeo.WithHistory(history))
	}

	// 创建 AoEo SDK 客户端
	client, err := aoeo.NewClient(coreCfg, opts...)
	if err != nil {
		log.Fatalf("[AoEo] 客户端创建失败: %v", err)
	}
	defer client.Close()

	// 启动后台健康检查
	if cfg.HealthCheck.Interval > 0 {
		client.SetHealthCheckInterval(cfg.HealthCheck.Interval)
	}

	// 创建 HTTP 服务器包装器
	srv := server.NewServer(client)

	// 配置热重载回调
	reloadFn := func() error {
		newCfg, err := config.LoadConfig(*configPath)
		if err != nil {
			return err
		}
		newCoreCfg := newCfg.ToCoreConfig()
		return client.Scheduler().ApplyConfig(newCoreCfg)
	}

	// 注册路由
	mux := http.NewServeMux()

	// OpenAI 兼容接口
	mux.HandleFunc("POST /v1/chat/completions", srv.ChatHandler)
	mux.HandleFunc("GET /v1/models", srv.ModelsHandler)

	// 健康检查与指标（由另一个 agent 实现）
	mux.HandleFunc("GET /healthz", server.HealthHandler())
	mux.HandleFunc("GET /readyz", server.ReadyHandler(client.ProviderStatus))
	mux.HandleFunc("GET /metrics", server.MetricsHandler(client.ProviderStatus))

	// 管理接口
	mux.HandleFunc("GET /admin/providers", srv.ProviderStatusHandler)
	mux.HandleFunc("GET /admin/stats", srv.StatsHandler)
	mux.HandleFunc("PUT /admin/config/reload", server.ReloadHandler(reloadFn))
	mux.HandleFunc("POST /admin/providers/{name}/test", srv.TestProviderHandler)

	// 应用中间件链（认证、日志、恢复等，由另一个 agent 实现）
	handler := server.Chain(mux, cfg.Server.APIKey)

	// 创建 HTTP 服务器
	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// 优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 启动 HTTP 服务器
	go func() {
		log.Printf("[AoEo] Gateway v%s listening on %s", version, cfg.Server.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[AoEo] HTTP 服务器异常退出: %v", err)
		}
	}()

	// 等待关闭信号
	<-ctx.Done()
	log.Println("[AoEo] 收到关闭信号，正在优雅关闭...")

	// 给予 30 秒的关闭超时
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[AoEo] HTTP 服务器关闭超时: %v", err)
	}

	if err := client.Close(); err != nil {
		log.Printf("[AoEo] 客户端关闭出错: %v", err)
	}

	log.Println("[AoEo] Gateway 已关闭")
}
