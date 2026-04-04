// Package tunproxy 提供TUN设备与代理出站的集成，实现透明代理功能
package tunproxy

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SxAllyn/zpt/internal/outbound"
	"github.com/SxAllyn/zpt/internal/router"
	"github.com/SxAllyn/zpt/internal/tun"
)

// devicePacketSender 包装TUN设备实现PacketSender接口
type devicePacketSender struct {
	device tun.Device
}

func (d *devicePacketSender) SendPacketToTUN(packet *tun.Packet) error {
	return d.device.WritePacket(packet)
}

// tunProxyPacketSender 包装TUN代理实现PacketSender接口
type tunProxyPacketSender struct {
	proxy *TUNProxy
}

func (t *tunProxyPacketSender) SendPacketToTUN(packet *tun.Packet) error {
	return t.proxy.SendPacketToTUN(packet)
}

// Config TUN代理配置
type Config struct {
	// TUN设备配置
	TUNConfig *tun.Config
	// 路由规则列表
	Routes []*router.Route
	// 出站拨号器（如Zop出站）
	OutboundDialer outbound.Dialer
	// 代理接口名称
	ProxyInterface string
	// 最大并发连接数
	MaxConnections int
	// 连接超时
	ConnectTimeout time.Duration
	// 是否启用调试日志
	Debug bool
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		TUNConfig: tun.DefaultConfig(),
		Routes: []*router.Route{
			{
				Destination: "0.0.0.0/0", // 默认路由
				Interface:   "proxy",     // 默认通过代理接口
				Priority:    100,
				Enabled:     true,
			},
		},
		OutboundDialer: nil, // 必须由调用者提供
		ProxyInterface: "proxy",
		MaxConnections: 1024,
		ConnectTimeout: 30 * time.Second,
		Debug:          false,
	}
}

// TUNProxy TUN代理主管理器
type TUNProxy struct {
	config     *Config
	device     tun.Device         // TUN设备
	router     router.Router      // 路由引擎
	dialer     outbound.Dialer    // 出站拨号器
	proxyIface *ProxyInterface    // 代理接口处理器
	connTrack  *ConnectionTracker // 连接跟踪器
	stats      *TUNProxyStats     // 统计信息
	running    atomic.Bool        // 运行状态
	stopChan   chan struct{}      // 停止信号
	wg         sync.WaitGroup     // 等待组
	mu         sync.RWMutex       // 保护状态
}

// TUNProxyStats 代理统计信息
type TUNProxyStats struct {
	PacketsReceived uint64
	PacketsSent     uint64
	BytesReceived   uint64
	BytesSent       uint64
	Connections     uint64
	Errors          uint64
}

// NewTUNProxy 创建TUN代理实例
func NewTUNProxy(config *Config) (*TUNProxy, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if config.OutboundDialer == nil {
		return nil, fmt.Errorf("出站拨号器不能为空")
	}

	// 创建TUN设备
	device, err := tun.NewDevice(config.TUNConfig)
	if err != nil {
		return nil, fmt.Errorf("创建TUN设备失败: %w", err)
	}

	// 创建路由引擎
	router := router.NewDefaultRouter()

	// 创建连接跟踪器
	connTrack := NewConnectionTracker(config.MaxConnections)

	// 创建TUN代理实例（proxyIface暂为nil，稍后设置）
	proxy := &TUNProxy{
		config:    config,
		device:    device,
		router:    router,
		dialer:    config.OutboundDialer,
		connTrack: connTrack,
		stats:     &TUNProxyStats{},
		stopChan:  make(chan struct{}),
	}

	// 创建数据包发送器（包装TUN代理）
	packetSender := &tunProxyPacketSender{proxy: proxy}

	// 创建代理接口处理器
	proxyIface := NewProxyInterface(config.OutboundDialer, config.ConnectTimeout, packetSender)
	proxy.proxyIface = proxyIface

	return proxy, nil
}

