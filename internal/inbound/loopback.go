// Package inbound 提供入站连接处理实现
package inbound

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/SxAllyn/zpt/internal/loopback"
)

// LoopbackConfig 环回入站配置
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

// LoopbackInbound 环回入站实现，用于测试
type LoopbackInbound struct {
	config    LoopbackConfig
	pair      *loopback.Pair
	closeCh   chan struct{}
	closeOnce sync.Once
	addr      net.Addr
}

// NewLoopbackInbound 创建环回入站
func NewLoopbackInbound(config LoopbackConfig) *LoopbackInbound {
	return &LoopbackInbound{
		config:  config,
		pair:    loopback.NewPair(config.BufferSize),
		closeCh: make(chan struct{}),
		addr:    &loopbackAddr{},
	}
}

// NewLoopbackInboundWithPair 使用现有环回对创建入站
func NewLoopbackInboundWithPair(pair *loopback.Pair) *LoopbackInbound {
	return &LoopbackInbound{
		config:  DefaultLoopbackConfig(),
		pair:    pair,
		closeCh: make(chan struct{}),
		addr:    &loopbackAddr{},
	}
}

// Listen 开始监听（对于环回入站，无需实际监听）
func (l *LoopbackInbound) Listen(ctx context.Context) error {
	// 环回入站不需要实际监听，直接返回
	return nil
}

// Accept 接受新连接（从环回对中获取连接）
func (l *LoopbackInbound) Accept() (net.Conn, error) {
	select {
	case <-l.closeCh:
		return nil, errors.New("环回入站已关闭")
	default:
	}

	return l.pair.Accept()
}

// Close 关闭监听器
func (l *LoopbackInbound) Close() error {
	l.closeOnce.Do(func() {
		close(l.closeCh)
		// 关闭环回对
		l.pair.Close()
	})
	return nil
}

// Addr 返回监听地址
func (l *LoopbackInbound) Addr() net.Addr {
	return l.addr
}

// Handle 处理传入的连接（将连接放入环回对）
func (l *LoopbackInbound) Handle(ctx context.Context, conn net.Conn) error {
	// 环回入站不需要处理外部连接
	// 连接通过环回对内部创建
	return errors.New("环回入站不支持外部连接处理")
}

// GetPair 获取环回对（用于与出站共享）
func (l *LoopbackInbound) GetPair() *loopback.Pair {
	return l.pair
}

// loopbackAddr 环回地址实现
type loopbackAddr struct{}

func (a *loopbackAddr) Network() string { return "loopback" }
func (a *loopbackAddr) String() string  { return "loopback:0" }
