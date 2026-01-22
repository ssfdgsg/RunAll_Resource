# 容器终端 Exec 功能设计文档

## 1. 功能概述

实现通过 gRPC 双向流暴露 Kubernetes exec 功能，允许 BFF 层通过 WebSocket 与用户交互，Resource 微服务负责与 K8s API Server 通信。

## 2. 架构设计

### 2.1 整体架构

```
浏览器 <--WSS--> BFF <--gRPC Stream--> Resource微服务 <--SPDY--> K8s API Server <--> Pod Container
```

### 2.2 Resource 微服务职责

- 接收 gRPC 双向流请求
- 根据 `instance_id` 查询实例信息，获取 namespace 和 pod 信息
- 建立与 K8s API Server 的 SPDY 连接
- 双向转发数据流：
  - 输入流：gRPC → K8s stdin
  - 输出流：K8s stdout/stderr → gRPC
- 处理终端大小调整（resize）
- 处理连接关闭和错误

## 3. API 设计

### 3.1 Protobuf 定义

**文件**: `api/resource/v1/resource.proto`

```protobuf
service resourceService {
  // 现有 RPC...
  
  // 容器 Exec 双向流
  rpc ExecContainer(stream ExecRequest) returns (stream ExecResponse);
}

// ========== 请求消息 ==========

message ExecRequest {
  oneof message {
    ExecInit init = 1;        // 初始化连接
    ExecInput input = 2;      // 标准输入
    ExecResize resize = 3;    // 终端大小调整
  }
}

// 初始化消息（第一条消息必须是此类型）
message ExecInit {
  int64 instance_id = 1;           // 实例ID
  repeated string command = 2;      // 执行的命令，如 ["/bin/bash"]
  bool tty = 3;                     // 是否分配 TTY，默认 true
  optional string container_name = 4; // 容器名称（可选，默认使用第一个容器）
}

// 标准输入消息
message ExecInput {
  bytes data = 1;  // 用户输入的数据（如键盘输入）
}

// 终端大小调整消息
message ExecResize {
  uint32 rows = 1;  // 终端行数
  uint32 cols = 2;  // 终端列数
}

// ========== 响应消息 ==========

message ExecResponse {
  oneof message {
    ExecOutput output = 1;    // 标准输出/错误
    ExecError error = 2;      // 错误信息
    ExecExit exit = 3;        // 退出信号
  }
}

// 输出消息
message ExecOutput {
  enum Stream {
    STDOUT = 0;  // 标准输出
    STDERR = 1;  // 标准错误
  }
  Stream stream = 1;  // 输出流类型
  bytes data = 2;     // 输出数据
}

// 错误消息
message ExecError {
  string message = 1;  // 错误描述
}

// 退出消息
message ExecExit {
  int32 code = 1;  // 退出码
}
```

### 3.2 消息流程

#### 初始化阶段

```
Client → Server: ExecRequest{init: ExecInit{instance_id: 123, command: ["/bin/bash"], tty: true}}
Server → Client: ExecResponse{output: ExecOutput{stream: STDOUT, data: "bash-5.1$ "}}
```

#### 交互阶段

```
Client → Server: ExecRequest{input: ExecInput{data: "ls -la\n"}}
Server → Client: ExecResponse{output: ExecOutput{stream: STDOUT, data: "total 64\ndrwxr-xr-x..."}}
```

#### 调整大小

```
Client → Server: ExecRequest{resize: ExecResize{rows: 30, cols: 100}}
```

#### 退出阶段

```
Server → Client: ExecResponse{exit: ExecExit{code: 0}}
```

## 4. 实现设计

### 4.1 分层架构

#### Service 层 (`internal/service/resource.go`)

- 实现 `ExecContainer` gRPC 双向流处理
- 接收初始化消息，解析 `instance_id`
- 创建输入/输出通道
- 启动协程处理输入流（gRPC → channel）
- 启动协程处理输出流（channel → gRPC）
- 调用 biz 层执行命令

#### Biz 层 (`internal/biz/resource.go`)

- 定义 `ExecRepo` 接口
- 定义数据结构：`ExecOptions`, `ExecInput`, `ExecOutput`
- 实现 `StreamExec` UseCase，编排业务逻辑
- 根据 `instance_id` 查询实例信息（namespace, pod name）

#### Data 层 (`internal/data/exec.go` - 新建)

