// Package config 配置文件热重载监听器，基于轮询机制检测文件变更并触发回调。
//
// ConfigWatcher 通过定期检查文件修改时间（ModTime）实现配置热重载，
// 无需引入 fsnotify 等外部依赖，适用于容器和嵌入式环境。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package config

import (
	"log"
	"os"
	"sync"
	"time"
)

// ConfigWatcher 轮询式配置文件监听器，检测文件修改并触发回调。
//
// 使用 os.Stat 获取文件 ModTime，当检测到文件被修改时重新加载配置，
// 并通过 onChange 回调通知调用方。所有操作均为协程安全。
type ConfigWatcher struct {
	path     string            // 被监听的配置文件路径
	interval time.Duration     // 轮询间隔（默认 5s）
	onChange func(*AoEoConfig) // 配置变更回调函数
	stopCh   chan struct{}     // 停止信号通道
	lastMod  time.Time         // 上次记录的文件修改时间
	mu       sync.Mutex        // 保护 lastMod 字段的并发访问
	running  bool              // 标记监听器是否正在运行
}

// NewConfigWatcher 创建一个新的配置文件监听器实例。
//
// Param:
//   - path: string - 被监听的 YAML 配置文件路径
//   - interval: time.Duration - 轮询间隔，为 0 时使用默认值 5s
//   - onChange: func(*AoEoConfig) - 配置变更时调用的回调函数，不可为 nil
//
// Return:
//   - *ConfigWatcher: 初始化完成的监听器实例
func NewConfigWatcher(path string, interval time.Duration, onChange func(*AoEoConfig)) *ConfigWatcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &ConfigWatcher{
		path:     path,
		interval: interval,
		onChange: onChange,
		stopCh:   make(chan struct{}),
	}
}

// Start 启动轮询协程，开始监听配置文件变更。
//
// 启动时会立即执行一次检查以记录当前文件 ModTime 作为基准。
// 后续按照 interval 间隔定期检测文件变更。
// 若监听器已在运行，重复调用 Start 为无操作（幂等）。
func (w *ConfigWatcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	// 记录初始 ModTime 作为基准
	w.updateLastMod()

	go w.poll()
}

// Stop 停止轮询协程，关闭监听器。
//
// 发送停止信号并等待协程退出。若监听器未运行，调用 Stop 为无操作（幂等）。
func (w *ConfigWatcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	w.mu.Unlock()

	close(w.stopCh)
}

// poll 轮询主循环，在独立协程中运行。
//
// 每次检测到文件 ModTime 变化时，重新加载配置并调用 onChange 回调。
// 所有错误仅记录日志，不会导致协程崩溃。
func (w *ConfigWatcher) poll() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkForChanges()
		}
	}
}

// checkForChanges 检查配置文件是否被修改，若检测到变更则重新加载并触发回调。
//
// 比较当前文件 ModTime 与上次记录的值，不同时触发 LoadConfig。
// 加载失败时记录错误日志，不中断轮询流程。
func (w *ConfigWatcher) checkForChanges() {
	info, err := os.Stat(w.path)
	if err != nil {
		log.Printf("[ConfigWatcher] 获取文件状态失败 %s: %v", w.path, err)
		return
	}

	w.mu.Lock()
	lastMod := w.lastMod
	w.mu.Unlock()

	// ModTime 未变化则跳过
	if !info.ModTime().After(lastMod) {
		return
	}

	log.Printf("[ConfigWatcher] 检测到配置文件变更，重新加载: %s", w.path)

	// 重新加载配置
	cfg, err := LoadConfig(w.path)
	if err != nil {
		log.Printf("[ConfigWatcher] 重新加载配置失败: %v", err)
		return
	}

	// 更新 ModTime 基准
	w.mu.Lock()
	w.lastMod = info.ModTime()
	w.mu.Unlock()

	// 触发回调通知调用方
	if w.onChange != nil {
		w.onChange(cfg)
	}
}

// updateLastMod 读取文件当前 ModTime 并记录为基准值。
//
// 仅在 Start 时调用一次，用于初始化轮询的基准比较时间。
// 文件不存在或无法访问时记录错误日志。
func (w *ConfigWatcher) updateLastMod() {
	info, err := os.Stat(w.path)
	if err != nil {
		log.Printf("[ConfigWatcher] 初始化 ModTime 失败 %s: %v", w.path, err)
		return
	}

	w.mu.Lock()
	w.lastMod = info.ModTime()
	w.mu.Unlock()
}
