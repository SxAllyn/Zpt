//go:build !linux && !windows
// +build !linux,!windows

package tun

// NewDevice 创建默认平台的TUN设备（使用Mock实现）
func NewDevice(config *Config) (Device, error) {
	// 非Linux和Windows平台使用Mock实现
	return NewMockDevice(config)
}