// Start 启动TUN代理
func (p *TUNProxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running.Load() {
		return fmt.Errorf("TUN代理已在运行")
	}

	// 启动TUN设备
	if err := p.device.Start(); err != nil {
		return fmt.Errorf("启动TUN设备失败: %w", err)
	}

	// 启动路由引擎
	if err := p.router.Start(); err != nil {
		p.device.Stop()
		return fmt.Errorf("启动路由引擎失败: %w", err)
	}

	// 注册代理接口
	if err := p.router.RegisterInterface(p.config.ProxyInterface, p.proxyIface); err != nil {
		p.router.Stop()
		p.device.Stop()
		return fmt.Errorf("注册代理接口失败: %w", err)
	}

	// 添加路由规则
	for _, route := range p.config.Routes {
		if err := p.router.AddRoute(route); err != nil {
			p.router.UnregisterInterface(p.config.ProxyInterface)
			p.router.Stop()
			p.device.Stop()
			return fmt.Errorf("添加路由规则失败: %w", err)
		}
	}

	// 启动数据包处理循环
	p.running.Store(true)
	p.wg.Add(1)
	go p.packetLoop()

	if p.config.Debug {
		fmt.Printf("TUN代理已启动，设备: %v\n", p.device.Config().Name)
	}

	return nil
}

// Stop 停止TUN代理
func (p *TUNProxy) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running.Load() {
		return fmt.Errorf("TUN代理未运行")
	}

	// 发送停止信号
	close(p.stopChan)
	p.running.Store(false)

	// 等待处理循环结束
	p.wg.Wait()

	// 注销接口
	p.router.UnregisterInterface(p.config.ProxyInterface)

	// 停止路由引擎
	p.router.Stop()

	// 停止TUN设备
	p.device.Stop()

	// 关闭连接跟踪器
	p.connTrack.Close()

	if p.config.Debug {
		fmt.Println("TUN代理已停止")
	}

	return nil
}

// packetLoop 数据包处理循环
func (p *TUNProxy) packetLoop() {
	defer p.wg.Done()

	for {
		select {
		case <-p.stopChan:
			return
		default:
			// 读取数据包
			packet, err := p.device.ReadPacket()
			if err != nil {
				if p.running.Load() {
					p.stats.Errors++
					if p.config.Debug {
						fmt.Printf("读取数据包失败: %v\n", err)
					}
				}
				continue
			}

			p.stats.PacketsReceived++
			p.stats.BytesReceived += uint64(len(packet.Data))

			// 创建数据包上下文
			ctx := &router.PacketContext{
				Packet:         packet.Data,
				Src:            packet.Src,
				Dst:            packet.Dst,
				Protocol:       packet.Protocol,
				Timestamp:      packet.Timestamp,
				InputInterface: "tun",
			}

			// 路由数据包
			if err := p.router.RoutePacket(ctx); err != nil {
				p.stats.Errors++
				if p.config.Debug {
					fmt.Printf("路由数据包失败: %v\n", err)
				}
			}
		}
	}
}

// Stats 获取统计信息
func (p *TUNProxy) Stats() *TUNProxyStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := *p.stats
	return &stats
}

// IsRunning 检查代理是否运行中
func (p *TUNProxy) IsRunning() bool {
	return p.running.Load()
}

// GetDevice 获取TUN设备（用于测试）
func (p *TUNProxy) GetDevice() tun.Device {
	return p.device
}

// GetRouter 获取路由引擎（用于测试）
func (p *TUNProxy) GetRouter() router.Router {
	return p.router
}

// SendPacketToTUN 发送数据包到TUN设备（实现PacketSender接口）
func (p *TUNProxy) SendPacketToTUN(packet *tun.Packet) error {
	if !p.running.Load() {
		return fmt.Errorf("TUN代理未运行")
	}

	// 写入TUN设备
	err := p.device.WritePacket(packet)
	if err != nil {
		p.stats.Errors++
		return fmt.Errorf("写入TUN设备失败: %w", err)
	}

	// 更新统计
	p.stats.PacketsSent++
	p.stats.BytesSent += uint64(len(packet.Data))

	return nil
}