- 实现 `ExecRepo` 接口
- 使用 `k8s.io/client-go/tools/remotecommand` 建立 SPDY 连接
- 创建 stdin/stdout/stderr 管道
- 双向转发数据流
- 处理 TTY 大小调整（使用 `TerminalSizeQueue`）

### 4.2 核心数据结构

#### Biz 层

```go
// ExecRepo K8s exec 操作接口
type ExecRepo interface {
    StreamExec(ctx context.Context, opts ExecOptions, 
               input <-chan ExecInput, 
               output chan<- ExecOutput) error
}

type ExecOptions struct {
    Namespace     string
    PodName       string
    ContainerName string
    Command       []string
    TTY           bool
}

type ExecInput struct {
    Type ExecInputType
    Data []byte
    Rows uint32
    Cols uint32
}

type ExecInputType int
const (
    ExecInputStdin ExecInputType = iota
    ExecInputResize
)

type ExecOutput struct {
    Type     ExecOutputType
    Stream   string // "stdout" or "stderr"
    Data     []byte
    ExitCode int32
}

type ExecOutputType int
const (
    ExecOutputData ExecOutputType = iota
    ExecOutputError
    ExecOutputExit
)
```

### 4.3 实例信息查询

根据 `instance_id` 查询实例信息：

1. 调用 `InstanceRepo.ListResourceSpecs(ctx, []int64{instanceID})`
2. 获取 `spec.UserID` 作为 namespace
3. Pod 名称使用 `strconv.FormatInt(instanceID, 10)`（与创建实例时一致）
4. 容器名称默认使用 Pod 名称（与 Deployment 定义一致）

### 4.4 K8s Exec 实现

使用 `remotecommand.NewSPDYExecutor` 建立连接：

```go
req := r.client.CoreV1().RESTClient().Post().
    Resource("pods").
    Name(podName).
    Namespace(namespace).
    SubResource("exec").
    VersionedParams(&corev1.PodExecOptions{
        Container: containerName,
        Command:   command,
        Stdin:     true,
        Stdout:    true,
        Stderr:    true,
        TTY:       tty,
    }, scheme.ParameterCodec)

exec, err := remotecommand.NewSPDYExecutor(r.config, "POST", req.URL())
```

### 4.5 流转发

#### 输入流处理

```go
go func() {
    for in := range inputChan {
        switch in.Type {
        case biz.ExecInputStdin:
            stdinWriter.Write(in.Data)
        case biz.ExecInputResize:
            // 通过 TerminalSizeQueue 发送 resize 事件
            sizeQueue.Next(&remotecommand.TerminalSize{
                Width:  uint16(in.Cols),
                Height: uint16(in.Rows),
            })
        }
    }
}()
```

#### 输出流处理

```go
go func() {
    buf := make([]byte, 8192)
    for {
        n, err := stdoutReader.Read(buf)
        if n > 0 {
            outputChan <- biz.ExecOutput{
                Type:   biz.ExecOutputData,
                Stream: "stdout",
                Data:   append([]byte(nil), buf[:n]...),
            }
        }
        if err != nil {
            break
        }
    }
}()
```

## 5. 错误处理

### 5.1 错误场景

| 场景 | 处理方式 |
|------|---------|
| 实例不存在 | 返回 `ExecError{message: "instance not found"}` |
| Pod 不存在 | 返回 `ExecError{message: "pod not found"}` |
| 容器不存在 | 返回 `ExecError{message: "container not found"}` |
| 权限不足 | 返回 `ExecError{message: "permission denied"}` |
| 网络中断 | gRPC 自动处理，客户端检测到流关闭 |
| 容器崩溃 | 返回 `ExecExit{code: 非0}` |

### 5.2 资源清理

- 使用 `defer` 确保管道关闭
- 使用 `context.Context` 传播取消信号
- gRPC 流关闭时自动清理 K8s 连接

## 6. 安全考虑

### 6.1 权限验证

- **BFF 层负责**：验证用户是否有权限访问指定实例
- Resource 微服务信任 BFF 层的请求（内网通信）

### 6.2 命令限制

- 不限制可执行命令（由 BFF 层或业务层控制）
- 可选：在 biz 层添加命令白名单验证

### 6.3 审计日志

- 不在 Resource 微服务记录审计日志
- 由 BFF 层负责记录用户操作审计

## 7. 性能考虑

### 7.1 并发连接

- 每个 exec 会话占用：
  - 1 个 gRPC 双向流
  - 1 个 K8s SPDY 连接
  - 3 个 goroutine（输入、stdout、stderr）
  - 约 64KB 内存（缓冲区）

