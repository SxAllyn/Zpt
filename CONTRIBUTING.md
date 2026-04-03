# 贡献指南

欢迎参与 Zpt-core 开发！请仔细阅读本指南以确保贡献符合项目标准。

## 开发流程

### 1. 环境准备
- Go 1.21 或更高版本
- Git
- 推荐编辑器：VS Code 或 Goland

### 2. 获取代码
```bash
git clone https://github.com/SxAllyn/zpt.git
cd zpt
```

### 3. 安装依赖
```bash
go mod download
```

### 4. 构建与测试
```bash
make build    # 构建
make test     # 运行测试
make lint     # 代码检查
```

## 代码规范

### 1. Go 代码风格
- 遵循 [Go 官方代码风格](https://golang.org/doc/effective_go)
- 使用 `gofmt` 自动格式化代码
- 最大行宽：120 字符

### 2. 命名约定
- **包名**：小写，单数名词，简洁明确
- **接口名**：`er` 结尾，如 `Reader`、`Writer`
- **变量名**：驼峰式，首字母小写
- **常量名**：全大写，下划线分隔
- **测试文件**：`_test.go` 后缀

### 3. 错误处理
- 使用 `error` 类型返回错误，不要使用 panic
- 错误信息应清晰说明问题原因
- 使用 `errors.New()` 或 `fmt.Errorf()` 创建错误
- 支持错误链：`fmt.Errorf("context: %w", err)`

### 4. 日志记录
- 使用项目统一的日志接口（待实现）
- 生产代码避免使用 `fmt.Print`
- 日志级别：DEBUG、INFO、WARN、ERROR

## 提交规范

### 1. 提交信息格式
遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

### 2. 提交类型
- `feat`: 新功能
- `fix`: 错误修复
- `docs`: 文档更新
- `style`: 代码格式调整（不影响功能）
- `refactor`: 代码重构（既非新功能也非错误修复）
- `perf`: 性能优化
- `test`: 测试相关
- `chore`: 构建过程或辅助工具变更
- `ci`: CI配置变更

### 3. 示例
```
feat(ztp): 添加多路复用支持

- 实现 Stream 打开/关闭逻辑
- 添加流控机制
- 更新相关测试

Closes #123
```

## Pull Request 流程

### 1. 创建分支
```bash
git checkout -b feat/your-feature-name
```

### 2. 开发与测试
- 编写代码和测试
- 确保所有测试通过：`make test`
- 确保代码格式化：`make fmt`
- 确保无代码质量问题：`make lint`

### 3. 提交代码
```bash
git add .
git commit -m "feat(module): description"
git push origin feat/your-feature-name
```

### 4. 创建 Pull Request
- 在 GitHub 创建 PR
- 标题清晰描述变更内容
- 详细说明修改原因和影响
- 关联相关 Issue（如有）

### 5. 代码审查
- 至少需要一名核心维护者批准
- 解决 Review 意见
- 确保 CI 通过

## 测试要求

### 1. 单元测试
- 每个导出函数必须有单元测试
- 测试覆盖率不低于 80%
- 使用 `t.Run()` 组织子测试
- 测试文件与被测试文件在同一目录

### 2. 集成测试
- 复杂功能需要集成测试
- 测试真实场景下的组件交互
- 确保测试可重复执行

### 3. 性能测试
- 关键路径需要性能基准测试
- 使用 `testing.B` 编写基准测试
- 监控内存分配和 CPU 使用

## 安全要求

### 1. 代码安全
- 禁止硬编码密钥或密码
- 敏感信息使用环境变量
- 验证所有外部输入
- 使用安全的随机数生成器

### 2. 依赖安全
- 定期更新依赖
- 使用 `govulncheck` 检查漏洞
- 审核第三方代码的安全性

### 3. 协议安全
- 自研协议需经过密码学专家评审
- 使用标准加密算法
- 实现完整的错误处理和边界检查

## 文档要求

### 1. 代码文档
- 所有导出函数、类型、常量必须有文档注释
- 文档注释使用完整的句子
- 示例代码应可运行

### 2. 用户文档
- 新功能需要更新用户文档
- 包含使用示例和配置说明
- 说明注意事项和常见问题

### 3. API 文档
- 公开 API 必须有完整文档
- 包含请求/响应示例
- 说明错误码和异常情况

## 项目管理

### 1. Issue 报告
- 使用模板报告问题或需求
- 提供重现步骤和环境信息
- 关联的 PR 应引用 Issue

### 2. 里程碑跟踪
- 项目按里程碑开发
- 每个里程碑有明确的目标和验收标准
- 进度在 DEVLOG.md 中记录

### 3. 版本发布
- 遵循语义化版本规范（SemVer）
- 发布前完成测试和安全审查
- 更新 CHANGELOG.md

## 获取帮助

- 查看 [项目文档](./docs/)
- 阅读 [开发规划](../开发规划.md)
- 在 GitHub Discussions 提问
- 联系核心维护者

---

感谢您的贡献！