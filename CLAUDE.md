# CLAUDE.md - Kratos 项目开发指南

> **环境**: Git Bash on Windows | **框架**: Kratos v2 + Wire + GORM

---

## 快捷命令 (Commands)

```bash
# 生成 API 代码 (Proto -> Go)
make api

# 生成 Wire 依赖注入
cd cmd/resource && wire

# 生成所有代码并整理依赖
make generate

# 构建项目
make build

# 运行服务
./bin/resource -conf configs/config.yaml
```

---

## 架构分层 (Kratos DDD)

严格遵循分层依赖规则，**禁止跨层调用**：

```
┌─────────────────────────────────────────────────────┐
│  API Layer (api/)                                   │
│  - Proto 定义契约                                    │
│  - make api 生成 pb.go / grpc.pb.go / http.pb.go    │
└─────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────┐
│  Service Layer (internal/service/)                  │
│  - 实现 Proto 定义的 RPC 方法                        │
│  - Proto ↔ Biz 对象转换 (防腐层)                     │
│  - 调用 Biz UseCase，禁止直接调用 Data              │
└─────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────┐
│  Biz Layer (internal/biz/)                          │
│  - 领域模型 (DO) + 业务逻辑                          │
│  - 定义 Repo 接口 (由 Data 层实现)                   │
│  - 禁止出现 gorm.* / sql.* 依赖                     │
└─────────────────────────────────────────────────────┘
                         ↓
┌─────────────────────────────────────────────────────┐
│  Data Layer (internal/data/)                        │
│  - 实现 Biz 定义的 Repo 接口                         │
│  - DB 模型 (PO) + GORM 操作                          │
│  - 管理数据库/缓存连接                               │
└─────────────────────────────────────────────────────┘
```

---

## 新增 API 标准流程

当需要添加新 API 时，严格按以下顺序执行：

### Step 1: Proto 定义 (`api/<svc>/v1/<svc>.proto`)

```protobuf
service resourceService {
  rpc GetResource (GetResourceReq) returns (GetResourceReply) {
    option (google.api.http) = {
      get: "/v1/resources/{id}"  // 必须包含 v1 版本号
    };
  }
}

message GetResourceReq {
  int64 id = 1;
}

message GetResourceReply {
  Resource resource = 1;
}
```

**规范**:
- HTTP 路由必须包含 `/v1/` 版本前缀
- 列表接口使用 `repeated` 字段
- POST/PUT 请求添加 `body: "*"`

### Step 2: 生成代码

```bash
make api
```

生成文件:
- `*_pb.go` - Protobuf 消息
- `*_grpc.pb.go` - gRPC 服务接口
- `*_http.pb.go` - HTTP 服务接口

### Step 3: Biz 层 (`internal/biz/<svc>.go`)

```go
// 领域模型 (DO)
type Resource struct {
    ID        int64
    Name      string
    CreatedAt time.Time
}

// Repo 接口 (由 Data 层实现)
type ResourceRepo interface {
    GetByID(ctx context.Context, id int64) (*Resource, error)
    List(ctx context.Context, filter Filter) ([]Resource, error)
    Create(ctx context.Context, r *Resource) error
}

// UseCase
type ResourceUsecase struct {
    repo ResourceRepo      // 注入 Repo 接口
    log  *log.Helper       // 注入日志
}

func NewResourceUsecase(repo ResourceRepo, logger log.Logger) *ResourceUsecase {
    return &ResourceUsecase{repo: repo, log: log.NewHelper(logger)}
}
```

**铁律**:
- ✅ 定义 `Repo` 接口
- ✅ 注入 `log.Helper`
- ❌ 禁止 `import "gorm.io/gorm"`
- ❌ 禁止 `import "database/sql"`

### Step 4: Data 层 (`internal/data/<svc>.go`)

```go
// DB 模型 (PO)
type resourcePO struct {
    ID        int64  `gorm:"primaryKey"`
    Name      string `gorm:"column:name"`
}

func (resourcePO) TableName() string { return "resource" }

// 实现 Repo 接口
type resourceRepo struct {
    data *Data
    log  *log.Helper
}

func NewResourceRepo(data *Data, logger log.Logger) biz.ResourceRepo {
    return &resourceRepo{data: data, log: log.NewHelper(logger)}
}

func (r *resourceRepo) GetByID(ctx context.Context, id int64) (*biz.Resource, error) {
    var po resourcePO
    if err := r.data.db.WithContext(ctx).First(&po, id).Error; err != nil {
        return nil, err
    }
    return &biz.Resource{ID: po.ID, Name: po.Name}, nil  // PO -> DO 转换
}
```

