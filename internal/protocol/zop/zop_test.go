// Package zop 测试
package zop

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// mockConn 模拟连接用于测试
type mockConn struct {
	io.ReadWriteCloser
}

func newMockConn() io.ReadWriteCloser {
	// 使用 net.Pipe 创建双向内存连接
	client, server := net.Pipe()

	// 启动一个 goroutine 将服务器端数据回显（简化处理）
	go func() {
		defer server.Close()
		buf := make([]byte, 4096)
		for {
			n, err := server.Read(buf)
			if err != nil {
				return
			}
			// 简单回显
			server.Write(buf[:n])
		}
	}()

	return client
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config == nil {
		t.Fatal("DefaultConfig() 返回 nil")
	}
	if config.DefaultMode != ModeHTTP3 {
		t.Errorf("期望默认形态为 ModeHTTP3，得到 %v", config.DefaultMode)
	}
	if len(config.EnabledModes) != 3 {
		t.Errorf("期望启用3种形态，得到 %v", len(config.EnabledModes))
	}
	if !config.EnableDynamicSwitch {
		t.Error("期望启用动态切换")
	}
}

func TestNewTransport(t *testing.T) {
	config := DefaultConfig()
	transport, err := NewTransport(config, newMockConn())
	if err != nil {
		t.Fatalf("NewTransport 失败: %v", err)
	}
	defer transport.Close()

	if transport.Mode() != ModeHTTP3 {
		t.Errorf("期望形态为 ModeHTTP3，得到 %v", transport.Mode())
	}
}

func TestNewTransportWithMode(t *testing.T) {
	config := DefaultConfig()

	// 测试 HTTP/3 形态
	transport, err := NewTransportWithMode(config, ModeHTTP3, newMockConn())
	if err != nil {
		t.Fatalf("NewTransportWithMode(HTTP3) 失败: %v", err)
	}
	defer transport.Close()
	if transport.Mode() != ModeHTTP3 {
		t.Errorf("期望形态为 ModeHTTP3，得到 %v", transport.Mode())
	}

	// 测试 WebRTC 形态
	transport, err = NewTransportWithMode(config, ModeWebRTC, newMockConn())
	if err != nil {
		t.Fatalf("NewTransportWithMode(WebRTC) 失败: %v", err)
	}
	defer transport.Close()
	if transport.Mode() != ModeWebRTC {
		t.Errorf("期望形态为 ModeWebRTC，得到 %v", transport.Mode())
	}

	// 测试 DoQ 形态
	transport, err = NewTransportWithMode(config, ModeDoQ, newMockConn())
	if err != nil {
		t.Fatalf("NewTransportWithMode(DoQ) 失败: %v", err)
	}
	defer transport.Close()
	if transport.Mode() != ModeDoQ {
		t.Errorf("期望形态为 ModeDoQ，得到 %v", transport.Mode())
	}
}

func TestNewObfuscator(t *testing.T) {
	config := DefaultConfig()
	obfuscator, err := NewObfuscator(config)
	if err != nil {
		t.Fatalf("NewObfuscator 失败: %v", err)
	}

	if obfuscator.GetMode() != ModeHTTP3 {
		t.Errorf("期望形态为 ModeHTTP3，得到 %v", obfuscator.GetMode())
	}
}

func TestNewObfuscatorWithMode(t *testing.T) {
	config := DefaultConfig()

	// 测试 HTTP/3 混淆器
	obfuscator, err := NewObfuscatorWithMode(config, ModeHTTP3)
	if err != nil {
		t.Fatalf("NewObfuscatorWithMode(HTTP3) 失败: %v", err)
	}
	if obfuscator.GetMode() != ModeHTTP3 {
		t.Errorf("期望形态为 ModeHTTP3，得到 %v", obfuscator.GetMode())
	}

	// 测试 WebRTC 混淆器
	obfuscator, err = NewObfuscatorWithMode(config, ModeWebRTC)
	if err != nil {
		t.Fatalf("NewObfuscatorWithMode(WebRTC) 失败: %v", err)
	}
	if obfuscator.GetMode() != ModeWebRTC {
		t.Errorf("期望形态为 ModeWebRTC，得到 %v", obfuscator.GetMode())
	}

	// 测试 DoQ 混淆器
	obfuscator, err = NewObfuscatorWithMode(config, ModeDoQ)
	if err != nil {
		t.Fatalf("NewObfuscatorWithMode(DoQ) 失败: %v", err)
	}
	if obfuscator.GetMode() != ModeDoQ {
		t.Errorf("期望形态为 ModeDoQ，得到 %v", obfuscator.GetMode())
	}
}

