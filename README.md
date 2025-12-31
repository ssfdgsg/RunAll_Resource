# Resource 资源管理服务

基于 [Kratos v2](https://go-kratos.dev/) 微服务框架构建的云资源实例管理服务，提供实例的全生命周期管理能力，支持 HTTP/gRPC 双协议接入与 RabbitMQ 事件驱动。

## 项目概述

本服务是一个典型的 DDD 分层架构微服务，核心功能包括：

- **资源实例管理**：创建、查询云计算实例（含 CPU/Memory/GPU 规格）
- **Kubernetes 集成**：自动将实例规格转换为 K8s Pod 并调度到集群
- **事件驱动架构**：通过 RabbitMQ 消费实例生命周期事件（创建/删除/规格变更等）
- **审计日志**：记录所有实例操作的完整审计轨迹

## 技术栈

| 组件 | 技术选型 |
|------|----------|
| 框架 | Kratos v2 |
| 依赖注入 | Google Wire |
| 数据库 | PostgreSQL + GORM |
| 消息队列 | RabbitMQ |
| 容器编排 | Kubernetes client-go |
| 协议 | gRPC / HTTP (RESTful) |
| 序列化 | Protocol Buffers |

## 项目结构

```
.
├── api/                          # Protobuf API 定义
│   ├── helloworld/v1/            # 示例 Greeter 服务
│   ├── mq/v1/                    # MQ 事件消息定义
│   └── resource/v1/              # 资源服务 API
├── cmd/resource/                 # 应用入口
│   ├── main.go                   # 启动逻辑
│   ├── wire.go                   # Wire 依赖声明
│   └── wire_gen.go               # Wire 生成代码
├── configs/                      # 运行时配置
│   └── config.yaml
├── internal/                     # 内部实现（不可外部导入）
│   ├── biz/                      # 业务逻辑层（UseCase + 领域模型）
│   ├── conf/                     # 配置结构定义
│   ├── data/                     # 数据访问层（Repo 实现）
│   ├── server/                   # 服务器实例（HTTP/gRPC/MQ）
│   └── service/                  # 服务层（API 实现）
├── third_party/                  # 第三方 Proto 依赖
└── tests/                        # 测试文件
```

## 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                      Transport Layer                        │
│              (HTTP Server / gRPC Server / MQ Server)        │
├─────────────────────────────────────────────────────────────┤
│                      Service Layer                          │
│         (GreeterService / ResourceService)                  │
│              实现 API 接口，编排 UseCase                      │
├─────────────────────────────────────────────────────────────┤
│                      Business Layer                         │
│         (GreeterUsecase / ResourceUsecase)                  │
│              核心业务逻辑，定义 Repo 接口                      │
├─────────────────────────────────────────────────────────────┤
│                       Data Layer                            │
│    (GreeterRepo / InstanceRepo / AuditRepo / K8sRepo)       │
│              实现 Repo 接口，管理外部依赖                      │
└─────────────────────────────────────────────────────────────┘
```

## API 接口

### HTTP 端点

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/helloworld/{name}` | 问候服务示例 |
| GET | `/v1/resources` | 查询资源列表 |

### gRPC 服务

- `helloworld.v1.Greeter/SayHello` - 问候服务
- `resource.v1.resourceService/ListResources` - 资源列表查询

### MQ 事件类型

服务通过 RabbitMQ 消费以下实例生命周期事件：

| 事件类型 | 描述 |
|----------|------|
| `INSTANCE_CREATED` | 实例创建 |
| `INSTANCE_DELETED` | 实例删除 |
| `INSTANCE_SPEC_CHANGED` | 规格变更 |
| `INSTANCE_STARTED` | 实例启动 |
| `INSTANCE_STOPPED` | 实例停止 |
| `INSTANCE_K8S_SYNC` | K8s 状态同步 |

## 快速开始

### 环境要求

- Go 1.24+
- PostgreSQL 15+
- RabbitMQ 3.x
- Kubernetes 集群（可选）
- protoc 编译器

### 安装依赖

```bash
# 安装 protoc 插件和 wire
make init
```

### 配置

编辑 `configs/config.yaml`：

```yaml
server:
  http:
    addr: 0.0.0.0:8000
    timeout: 1s
  grpc:
    addr: 0.0.0.0:9000
    timeout: 1s
data:
  database:
    driver: postgresql
    source: postgresql://user:pass@localhost:5432/resource
  rabbitmq:
    url: amqp://guest:guest@localhost:5672/
    queue: greeter.hello
  kubernetes:
    kubeconfig: ~/.kube/config
```

### 构建与运行

```bash
# 生成 API 代码
make api

# 生成配置代码
make config

# 生成 Wire 依赖注入代码
make generate

# 构建
make build

# 运行
./bin/resource -conf ./configs
```

### Docker 部署

```bash
# 构建镜像
docker build -t resource:latest .

# 运行容器
docker run --rm -p 8000:8000 -p 9000:9000 \
  -v /path/to/configs:/data/conf \
  resource:latest
```

## 开发指南

### 代码生成

```bash
# 重新生成所有 API 和配置代码
make all

# 仅生成 API
make api

# 仅生成内部配置
make config

# 生成 Wire 依赖
make generate
```

### 运行测试

```bash
go test ./...
```

### 发送测试消息

```bash
# 运行 MQ 测试脚本发送实例创建事件
go run tests/test_mq.go -conf ./configs
```

## 数据模型

### Instance（实例聚合根）

| 字段 | 类型 | 描述 |
|------|------|------|
| instance_id | int64 | 雪花 ID |
| user_id | uuid | 用户 ID |
| name | string | 实例名称 |
| status | string | 状态（CREATING/RUNNING/STOPPED） |
| created_at | timestamp | 创建时间 |
| updated_at | timestamp | 更新时间 |

### InstanceSpec（实例规格）

| 字段 | 类型 | 描述 |
|------|------|------|
| instance_id | int64 | 关联实例 ID |
| cpu | uint32 | CPU 核数 |
| memory | uint32 | 内存大小 (MB) |
| gpu | uint32 | GPU 类型编码 |
| image | string | 容器镜像 |
| config_json | jsonb | 自定义配置 |

## GPU 类型映射

| 编码 | GPU 型号 |
|------|----------|
| 0 | 无 GPU |
| 1 | Tesla V100 |
| 2 | Tesla A100 |
| 3 | Tesla H100 |
| 4 | Tesla T4 |
| 5 | RTX 3090 |
| 6 | RTX 4060 Mobile |

## 许可证

[MIT License](LICENSE)
