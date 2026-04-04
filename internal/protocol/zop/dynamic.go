// Package zop 实现 Zpt 混淆协议
package zop

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// dynamicTransport 动态切换传输包装器
type dynamicTransport struct {
	config             *Config
	stats              TransportStats
	createdAt          time.Time
	closed             bool
	mu                 sync.RWMutex
	current            Transport          // 当前激活的传输
	transports         map[Mode]Transport // 所有可用传输实例
	switchTimer        *time.Timer        // 切换定时器
	lastSwitch         time.Time          // 上次切换时间
	trafficSinceSwitch uint64             // 切换后累计流量
}

// NewDynamicTransport 创建动态切换传输
func NewDynamicTransport(config *Config, conn io.ReadWriteCloser) (Transport, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if !config.EnableDynamicSwitch {
		// 动态切换未启用，返回普通传输
		return NewTransport(config, conn)
	}

	dt := &dynamicTransport{
		config:     config,
		createdAt:  time.Now(),
		stats:      TransportStats{},
		transports: make(map[Mode]Transport),
		lastSwitch: time.Now(),
	}

	// 初始化所有启用的传输形态
	for _, mode := range config.EnabledModes {
		transport, err := NewTransportWithMode(config, mode, conn)
		if err != nil {
			return nil, fmt.Errorf("创建形态 %v 传输失败: %w", mode, err)
		}
		dt.transports[mode] = transport
	}

	// 设置当前传输为默认形态
	defaultTransport, ok := dt.transports[config.DefaultMode]
	if !ok {
		// 默认形态不可用，使用第一个可用形态
		for mode, transport := range dt.transports {
			defaultTransport = transport
			dt.config.DefaultMode = mode
			break
		}
	}
	dt.current = defaultTransport

	// 启动切换检查
	dt.startSwitchChecker()

	return dt, nil
}

// Read 读取数据（委托给当前传输）
func (dt *dynamicTransport) Read(p []byte) (n int, err error) {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	if dt.closed {
		return 0, io.EOF
	}

	n, err = dt.current.Read(p)
	if err == nil {
		dt.stats.BytesReceived += uint64(n)
		dt.trafficSinceSwitch += uint64(n)
	}
	return n, err
}

// Write 写入数据（委托给当前传输）
func (dt *dynamicTransport) Write(p []byte) (n int, err error) {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	if dt.closed {
		return 0, io.ErrClosedPipe
	}

	n, err = dt.current.Write(p)
	if err == nil {
		dt.stats.BytesSent += uint64(n)
		dt.trafficSinceSwitch += uint64(n)
	}
	return n, err
}