func TestObfuscateDeobfuscate(t *testing.T) {
	config := DefaultConfig()
	obfuscator, err := NewObfuscator(config)
	if err != nil {
		t.Fatalf("NewObfuscator 失败: %v", err)
	}

	original := []byte("test data")
	ctx := context.Background()

	// 测试混淆
	obfuscated, err := obfuscator.Obfuscate(ctx, original)
	if err != nil {
		t.Fatalf("Obfuscate 失败: %v", err)
	}
	if len(obfuscated) == 0 {
		t.Error("混淆后数据为空")
	}

	// 测试解混淆
	deobfuscated, err := obfuscator.Deobfuscate(ctx, obfuscated)
	if err != nil {
		t.Fatalf("Deobfuscate 失败: %v", err)
	}
	if string(deobfuscated) != string(original) {
		t.Errorf("解混淆后数据不匹配: 期望 %s, 得到 %s", original, deobfuscated)
	}
}

func TestTransportSwitch(t *testing.T) {
	config := DefaultConfig()
	config.EnabledModes = []Mode{ModeHTTP3, ModeWebRTC} // 只启用两种形态用于测试

	transport, err := NewTransport(config, newMockConn())
	if err != nil {
		t.Fatalf("NewTransport 失败: %v", err)
	}
	defer transport.Close()

	ctx := context.Background()

	// 切换到 WebRTC
	err = transport.Switch(ctx, ModeWebRTC)
	if err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	if transport.Mode() != ModeWebRTC {
		t.Errorf("切换后形态应为 ModeWebRTC，得到 %v", transport.Mode())
	}

	// 切换回 HTTP/3
	err = transport.Switch(ctx, ModeHTTP3)
	if err != nil {
		t.Fatalf("Switch 失败: %v", err)
	}
	if transport.Mode() != ModeHTTP3 {
		t.Errorf("切换后形态应为 ModeHTTP3，得到 %v", transport.Mode())
	}
}

func TestDynamicTransport(t *testing.T) {
	config := DefaultConfig()
	config.EnableDynamicSwitch = true

	transport, err := NewDynamicTransport(config, newMockConn())
	if err != nil {
		t.Fatalf("NewDynamicTransport 失败: %v", err)
	}
	defer transport.Close()

	// 验证初始形态
	mode := transport.Mode()
	if mode != ModeHTTP3 {
		t.Errorf("期望初始形态为 ModeHTTP3，得到 %v", mode)
	}

	// 获取统计信息
	stats := transport.GetStats()
	if stats.SwitchCount != 0 {
		t.Errorf("期望初始切换次数为0，得到 %v", stats.SwitchCount)
	}
	if stats.CurrentModeDuration < 0 {
		t.Error("当前形态持续时间应为正数")
	}
}

func TestDynamicObfuscator(t *testing.T) {
	config := DefaultConfig()
	config.EnableDynamicSwitch = true

	obfuscator, err := NewDynamicObfuscator(config)
	if err != nil {
		t.Fatalf("NewDynamicObfuscator 失败: %v", err)
	}

	// 验证初始形态
	mode := obfuscator.GetMode()
	if mode != ModeHTTP3 {
		t.Errorf("期望初始形态为 ModeHTTP3，得到 %v", mode)
	}

	// 测试切换到 WebRTC
	err = obfuscator.SwitchMode(ModeWebRTC)
	if err != nil {
		t.Fatalf("SwitchMode 失败: %v", err)
	}
	if obfuscator.GetMode() != ModeWebRTC {
		t.Errorf("切换后形态应为 ModeWebRTC，得到 %v", obfuscator.GetMode())
	}

	// 切换回 HTTP/3 进行混淆测试（WebRTC 和 DoQ 的混淆器 Mock 实现暂不测试）
	err = obfuscator.SwitchMode(ModeHTTP3)
	if err != nil {
		t.Fatalf("SwitchMode 回 HTTP3 失败: %v", err)
	}
	if obfuscator.GetMode() != ModeHTTP3 {
		t.Errorf("切换后形态应为 ModeHTTP3，得到 %v", obfuscator.GetMode())
	}

	// 测试混淆/解混淆功能（使用 HTTP/3 混淆器）
	ctx := context.Background()
	data := []byte("test data")
	obfuscated, err := obfuscator.Obfuscate(ctx, data)
	if err != nil {
		t.Fatalf("Obfuscate 失败: %v", err)
	}
	deobfuscated, err := obfuscator.Deobfuscate(ctx, obfuscated)
	if err != nil {
		t.Fatalf("Deobfuscate 失败: %v", err)
	}
	if string(deobfuscated) != string(data) {
		t.Errorf("解混淆后数据不匹配")
	}
}

func TestGenerateRandomID(t *testing.T) {
	id1 := generateRandomID(8)
	id2 := generateRandomID(8)

	if len(id1) != 8 {
		t.Errorf("期望ID长度为8，得到 %v", len(id1))
	}
	if len(id2) != 8 {
		t.Errorf("期望ID长度为8，得到 %v", len(id2))
	}

	// 两次生成的ID应不同（大概率）
	if id1 == id2 {
		t.Error("两次生成的随机ID相同")
	}
}

