// Package outbound 提供出站连接处理实现
package outbound

import (
	"context"
	"net"
)

// Dialer 出站连接拨号器接口
type Dialer interface {
	// Dial 建立到目标地址的连接
	Dial(ctx context.Context, network, address string) (net.Conn, error)
	// DialContext 使用上下文建立连接
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Handler 出站连接处理器接口
type Handler interface {
	// Handle 处理出站连接
	Handle(ctx context.Context, conn net.Conn, target string) error
}

// BaseOutbound 基础出站结构
type BaseOutbound struct {
	config  interface{}
	dialer  Dialer
	handler Handler
}
