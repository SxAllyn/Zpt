package loopback

import (
	"context"
	"testing"
	"time"
)

func TestPairNew(t *testing.T) {
	p := NewPair(1024)
	if p == nil {
		t.Fatal("NewPair返回nil")
	}
	if p.IsClosed() {
		t.Error("新建的Pair不应是关闭状态")
	}
}

func TestPairDialAccept(t *testing.T) {
	p := NewPair(1024)
	defer p.Close()

	// 测试Dial和Accept
	clientConn, err := p.Dial()
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer clientConn.Close()

	serverConn, err := p.Accept()
	if err != nil {
		t.Fatalf("Accept失败: %v", err)
	}
	defer serverConn.Close()

	// 测试数据传输
	testData := []byte("hello loopback")
	go func() {
		clientConn.Write(testData)
		clientConn.Close()
	}()

	buf := make([]byte, len(testData))
	n, err := serverConn.Read(buf)
	if err != nil {
		t.Fatalf("读取数据失败: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("读取数据长度不符: 期望 %d, 实际 %d", len(testData), n)
	}
	if string(buf) != string(testData) {
		t.Fatalf("数据内容不符: 期望 %s, 实际 %s", testData, buf)
	}
}

func TestPairClose(t *testing.T) {
	p := NewPair(1024)

	// 关闭后Dial应失败
	p.Close()
	if !p.IsClosed() {
		t.Error("Close后IsClosed应返回true")
	}

	_, err := p.Dial()
	if err == nil {
		t.Error("关闭后Dial应返回错误")
	}

	_, err = p.Accept()
	if err == nil {
		t.Error("关闭后Accept应返回错误")
	}

	// 多次关闭应安全
	err = p.Close()
	if err != nil {
		t.Errorf("重复关闭应安全: %v", err)
	}
}

func TestPairConcurrent(t *testing.T) {
	p := NewPair(1024)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 并发Dial和Accept
	errCh := make(chan error, 2)
	go func() {
		conn, err := p.Dial()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		conn.Write([]byte("ping"))
		errCh <- nil
	}()

	go func() {
		conn, err := p.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	// 等待两个goroutine完成
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("并发操作失败: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("并发操作超时")
		}
	}
}

func TestPairBufferSize(t *testing.T) {
	// 测试小缓冲区
	p := NewPair(1)
	defer p.Close()

	// 先Dial一个连接
	conn1, err := p.Dial()
	if err != nil {
		t.Fatalf("第一次Dial失败: %v", err)
	}
	defer conn1.Close()

	// 第二个Dial应该在超时后失败（因为缓冲区大小为1，但第一个连接尚未被Accept）
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		_, err := p.Dial()
		t.Logf("第二次Dial结果: %v", err)
	}()

	// 接受第一个连接
	_, err = p.Accept()
	if err != nil {
		t.Fatalf("Accept失败: %v", err)
	}

	<-ctx.Done()
}