### 7.2 缓冲区大小

- stdin/stdout/stderr 使用 8KB 缓冲区
- gRPC 流使用默认缓冲（4MB）

### 7.3 超时控制

- 使用 `context.Context` 传播超时
- 建议 BFF 层设置合理的超时时间（如 1 小时）

## 8. 测试计划

### 8.1 单元测试

- `internal/biz/resource_test.go`: 测试 UseCase 逻辑
- `internal/data/exec_test.go`: 测试 K8s exec 实现（使用 fake client）

### 8.2 集成测试

- 使用真实 K8s 集群（minikube）
- 测试完整的 exec 流程
- 测试错误场景（Pod 不存在、容器崩溃等）

### 8.3 手动测试

- 使用 grpcurl 测试 gRPC 流
- 使用 BFF 层 + 前端测试完整流程

## 9. 依赖包

```go
// 已有依赖
k8s.io/client-go
k8s.io/api
k8s.io/apimachinery

// 新增依赖（client-go 已包含）
k8s.io/client-go/tools/remotecommand
```

## 10. 实现步骤

1. ✅ 编写设计文档
2. ✅ 更新 `api/resource/v1/resource.proto`
3. ✅ 生成 protobuf 代码（`make api`）
4. ✅ 实现 biz 层接口和数据结构
5. ✅ 实现 data 层 K8s exec 逻辑
6. ✅ 实现 service 层 gRPC 流处理
7. ✅ 更新依赖注入（`wire generate`）
8. ⏳ 编写单元测试
9. ⏳ 集成测试验证

## 11. 后续优化

- 支持多容器 Pod 的容器选择
- 支持文件上传/下载（通过 tar 流）
- 支持会话录制（记录所有输入输出）
- 支持会话共享（多用户同时查看）

---

## 12. 实现总结

### 已完成的文件

1. **API 定义** (`api/resource/v1/resource.proto`)
   - 添加 `ExecContainer` RPC 定义
   - 定义 `ExecRequest` 和 `ExecResponse` 消息
   - 支持 init、input、resize 三种请求类型
   - 支持 output、error、exit 三种响应类型

2. **Biz 层** (`internal/biz/resource.go`)
   - 添加 `ExecRepo` 接口
   - 定义 `ExecOptions`、`ExecInput`、`ExecOutput` 数据结构
   - 实现 `StreamExec` UseCase
   - 根据 `instance_id` 查询实例信息并构建 exec 参数

3. **Data 层** (`internal/data/exec.go`)
   - 实现 `ExecRepo` 接口
   - 使用 `remotecommand.NewSPDYExecutor` 建立 K8s 连接
   - 实现双向流转发（stdin/stdout/stderr）
   - 实现终端大小调整（TerminalSizeQueue）

4. **Data 层基础设施** (`internal/data/data.go`)
   - 添加 `K8sClient` 结构体（封装 client 和 config）
   - 实现 `NewK8sClient` 构造函数
   - 更新 `ProviderSet` 添加 `NewExecRepo`

5. **K8s 仓储** (`internal/data/k8sInstance.go`)
   - 更新 `k8sRepo` 添加 `config` 字段
   - 修改 `NewK8sRepo` 使用共享的 `K8sClient`

6. **Service 层** (`internal/service/resource.go`)
   - 实现 `ExecContainer` gRPC 双向流处理
   - 处理初始化消息验证
   - 启动输入输出协程进行流转发
   - 调用 biz 层执行命令

7. **依赖注入** (`cmd/resource/wire_gen.go`)
   - 自动生成依赖注入代码
   - 正确注入 `ExecRepo` 到 `ResourceUsecase`

### API 设计特点

✅ **符合云平台标准**：
- 用户只需提供 `instance_id`，无需任何集群配置
- 服务端内部处理 namespace、pod name 的映射
- 完全隔离底层 K8s 实现细节

✅ **安全性**：
- BFF 层负责权限验证
- Resource 微服务信任内网请求
- 支持容器名称指定（可选）

✅ **易用性**：
- 简洁的 API 设计
- 支持 TTY 和非 TTY 模式
- 支持终端大小动态调整

### 测试建议

1. **单元测试**：使用 fake K8s client 测试 exec 逻辑
2. **集成测试**：在真实 K8s 集群中测试完整流程
3. **手动测试**：使用 grpcurl 或 BFF 层测试

### 下一步

- 编写单元测试
- 集成测试验证
- 性能测试（并发连接数）
- 文档完善（API 使用示例）