### Step 5: Service 层 (`internal/service/<svc>.go`)

```go
type ResourceService struct {
    v1.UnimplementedResourceServiceServer
    uc *biz.ResourceUsecase
}

func NewResourceService(uc *biz.ResourceUsecase) *ResourceService {
    return &ResourceService{uc: uc}
}

func (s *ResourceService) GetResource(ctx context.Context, req *v1.GetResourceReq) (*v1.GetResourceReply, error) {
    // 调用 Biz UseCase
    resource, err := s.uc.GetByID(ctx, req.Id)
    if err != nil {
        return nil, err
    }
    // DO -> Proto 转换
    return &v1.GetResourceReply{
        Resource: &v1.Resource{
            Id:   resource.ID,
            Name: resource.Name,
        },
    }, nil
}
```

**铁律**:
- ✅ 通过 `Biz UseCase` 调用业务逻辑
- ❌ 禁止直接调用 `Data` 层

### Step 6: Server 注册

**gRPC** (`internal/server/grpc.go`):
```go
resourcev1.RegisterResourceServiceServer(srv, resource)
```

**HTTP** (`internal/server/http.go`):
```go
resourcev1.RegisterResourceServiceHTTPServer(srv, resource)
```

### Step 7: Wire 注入

更新各层 `ProviderSet`:

```go
// internal/biz/biz.go
var ProviderSet = wire.NewSet(NewResourceUsecase, ...)

// internal/data/data.go
var ProviderSet = wire.NewSet(NewResourceRepo, ...)

// internal/service/service.go
var ProviderSet = wire.NewSet(NewResourceService, ...)
```

生成注入代码:
```bash
cd cmd/resource && wire
# 或
make generate
```

### Step 8: 测试验证 (`tests/<api>.http`)

```http
### HTTP 测试
GET http://localhost:8000/v1/resources/123

### gRPC 测试
GRPC localhost:9000/resource.v1.resourceService/GetResource

{
  "id": 123
}
```

---

## 检查清单 (Checklist)

| # | 文件 | 操作 | 状态 |
|---|------|------|------|
| 1 | `api/<svc>/v1/*.proto` | 定义 service + rpc + message | ☐ |
| 2 | - | `make api` | ☐ |
| 3 | `internal/biz/<svc>.go` | 领域模型 + Repo 接口 + UseCase | ☐ |
| 4 | `internal/biz/biz.go` | 添加到 ProviderSet | ☐ |
| 5 | `internal/data/<svc>.go` | 实现 Repo 接口 | ☐ |
| 6 | `internal/data/data.go` | 添加到 ProviderSet | ☐ |
| 7 | `internal/service/<svc>.go` | 实现 Service | ☐ |
| 8 | `internal/service/service.go` | 添加到 ProviderSet | ☐ |
| 9 | `internal/server/grpc.go` | 注册 gRPC 服务 | ☐ |
| 10 | `internal/server/http.go` | 注册 HTTP 服务 | ☐ |
| 11 | - | `make generate` | ☐ |
| 12 | `tests/*.http` | 测试验证 | ☐ |

---

## 常见问题 (FAQ)

**Q: Proto 修改后不生效？**
```bash
make api
```

**Q: Wire 注入报错？**
1. 检查 `NewXxx` 是否添加到对应 `ProviderSet`
2. 重新生成: `make generate`

**Q: HTTP 路由 404？**
1. 检查 Proto 中 `google.api.http` 注解
2. 确认 `RegisterXxxHTTPServer` 已调用

**Q: 找不到 protoc 插件？**
```bash
make init
```

---

## 项目结构

```
.
├── api/                    # Proto 定义
│   └── resource/v1/
├── cmd/resource/           # 入口 + Wire
├── configs/                # 配置文件
├── internal/
│   ├── biz/                # 业务逻辑 (领域层)
│   ├── data/               # 数据访问 (基础设施层)
│   ├── service/            # 服务实现 (应用层)
│   ├── server/             # HTTP/gRPC Server
│   └── conf/               # 配置结构体
├── tests/                  # .http 测试文件
└── third_party/            # 第三方 Proto
```

---

## 文档同步规范

> **⚠️ 重要**: `AGENTS.md` 和 `CLAUDE.md` 必须保持同步更新。

| 文件 | 侧重内容 |
|------|----------|
| `AGENTS.md` | 项目规范、Agent 开发指南、代码风格约定 |
| `CLAUDE.md` | Kratos API 开发流程、快捷命令、检查清单 |

**同步规则**:
- 修改任一文件中的规范/流程/约束时，必须同时更新另一个文件
- 共同部分（分层架构、代码风格）必须保持一致
- 提交前检查两个文件的同步状态