// Close 关闭传输
func (dt *dynamicTransport) Close() error {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.closed {
		return nil
	}
	dt.closed = true

	// 停止切换定时器
	if dt.switchTimer != nil {
		dt.switchTimer.Stop()
	}

	// 关闭所有传输实例
	var firstErr error
	for _, transport := range dt.transports {
		if err := transport.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// Mode 返回当前伪装形态
func (dt *dynamicTransport) Mode() Mode {
	dt.mu.RLock()
	defer dt.mu.RUnlock()
	return dt.current.Mode()
}

// Switch 切换到指定形态
func (dt *dynamicTransport) Switch(ctx context.Context, newMode Mode) error {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.closed {
		return io.ErrClosedPipe
	}

	// 检查目标形态是否可用
	target, ok := dt.transports[newMode]
	if !ok {
		return fmt.Errorf("目标形态 %v 不可用", newMode)
	}

	// 如果已经是目标形态，直接返回
	if dt.current.Mode() == newMode {
		return nil
	}

	// 执行切换
	dt.current = target
	dt.stats.SwitchCount++
	dt.lastSwitch = time.Now()
	dt.trafficSinceSwitch = 0

	return nil
}

// GetStats 获取统计信息
func (dt *dynamicTransport) GetStats() TransportStats {
	dt.mu.RLock()
	defer dt.mu.RUnlock()

	stats := dt.stats
	stats.CurrentModeDuration = time.Since(dt.lastSwitch)
	return stats
}

// startSwitchChecker 启动切换检查器
func (dt *dynamicTransport) startSwitchChecker() {
	// 检查间隔
	checkInterval := 30 * time.Second

	dt.switchTimer = time.AfterFunc(checkInterval, func() {
		dt.checkAndSwitch()
		// 重新安排下一次检查
		if !dt.closed {
			dt.startSwitchChecker()
		}
	})
}

// checkAndSwitch 检查并执行切换
func (dt *dynamicTransport) checkAndSwitch() {
	dt.mu.RLock()
	currentMode := dt.current.Mode()
	currentProfile := dt.getCurrentProfile()
	traffic := dt.trafficSinceSwitch
	dt.mu.RUnlock()

	if currentProfile == nil {
		return
	}

	shouldSwitch := false
	var targetMode Mode

	switch currentProfile.SwitchPolicy {
	case SwitchPolicyTime:
		// 时间触发切换
		if time.Now().After(currentProfile.NextSwitchTime) {
			shouldSwitch = true
			targetMode = dt.selectNextMode(currentMode)
		}

	case SwitchPolicyTraffic:
		// 流量触发切换
		if traffic >= currentProfile.SwitchParams.TrafficThreshold {
			shouldSwitch = true
			targetMode = dt.selectNextMode(currentMode)
		}

	case SwitchPolicyRandom:
		// 随机切换
		if dt.shouldSwitchRandom(currentProfile.SwitchParams.RandomProbability) {
			shouldSwitch = true
			targetMode = dt.selectNextMode(currentMode)
		}

	case SwitchPolicyAdaptive:
		// 自适应切换（Mock实现）
		if dt.shouldSwitchAdaptive(currentProfile.SwitchParams.AdaptiveParams) {
			shouldSwitch = true
			targetMode = dt.selectAdaptiveMode()
		}

	default:
		// 无切换策略
		return
	}

	if shouldSwitch {
		ctx := context.Background()
		dt.Switch(ctx, targetMode)
	}
}

// getCurrentProfile 获取当前混淆配置
func (dt *dynamicTransport) getCurrentProfile() *ObfuscationProfile {
	currentMode := dt.current.Mode()
	for _, profile := range dt.config.Profiles {
		if profile.CurrentMode == currentMode {
			return &profile
		}
	}
	return nil
}

// selectNextMode 选择下一个形态
func (dt *dynamicTransport) selectNextMode(currentMode Mode) Mode {
	// 简单实现：轮询下一个启用的形态
	enabled := dt.config.EnabledModes
	if len(enabled) == 0 {
		return currentMode
	}

	// 查找当前形态在列表中的位置
	currentIdx := -1
	for i, mode := range enabled {
		if mode == currentMode {
			currentIdx = i
			break
		}
	}

	// 选择下一个形态
	nextIdx := 0
	if currentIdx >= 0 && currentIdx < len(enabled)-1 {
		nextIdx = currentIdx + 1
	} else {
		nextIdx = 0
	}

	return enabled[nextIdx]
}

// shouldSwitchRandom 随机切换决策
func (dt *dynamicTransport) shouldSwitchRandom(probability float64) bool {
	if probability <= 0 {
		return false
	}
	// 简化实现：使用时间戳作为随机源
	now := time.Now().UnixNano()
	randValue := float64(now%10000) / 10000.0
	return randValue < probability
}

// shouldSwitchAdaptive 自适应切换决策（Mock实现）
func (dt *dynamicTransport) shouldSwitchAdaptive(params AdaptiveParams) bool {
	// Mock实现：假设网络状况良好，不切换
	// 实际实现需要监测延迟、丢包率等指标
	return false
}

// selectAdaptiveMode 选择自适应形态（Mock实现）
func (dt *dynamicTransport) selectAdaptiveMode() Mode {
	// 简化实现：返回第一个可用形态
	if len(dt.config.EnabledModes) > 0 {
		return dt.config.EnabledModes[0]
	}
	return dt.current.Mode()
}

// dynamicObfuscator 动态切换混淆器
type dynamicObfuscator struct {
	config  *Config
	mu      sync.RWMutex
	current Obfuscator
}

// NewDynamicObfuscator 创建动态切换混淆器
func NewDynamicObfuscator(config *Config) (Obfuscator, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if !config.EnableDynamicSwitch {
		// 动态切换未启用，返回普通混淆器
		return NewObfuscator(config)
	}

	do := &dynamicObfuscator{
		config: config,
	}

	// 初始化当前混淆器
	obfuscator, err := NewObfuscatorWithMode(config, config.DefaultMode)
	if err != nil {
		return nil, err
	}
	do.current = obfuscator

	return do, nil
}

// Obfuscate 混淆数据（委托给当前混淆器）
func (do *dynamicObfuscator) Obfuscate(ctx context.Context, data []byte) ([]byte, error) {
	do.mu.RLock()
	defer do.mu.RUnlock()
	return do.current.Obfuscate(ctx, data)
}

// Deobfuscate 解混淆数据（委托给当前混淆器）
func (do *dynamicObfuscator) Deobfuscate(ctx context.Context, data []byte) ([]byte, error) {
	do.mu.RLock()
	defer do.mu.RUnlock()
	return do.current.Deobfuscate(ctx, data)
}

// GetMode 获取当前伪装形态
func (do *dynamicObfuscator) GetMode() Mode {
	do.mu.RLock()
	defer do.mu.RUnlock()
	return do.current.GetMode()
}

// SwitchMode 切换到指定形态
func (do *dynamicObfuscator) SwitchMode(newMode Mode) error {
	do.mu.Lock()
	defer do.mu.Unlock()

	// 检查新形态是否启用
	enabled := false
	for _, m := range do.config.EnabledModes {
		if m == newMode {
			enabled = true
			break
		}
	}
	if !enabled {
		return fmt.Errorf("形态 %v 未启用", newMode)
	}

	// 创建新混淆器
	obfuscator, err := NewObfuscatorWithMode(do.config, newMode)
	if err != nil {
		return err
	}

	do.current = obfuscator
	return nil
}