func TestTransportStats(t *testing.T) {
	stats := TransportStats{
		BytesSent:           100,
		BytesReceived:       200,
		CurrentModeDuration: time.Second,
		SwitchCount:         5,
	}

	if stats.BytesSent != 100 {
		t.Errorf("期望 BytesSent=100，得到 %v", stats.BytesSent)
	}
	if stats.BytesReceived != 200 {
		t.Errorf("期望 BytesReceived=200，得到 %v", stats.BytesReceived)
	}
	if stats.CurrentModeDuration != time.Second {
		t.Errorf("期望 CurrentModeDuration=1s，得到 %v", stats.CurrentModeDuration)
	}
	if stats.SwitchCount != 5 {
		t.Errorf("期望 SwitchCount=5，得到 %v", stats.SwitchCount)
	}
}

func TestWebRTCObfuscator(t *testing.T) {
	config := DefaultConfig()
	config.EnabledModes = []Mode{ModeWebRTC} // 只启用WebRTC

	obfuscator, err := NewObfuscatorWithMode(config, ModeWebRTC)
	if err != nil {
		t.Fatalf("NewObfuscatorWithMode(WebRTC) 失败: %v", err)
	}

	if obfuscator.GetMode() != ModeWebRTC {
		t.Errorf("期望形态为 ModeWebRTC，得到 %v", obfuscator.GetMode())
	}

	// 测试混淆/解混淆功能
	ctx := context.Background()
	data := []byte("test data for WebRTC")

	obfuscated, err := obfuscator.Obfuscate(ctx, data)
	if err != nil {
		t.Fatalf("Obfuscate 失败: %v", err)
	}

	deobfuscated, err := obfuscator.Deobfuscate(ctx, obfuscated)
	if err != nil {
		t.Fatalf("Deobfuscate 失败: %v", err)
	}

	if string(deobfuscated) != string(data) {
		t.Errorf("解混淆后数据不匹配: 期望 %s, 得到 %s", data, deobfuscated)
	}
}

func TestDoQObfuscator(t *testing.T) {
	config := DefaultConfig()
	config.EnabledModes = []Mode{ModeDoQ} // 只启用DoQ

	obfuscator, err := NewObfuscatorWithMode(config, ModeDoQ)
	if err != nil {
		t.Fatalf("NewObfuscatorWithMode(DoQ) 失败: %v", err)
	}

	if obfuscator.GetMode() != ModeDoQ {
		t.Errorf("期望形态为 ModeDoQ，得到 %v", obfuscator.GetMode())
	}

	// 测试混淆/解混淆功能
	ctx := context.Background()
	data := []byte("test data for DoQ")

	obfuscated, err := obfuscator.Obfuscate(ctx, data)
	if err != nil {
		t.Fatalf("Obfuscate 失败: %v", err)
	}

	deobfuscated, err := obfuscator.Deobfuscate(ctx, obfuscated)
	if err != nil {
		t.Fatalf("Deobfuscate 失败: %v", err)
	}

	if string(deobfuscated) != string(data) {
		t.Errorf("解混淆后数据不匹配: 期望 %s, 得到 %s", data, deobfuscated)
	}
}

// TestTransportDataFlow 测试传输层完整数据流
func TestTransportDataFlow(t *testing.T) {
	config := DefaultConfig()
	config.EnabledModes = []Mode{ModeHTTP3} // 只测试HTTP/3形态

	// 创建内存管道模拟双向连接
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// 客户端传输
	clientTransport, err := NewTransportWithMode(config, ModeHTTP3, clientConn)
	if err != nil {
		t.Fatalf("创建客户端传输失败: %v", err)
	}
	defer clientTransport.Close()

	// 服务器传输
	serverTransport, err := NewTransportWithMode(config, ModeHTTP3, serverConn)
	if err != nil {
		t.Fatalf("创建服务器传输失败: %v", err)
	}
	defer serverTransport.Close()

	// 测试数据
	testData := []byte("Hello, Zop! This is a test message for data flow verification.")

	// 客户端写入数据
	go func() {
		n, err := clientTransport.Write(testData)
		if err != nil {
			t.Errorf("客户端写入失败: %v", err)
		}
		if n != len(testData) {
			t.Errorf("客户端写入字节数不匹配: 期望 %d, 得到 %d", len(testData), n)
		}
		clientTransport.Close() // 写入完成后关闭
	}()

	// 服务器读取数据
	buf := make([]byte, 1024)
	totalRead := 0
	for totalRead < len(testData) {
		n, err := serverTransport.Read(buf[totalRead:])
		if err != nil && err != io.EOF {
			t.Fatalf("服务器读取失败: %v", err)
		}
		if n == 0 && err == io.EOF {
			break
		}
		totalRead += n
	}

	// 验证数据
	if totalRead != len(testData) {
		t.Errorf("读取总字节数不匹配: 期望 %d, 得到 %d", len(testData), totalRead)
	}
	if !bytes.Equal(buf[:totalRead], testData) {
		t.Errorf("数据内容不匹配")
	}
}
