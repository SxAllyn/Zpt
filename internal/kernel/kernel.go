// Package kernel 提供核心生命周期管理和依赖注入
package kernel

import (
	"context"
	"fmt"
	"sync"
)

// LifecycleManager 生命周期管理器接口
type LifecycleManager interface {
	// Start 启动所有服务
	Start() error
	// Stop 停止所有服务
	Stop() error
	// RegisterService 注册服务
	RegisterService(name string, service interface{})
	// GetService 获取服务
	GetService(name string) (interface{}, bool)
}

// Kernel 内核实现
type Kernel struct {
	mu       sync.RWMutex
	services map[string]interface{}
	started  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// New 创建新内核实例
func New() (*Kernel, error) {
	return &Kernel{
		services: make(map[string]interface{}),
		started:  false,
	}, nil
}

// Start 启动内核
func (k *Kernel) Start() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.started {
		return fmt.Errorf("内核已经启动")
	}

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel

	// 启动所有可启动的服务
	for name, svc := range k.services {
		if starter, ok := svc.(Starter); ok {
			k.wg.Add(1)
			go func(name string, s Starter) {
				defer k.wg.Done()
				if err := s.Start(ctx); err != nil {
					fmt.Printf("服务 %s 启动失败: %v\n", name, err)
				}
			}(name, starter)
		}
	}

	k.started = true
	fmt.Println("Zpt-core 内核启动完成")
	return nil
}

// Stop 停止内核
func (k *Kernel) Stop() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if !k.started {
		return fmt.Errorf("内核未启动")
	}

	// 取消上下文，通知所有服务停止
	if k.cancel != nil {
		k.cancel()
	}

	// 等待所有服务停止
	k.wg.Wait()

	// 调用所有服务的 Stop 方法
	for name, svc := range k.services {
		if stopper, ok := svc.(Stopper); ok {
			if err := stopper.Stop(); err != nil {
				fmt.Printf("服务 %s 停止失败: %v\n", name, err)
			}
		}
	}

	k.started = false
	fmt.Println("Zpt-core 内核已停止")
	return nil
}

// RegisterService 注册服务
func (k *Kernel) RegisterService(name string, service interface{}) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.started {
		panic("不能在启动后注册服务")
	}

	k.services[name] = service
	fmt.Printf("注册服务: %s\n", name)
}

// GetService 获取服务
func (k *Kernel) GetService(name string) (interface{}, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()

	svc, ok := k.services[name]
	return svc, ok
}

// Starter 可启动的服务接口
type Starter interface {
	Start(ctx context.Context) error
}

// Stopper 可停止的服务接口
type Stopper interface {
	Stop() error
}

// StarterStopper 同时支持启动和停止的服务接口
type StarterStopper interface {
	Starter
	Stopper
}
