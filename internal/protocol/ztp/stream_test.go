package ztp

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// newTestSession 创建用于测试的会话和传输层
func newTestSession(t *testing.T) (*Session, *testTransport) {
	transport := newTestTransport()
	session, err := NewSession(transport, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if err := session.Start(); err != nil {
		t.Fatalf("启动会话失败: %v", err)
	}
	t.Cleanup(func() {
		session.Close()
		transport.Close()
	})
	return session, transport
}

// newMockSession 创建模拟会话用于测试流
func newMockSession(t *testing.T) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		config:           DefaultSessionConfig(),
		transport:        nil, // 不需要实际传输
		streams:          make(map[uint32]*Stream),
		streamsMu:        sync.RWMutex{},
		nextStream:       1, // 客户端发起使用奇数ID
		highPriorityCh:   make(chan *Frame, 10),
		normalPriorityCh: make(chan *Frame, 10),
		lowPriorityCh:    make(chan *Frame, 10),
		recvCh:           make(chan *Frame, 10),
		errors:           make(chan error, 10),
		closeCh:          make(chan struct{}),
		isClosed:         atomic.Bool{},
		isStarted:        atomic.Bool{},
		ctx:              ctx,
		cancel:           cancel,
		wg:               sync.WaitGroup{},
	}
}

func TestStream_ID(t *testing.T) {
	session := newMockSession(t)
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}
	if stream.ID() == 0 {
		t.Errorf("流ID不应为0")
	}
}

func TestStream_WriteBasic(t *testing.T) {
	session := newMockSession(t)
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}
	// 消耗STREAM_OPEN帧（如果有）
	select {
	case <-session.normalPriorityCh:
	default:
	}

	// 测试写入小数据
	testData := []byte("test data")
	n, err := stream.Write(testData)
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}

	if n != len(testData) {
		t.Errorf("写入字节数错误: 期望 %d, 得到 %d", len(testData), n)
	}

	// 检查是否发送了数据帧（普通优先级）
	select {
	case frame := <-session.normalPriorityCh:
		if frame.Type != TypeData {
			t.Errorf("发送的帧类型错误: 期望 %#x, 得到 %#x", TypeData, frame.Type)
		}
		if frame.StreamID != stream.ID() {
			t.Errorf("帧流ID错误: 期望 %d, 得到 %d", stream.ID(), frame.StreamID)
		}
		if string(frame.Payload) != "test data" {
			t.Errorf("帧载荷错误: 期望 'test data', 得到 %q", string(frame.Payload))
		}
	default:
		t.Error("没有帧被发送到通道")
	}
}
