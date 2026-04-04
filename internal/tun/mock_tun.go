// Mock TUN设备实现，用于测试和开发
package tun

import (
	"errors"
	"sync"
	"time"
)

// MockDevice 模拟TUN设备
type MockDevice struct {
	config  *Config
	running bool
	stats   *DeviceStats
	mu      sync.RWMutex
	// 数据包通道
	packetChan chan *Packet
	// 停止通道
	stopChan chan struct{}
	// 模拟延迟（微秒）
	simulateLatency time.Duration
}

// NewMockDevice 创建Mock TUN设备
func NewMockDevice(config *Config) (*MockDevice, error) {
	if config == nil {
		config = DefaultConfig()
	}

	return &MockDevice{
		config:          config,
		running:         false,
		stats:           &DeviceStats{},
		packetChan:      make(chan *Packet, 1000),
		stopChan:        make(chan struct{}),
		simulateLatency: 0,
	}, nil
}

// Start 启动Mock设备
func (d *MockDevice) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.running {
		return errors.New("设备已在运行")
	}

	d.running = true
	d.stats = &DeviceStats{}
	return nil
}

// Stop 停止Mock设备
func (d *MockDevice) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return errors.New("设备未运行")
	}

	d.running = false
	close(d.stopChan)
	close(d.packetChan)
	return nil
}

// ReadPacket 读取数据包（模拟）
func (d *MockDevice) ReadPacket() (*Packet, error) {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return nil, errors.New("设备未运行")
	}

	// 模拟网络延迟
	if d.simulateLatency > 0 {
		time.Sleep(d.simulateLatency)
	}

	select {
	case packet := <-d.packetChan:
		d.mu.Lock()
		d.stats.PacketsReceived++
		d.stats.BytesReceived += uint64(len(packet.Data))
		d.mu.Unlock()
		return packet, nil
	case <-d.stopChan:
		return nil, errors.New("设备已停止")
	}
}

// WritePacket 写入数据包（模拟）
func (d *MockDevice) WritePacket(packet *Packet) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return errors.New("设备未运行")
	}

	// 模拟网络延迟
	if d.simulateLatency > 0 {
		time.Sleep(d.simulateLatency)
	}

	d.mu.Lock()
	d.stats.PacketsSent++
	d.stats.BytesSent += uint64(len(packet.Data))
	d.mu.Unlock()

	// 将数据包放入接收队列（模拟回环）
	select {
	case d.packetChan <- packet:
		return nil
	default:
		d.mu.Lock()
		d.stats.Errors++
		d.mu.Unlock()
		return errors.New("数据包队列已满")
	}
}

// Config 获取设备配置
func (d *MockDevice) Config() *Config {
	return d.config
}

// FD 获取文件描述符（Mock返回0）
func (d *MockDevice) FD() (uintptr, error) {
	return 0, errors.New("Mock设备不支持文件描述符")
}

// Stats 获取统计信息
func (d *MockDevice) Stats() *DeviceStats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// 返回副本
	stats := *d.stats
	return &stats
}

// IsRunning 检查设备是否运行中
func (d *MockDevice) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.running
}

// InjectPacket 注入数据包（测试用）
func (d *MockDevice) InjectPacket(packet *Packet) error {
	d.mu.RLock()
	running := d.running
	d.mu.RUnlock()

	if !running {
		return errors.New("设备未运行")
	}

	select {
	case d.packetChan <- packet:
		return nil
	default:
		return errors.New("数据包队列已满")
	}
}

// SetLatency 设置模拟延迟
func (d *MockDevice) SetLatency(latency time.Duration) {
	d.simulateLatency = latency
}

// ResetStats 重置统计信息
func (d *MockDevice) ResetStats() {
	d.mu.Lock()
	d.stats = &DeviceStats{}
	d.mu.Unlock()
}
