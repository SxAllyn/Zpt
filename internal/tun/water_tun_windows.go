//go:build windows
// +build windows

// Windows平台TUN设备实现（基于water库）
package tun

import (
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/songgao/water"
)

// WaterDevice 基于water库的Windows TUN设备
type WaterDevice struct {
	config  *Config
	ifce    *water.Interface
	stats   *DeviceStats
	running atomic.Bool
}

// NewWaterDevice 创建基于water的Windows TUN设备
func NewWaterDevice(config *Config) (*WaterDevice, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// 创建water配置
	waterConfig := water.Config{
		DeviceType: water.TUN,
	}

	// 创建TUN接口
	ifce, err := water.New(waterConfig)
	if err != nil {
		return nil, fmt.Errorf("创建TUN设备失败: %w", err)
	}

	device := &WaterDevice{
		config: config,
		ifce:   ifce,
		stats:  &DeviceStats{},
	}

	return device, nil
}

// Start 启动设备（配置IP地址和路由）
func (w *WaterDevice) Start() error {
	if w.running.Load() {
		return fmt.Errorf("设备已在运行")
	}

	// 设置IP地址
	if w.config.Address != "" {
		// Windows平台需要调用netsh命令配置IP地址
		// 这里简化处理，实际需要调用系统API或外部命令
		// 暂时跳过，由用户手动配置
	}

	w.running.Store(true)
	return nil
}

// Stop 停止设备
func (w *WaterDevice) Stop() error {
	if !w.running.Load() {
		return fmt.Errorf("设备未运行")
	}

	w.running.Store(false)
	if w.ifce != nil {
		return w.ifce.Close()
	}
	return nil
}

// ReadPacket 读取数据包
func (w *WaterDevice) ReadPacket() (*Packet, error) {
	if !w.running.Load() {
		return nil, fmt.Errorf("设备未运行")
	}

	buf := make([]byte, w.config.MTU+20) // MTU + 头部开销
	n, err := w.ifce.Read(buf)
	if err != nil {
		w.stats.Errors++
		return nil, err
	}

	packet := &Packet{
		Data:      buf[:n],
		Timestamp: time.Now().UnixNano(),
	}

	// 解析IP头部获取协议类型和地址
	if n >= 20 && buf[0]>>4 == 4 { // IPv4
		packet.Protocol = "IPv4"
		packet.Src = net.IP(buf[12:16])
		packet.Dst = net.IP(buf[16:20])
	} else if n >= 40 && buf[0]>>4 == 6 { // IPv6
		packet.Protocol = "IPv6"
		packet.Src = net.IP(buf[8:24])
		packet.Dst = net.IP(buf[24:40])
	} else {
		packet.Protocol = "Unknown"
	}

	w.stats.PacketsReceived++
	w.stats.BytesReceived += uint64(n)
	return packet, nil
}

// WritePacket 写入数据包
func (w *WaterDevice) WritePacket(packet *Packet) error {
	if !w.running.Load() {
		return fmt.Errorf("设备未运行")
	}

	if packet == nil || len(packet.Data) == 0 {
		return fmt.Errorf("无效的数据包")
	}

	_, err := w.ifce.Write(packet.Data)
	if err != nil {
		w.stats.Errors++
		return err
	}

	w.stats.PacketsSent++
	w.stats.BytesSent += uint64(len(packet.Data))
	return nil
}

// Config 获取设备配置
func (w *WaterDevice) Config() *Config {
	return w.config
}

// FD 获取设备文件描述符（Windows平台暂不支持）
func (w *WaterDevice) FD() (uintptr, error) {
	// Windows平台的water库未暴露文件描述符/句柄
	// 返回错误，调用方应处理此情况
	return 0, fmt.Errorf("Windows平台暂不支持文件描述符获取")
}

// Stats 获取设备统计信息
func (w *WaterDevice) Stats() *DeviceStats {
	return w.stats
}

// IsRunning 检查设备是否运行
func (w *WaterDevice) IsRunning() bool {
	return w.running.Load()
}
