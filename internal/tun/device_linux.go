//go:build linux
// +build linux

package tun

// NewDevice 创建Linux平台的TUN设备（使用water库实现）
func NewDevice(config *Config) (Device, error) {
	return NewWaterDevice(config)
}
