// Package outbound 提供出站连接处理实现
package outbound

import (
	"context"
	"net"

	"github.com/SxAllyn/zpt/internal/loopback"
)

// LoopbackConfig 环回出站配置
type LoopbackConfig struct {
	// BufferSize 缓冲区大小
	BufferSize int
}

// DefaultLoopbackConfig 返回默认环回配置
func DefaultLoopbackConfig() LoopbackConfig {
	return LoopbackConfig{
		BufferSize: 1024,
	}
}

// LoopbackOutbound 环回出站实现，用于测试
type LoopbackOutbound struct {
	config LoopbackConfig
	pair   *loopback.Pair
}

// NewLoopbackOutbound 创建环回出站
func NewLoopbackOutbound(config LoopbackConfig) *LoopbackOutbound {
	return &LoopbackOutbound{
		config: config,
		pair:   loopback.NewPair(config.BufferSize),
	}
}

// NewLoopbackOutboundWithPair 使用现有环回对创建出站
func NewLoopbackOutboundWithPair(pair *loopback.Pair) *LoopbackOutbound {
	return &LoopbackOutbound{
		config: DefaultLoopbackConfig(),
		pair:   pair,
	}
}

// Dial 拨号到环回地址
func (l *LoopbackOutbound) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return l.DialContext(ctx, network, address)
}

// DialContext 使用上下文拨号到环回地址
func (l *LoopbackOutbound) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// 忽略网络和地址参数，直接创建环回连接
	return l.pair.Dial()
}

// Handle 处理出站连接（环回出站不需要处理）
func (l *LoopbackOutbound) Handle(ctx context.Context, conn net.Conn, target string) error {
	// 环回出站不需要额外处理
	return nil
}

// Close 关闭出站
func (l *LoopbackOutbound) Close() error {
	// 注意：这里不关闭pair，因为可能与其他组件共享
	return nil
}

// GetPair 获取环回对（用于与入站共享）
func (l *LoopbackOutbound) GetPair() *loopback.Pair {
	return l.pair
}
