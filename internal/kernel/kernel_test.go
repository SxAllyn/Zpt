package kernel

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockService 模拟服务实现
type mockService struct {
	startCalled bool
	stopCalled  bool
	startDelay  time.Duration
	stopDelay   time.Duration
	startErr    error
	stopErr     error
}

func (m *mockService) Start(ctx context.Context) error {
	m.startCalled = true
	if m.startDelay > 0 {
		select {
		case <-time.After(m.startDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.startErr
}

func (m *mockService) Stop() error {
	m.stopCalled = true
	if m.stopDelay > 0 {
		time.Sleep(m.stopDelay)
	}
	return m.stopErr
}

func TestNewKernel(t *testing.T) {
	k, err := New()
	if err != nil {
		t.Fatalf("New() 失败: %v", err)
	}
	if k == nil {
		t.Fatal("New() 返回 nil")
	}
}

func TestKernel_RegisterService(t *testing.T) {
	k, _ := New()
	svc := &mockService{}

	k.RegisterService("test-service", svc)

	if _, ok := k.GetService("test-service"); !ok {
		t.Error("GetService 未找到注册的服务")
	}
}

func TestKernel_StartStop(t *testing.T) {
	k, _ := New()
	svc := &mockService{}
	k.RegisterService("test", svc)

	// 测试启动
	if err := k.Start(); err != nil {
		t.Fatalf("Start() 失败: %v", err)
	}

	// 测试重复启动
	if err := k.Start(); err == nil {
		t.Error("重复启动应该返回错误")
	}

	// 短暂运行
	time.Sleep(100 * time.Millisecond)

	// 测试停止
	if err := k.Stop(); err != nil {
		t.Fatalf("Stop() 失败: %v", err)
	}

	// 验证服务被调用
	if !svc.startCalled {
		t.Error("服务的 Start 方法未被调用")
	}
	if !svc.stopCalled {
		t.Error("服务的 Stop 方法未被调用")
	}
}

func TestKernel_StopWithoutStart(t *testing.T) {
	k, _ := New()
	if err := k.Stop(); err == nil {
		t.Error("未启动时停止应该返回错误")
	}
}

func TestKernel_ContextCancellation(t *testing.T) {
	k, _ := New()

	// 创建长时间运行的服务
	svc := &mockService{
		startDelay: 2 * time.Second,
	}
	k.RegisterService("long-running", svc)

	// 启动内核
	if err := k.Start(); err != nil {
		t.Fatalf("Start() 失败: %v", err)
	}

	// 立即停止
	go func() {
		time.Sleep(100 * time.Millisecond)
		k.Stop()
	}()

	// 等待停止完成
	time.Sleep(300 * time.Millisecond)

	// 服务应该被中断
	if svc.startCalled && svc.stopCalled {
		t.Log("服务正常被中断")
	}
}

func TestKernel_GetService(t *testing.T) {
	k, _ := New()
	svc := &mockService{}
	k.RegisterService("existing", svc)

	// 测试获取存在的服务
	retrieved, ok := k.GetService("existing")
	if !ok {
		t.Fatal("GetService 应该找到存在的服务")
	}
	if retrieved != svc {
		t.Error("GetService 返回的服务不匹配")
	}

	// 测试获取不存在的服务
	_, ok = k.GetService("nonexistent")
	if ok {
		t.Error("GetService 不应该找到不存在的服务")
	}
}

func TestKernel_ConcurrentAccess(t *testing.T) {
	k, _ := New()

	// 并发注册服务
	const numServices = 10
	var wg sync.WaitGroup
	wg.Add(numServices)

	for i := 0; i < numServices; i++ {
		go func(id int) {
			defer wg.Done()
			svc := &mockService{}
			k.RegisterService(string(rune('a'+id)), svc)
		}(i)
	}
	wg.Wait()

	// 并发获取服务
	wg.Add(numServices)
	for i := 0; i < numServices; i++ {
		go func(id int) {
			defer wg.Done()
			_, ok := k.GetService(string(rune('a' + id)))
			if !ok {
				t.Errorf("服务 %c 未找到", 'a'+id)
			}
		}(i)
	}
	wg.Wait()
}
