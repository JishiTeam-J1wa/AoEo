package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync/atomic"
)

// WeightStrategy 定义加权路由器的评分策略。
type WeightStrategy int

const (
	StrategyLatency     WeightStrategy = iota // 延迟越低，权重越高
	StrategySuccessRate                       // 成功率越高，权重越高
	StrategyCombined                          // 综合评分：延迟 50% + 成功率 50%
)

// SingleProviderRouter 强制选择指定名称的 Provider。
// 适用于 CLI 工具和调试场景，需要固定请求某个特定 Provider。
type SingleProviderRouter struct {
	Name string
}

// Select 从候选列表中查找与指定名称匹配且可用的 Provider。
// 如果找不到，返回包含 Provider 名称的错误信息。
func (r *SingleProviderRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	for i, c := range candidates {
		if c.Name == r.Name && c.Available {
			return i, nil
		}
	}
	return -1, fmt.Errorf("provider %q not available", r.Name)
}

// SelectSequence 返回仅包含目标 Provider 索引的单元素序列。
func (r *SingleProviderRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	idx, err := r.Select(ctx, candidates, req)
	if err != nil {
		return nil, err
	}
	return []int{idx}, nil
}

// Router 接口决定对给定请求使用哪个 Provider。
// 实现必须是并发安全的。
type Router interface {
	// Select 从候选列表中选择一个 Provider。
	// 返回候选列表中的索引，如果没有合适的 Provider 则返回 error。
	Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error)

	// SelectSequence 返回一个有序的候选索引列表，用于降级重试场景。
	// 调度器按顺序依次尝试，直到某个 Provider 成功响应。
	SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error)
}

// PrimaryRouter 始终选择第一个可用的 Provider。
// 适用于主备模式，优先使用排名靠前的 Provider。
type PrimaryRouter struct{}

// Select 返回第一个可用 Provider 的索引。
func (r *PrimaryRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	for i, c := range candidates {
		if c.Available {
			return i, nil
		}
	}
	return -1, errors.New("no available provider")
}

// SelectSequence 返回所有可用 Provider 的索引序列，按原始顺序排列。
func (r *PrimaryRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	return seq, nil
}

// RoundRobinRouter 以轮询方式将请求均匀分配到所有可用的 Provider。
// 每次调用 Select 都会依次选择下一个 Provider，保证负载均匀分布。
type RoundRobinRouter struct {
	idx atomic.Uint64
}

// Select 使用原子计数器轮询选择下一个可用 Provider。
func (r *RoundRobinRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	avail := make([]int, 0, len(candidates))
	for i, c := range candidates {
		if c.Available {
			avail = append(avail, i)
		}
	}
	if len(avail) == 0 {
		return -1, errors.New("no available provider")
	}
	next := int(r.idx.Add(1)-1) % len(avail)
	return avail[next], nil
}

// SelectSequence 返回所有可用 Provider 的索引序列，起始点随计数器轮转。
func (r *RoundRobinRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	// 轮转起始点以增加多样性，同时保留完整序列用于降级重试
	if len(seq) > 1 {
		start := int(r.idx.Add(1)-1) % len(seq)
		rotated := make([]int, len(seq))
		for i := range seq {
			rotated[i] = seq[(start+i)%len(seq)]
		}
		return rotated, nil
	}
	return seq, nil
}

// RandomRouter 从所有可用的 Provider 中随机选择一个。
// 使用 math/rand/v2 实现真正的随机选择，每次调用结果不可预测。
type RandomRouter struct{}

// Select 使用 math/rand/v2 随机选择一个可用 Provider。
func (r *RandomRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	var avail []int
	for i, c := range candidates {
		if c.Available {
			avail = append(avail, i)
		}
	}
	if len(avail) == 0 {
		return -1, errors.New("no available provider")
	}
	return avail[rand.IntN(len(avail))], nil
}

// SelectSequence 返回所有可用 Provider 的随机排列序列，用于降级重试。
func (r *RandomRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	var seq []int
	for i, c := range candidates {
		if c.Available {
			seq = append(seq, i)
		}
	}
	if len(seq) == 0 {
		return nil, errors.New("no available provider")
	}
	// 使用 Fisher-Yates 洗牌算法随机打乱序列
	rand.Shuffle(len(seq), func(i, j int) {
		seq[i], seq[j] = seq[j], seq[i]
	})
	return seq, nil
}

// WeightedRouter 基于运行时健康指标（延迟、成功率等）对 Provider 进行加权评分选择。
// 评分越高的 Provider 被选中的概率越大。使用确定性计数器实现加权随机。
type WeightedRouter struct {
	Strategy WeightStrategy
	counter  atomic.Uint64
}

// score 根据配置的评分策略计算 Provider 的权重分数。
// 分数范围始终在 [0, 1] 之间。
func (r *WeightedRouter) score(status ProviderStatus) float64 {
	h := status.Health
	switch r.Strategy {
	case StrategyLatency:
		// 延迟越低分数越高，使用类 Sigmoid 衰减函数
		// 0ms -> 1.0, 1000ms -> ~0.5, 5000ms -> ~0.17
		return 1.0 / (1.0 + float64(h.AvgLatencyMs)/1000.0)
	case StrategySuccessRate:
		return h.SuccessRate
	case StrategyCombined:
		latencyScore := 1.0 / (1.0 + float64(h.AvgLatencyMs)/1000.0)
		return latencyScore*0.5 + h.SuccessRate*0.5
	default:
		return 1.0
	}
}

// Select 按加权评分随机选择一个可用 Provider，权重越高被选中概率越大。
func (r *WeightedRouter) Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error) {
	type scored struct {
		idx    int
		score  float64
		weight float64
	}
	var scoredList []scored
	var totalWeight float64
	for i, c := range candidates {
		if c.Available {
			s := r.score(c)
			if s > 0 {
				scoredList = append(scoredList, scored{idx: i, score: s, weight: s})
				totalWeight += s
			}
		}
	}
	if len(scoredList) == 0 {
		return -1, errors.New("no available provider")
	}
	if len(scoredList) == 1 {
		return scoredList[0].idx, nil
	}

	// 确定性加权随机选择：使用黄金比例常数改善计数器分布均匀性
	h := r.counter.Add(1)
	step := uint64(0x9E3779B97F4A7C15) // 黄金比例常数，用于改善分布均匀性
	target := float64(h*step%math.MaxUint64) / float64(math.MaxUint64) * totalWeight
	var accum float64
	for _, s := range scoredList {
		accum += s.weight
		if accum >= target {
			return s.idx, nil
		}
	}
	return scoredList[len(scoredList)-1].idx, nil
}

// SelectSequence 返回按评分降序排列的可用 Provider 索引序列，用于降级重试。
func (r *WeightedRouter) SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error) {
	type scored struct {
		idx   int
		score float64
	}
	var scoredList []scored
	for i, c := range candidates {
		if c.Available {
			s := r.score(c)
			scoredList = append(scoredList, scored{idx: i, score: s})
		}
	}
	if len(scoredList) == 0 {
		return nil, errors.New("no available provider")
	}

	// 按评分降序排序（对少量元素使用冒泡排序，保证确定性）
	for i := 0; i < len(scoredList)-1; i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}

	seq := make([]int, len(scoredList))
	for i, s := range scoredList {
		seq[i] = s.idx
	}
	return seq, nil
}
