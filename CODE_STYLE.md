# 代码风格规范

本规范定义 Zpt-core 项目的 Go 代码编写标准，确保代码一致性和可维护性。

## 1. 格式规范

### 1.1 文件组织
- 每个 Go 文件对应一个逻辑单元
- 文件大小不超过 1000 行
- 导入分组顺序：
  1. 标准库
  2. 第三方库
  3. 项目内部包
- 每组之间用空行分隔

### 1.2 缩进与空格
- 使用 Tab 缩进（Go 默认）
- 运算符两侧加空格：`a + b`
- 逗号后加空格：`func(a, b, c)`
- 大括号不换行：
  ```go
  func example() {
      // 正确
  }
  ```

### 1.3 行宽
- 最大行宽：120 字符
- 超过时合理换行，保持可读性

## 2. 命名规范

### 2.1 包名
- 小写字母，无下划线
- 简短明确，单数形式
- 避免与标准库冲突
- 示例：`ztp`, `zap`, `router`

### 2.2 接口名
- 使用名词或动词+名词
- `er` 结尾表示行为：`Reader`, `Writer`
- 单个方法接口以方法名+`er`命名
- 示例：
  ```go
  type Handler interface {
      Handle(ctx context.Context) error
  }
  ```

### 2.3 结构体名
- 驼峰式，首字母大写
- 名词或名词短语
- 避免冗余后缀（如 `ZtpStruct`）
- 示例：`Session`, `StreamConfig`

### 2.4 方法名
- 驼峰式，首字母大写（公开）或小写（私有）
- 动词或动词短语
- Getter 方法省略 `Get` 前缀
- 布尔方法以 `Is`, `Has`, `Can` 等开头
- 示例：
  ```go
  func (s *Session) Close() error
  func (c *Config) IsValid() bool
  ```

### 2.5 变量名
- 短小精悍，作用域越大名称应越完整
- 循环变量：`i`, `j`, `k`
- 临时变量：简短描述用途
- 避免单字母变量（除循环变量）
- 示例：
  ```go
  var clientConn net.Conn  // 好
  var c net.Conn          // 不好（作用域大时）
  ```

### 2.6 常量名
- 全大写，下划线分隔
- 分组相关常量
- 示例：
  ```go
  const (
      MaxStreamCount = 65535
      DefaultTimeout = 30 * time.Second
  )
  ```

## 3. 代码组织

### 3.1 函数设计
- 单一职责，功能明确
- 参数不超过 5 个
- 返回错误作为最后一个返回值
- 函数长度不超过 50 行
- 示例：
  ```go
  func ProcessRequest(req *Request, timeout time.Duration) (*Response, error) {
      // 实现
  }
  ```

### 3.2 错误处理
- 尽早返回错误
- 错误信息清晰可读
- 使用错误链提供上下文
- 示例：
  ```go
  func loadConfig(path string) (*Config, error) {
      data, err := os.ReadFile(path)
      if err != nil {
          return nil, fmt.Errorf("读取配置文件失败: %w", err)
      }
      // ...
  }
  ```

### 3.3 注释规范
- 导出元素必须有文档注释
- 注释以元素名开头
- 使用完整的句子
- 示例：
  ```go
  // Session 表示一个 Ztp 会话，管理多个流。
  type Session struct {
      // ID 是会话的唯一标识符。
      ID uint32
      // 未导出字段不需要注释
      mu sync.Mutex
  }
  ```

### 3.4 测试代码
- 测试文件与被测文件同目录
- 测试函数名：`Test[类型]_[场景]`
- 表格驱动测试
- 示例：
  ```go
  func TestSession_OpenStream(t *testing.T) {
      tests := []struct {
          name    string
          setup   func() *Session
          wantErr bool
      }{
          {
              name: "正常打开流",
              setup: func() *Session {
                  return NewSession()
              },
              wantErr: false,
          },
      }
      
      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) {
              s := tt.setup()
              // 测试逻辑
          })
      }
  }
  ```

## 4. 并发规范

### 4.1 锁使用
- 明确锁的保护范围
- 使用 `defer` 释放锁
- 避免在持有锁时调用外部函数
- 示例：
  ```go
  type SafeCounter struct {
      mu    sync.RWMutex
      count int
  }
  
  func (c *SafeCounter) Increment() {
      c.mu.Lock()
      defer c.mu.Unlock()
      c.count++
  }
  ```

### 4.2 Channel 使用
- 明确 Channel 的所有权
- 由创建者关闭 Channel
- 避免 nil Channel 操作
- 示例：
  ```go
  func processRequests(requests <-chan Request, results chan<- Result) {
      for req := range requests {
          res := process(req)
          results <- res
      }
  }
  ```

## 5. 性能规范

### 5.1 内存分配
- 避免不必要的内存分配
- 使用 `sync.Pool` 重用对象
- 预分配 slice 和 map 容量
- 示例：
  ```go
  // 预分配容量
  items := make([]Item, 0, 100)
  
  // 使用 sync.Pool
  var bufferPool = sync.Pool{
      New: func() interface{} {
          return bytes.NewBuffer(make([]byte, 0, 1024))
      },
  }
  ```

### 5.2 字符串处理
- 使用 `strings.Builder` 拼接字符串
- 避免频繁的类型转换
- 示例：
  ```go
  var builder strings.Builder
  builder.WriteString("prefix")
  builder.WriteString(data)
  result := builder.String()
  ```

## 6. 安全规范

### 6.1 输入验证
- 验证所有外部输入
- 设置合理的长度限制
- 使用白名单验证
- 示例：
  ```go
  func validateInput(input string) error {
      if len(input) > MaxInputLength {
          return errors.New("输入过长")
      }
      // 更多验证
  }
  ```

### 6.2 加密安全
- 使用标准加密库
- 正确的随机数生成
- 密钥管理安全
- 示例：
  ```go
  import "crypto/rand"
  
  func generateKey() ([]byte, error) {
      key := make([]byte, 32)
      if _, err := rand.Read(key); err != nil {
          return nil, err
      }
      return key, nil
  }
  ```

## 7. 工具支持

### 7.1 自动格式化
```bash
go fmt ./...           # 格式化代码
gofumpt -w .          # 更严格的格式化（可选）
```

### 7.2 静态检查
```bash
go vet ./...           # Go 官方检查
golangci-lint run     # 综合代码检查
```

### 7.3 代码生成
- 使用 `go generate` 管理生成的代码
- 生成的代码必须有 `// Code generated ... DO NOT EDIT.` 头
- 生成的代码提交到版本库

## 8. 异常情况

### 8.1 Panic 使用
- 仅用于不可恢复的错误
- 程序启动时的参数检查
- 避免在库代码中使用 panic
- 使用 `recover` 处理协程 panic

### 8.2 断言
- 测试中使用 `testing` 包的断言
- 生产代码避免使用 `panic` 作为断言
- 示例：
  ```go
  // 测试中的断言
  if got != want {
      t.Errorf("got %v, want %v", got, want)
  }
  ```

## 9. 版本兼容

### 9.1 API 稳定性
- 公开 API 变更需谨慎
- 遵循语义化版本
- 废弃 API 提供替代方案
- 示例：
  ```go
  // Deprecated: 使用 NewSessionV2 代替
  func NewSession() *Session {
      return NewSessionV2(DefaultConfig())
  }
  ```

### 9.2 向后兼容
- 添加新功能不影响现有 API
- 配置格式变更提供迁移工具
- 协议版本协商机制

---

本规范随项目发展持续更新，所有贡献者应遵守并协助完善。