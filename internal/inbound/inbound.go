// Package inbound 提供入站连接处理实现
package inbound

import (
	"context"
	"net"
)

// Handler 入站连接处理器接口
type Handler interface {
	// Handle 处理传入的连接
	Handle(ctx context.Context, conn net.Conn) error
}

// Listener 入站监听器接口
type Listener interface {
	// Listen 开始监听连接
	Listen(ctx context.Context) error
	// Accept 接受新连接
	Accept() (net.Conn, error)
	// Close 关闭监听器
	Close() error
	// Addr 返回监听地址
	Addr() net.Addr
}

// BaseInbound 基础入站结构
type BaseInbound struct {
	config   interface{}
	handler  Handler
	listener Listener
}
