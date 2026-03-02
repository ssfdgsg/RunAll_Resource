# 数据库迁移

本目录包含数据库迁移脚本，用于管理数据库 schema 的版本演进。

## 迁移文件命名规范

迁移文件按照以下格式命名：

```
{序号}_{描述}.sql
```

例如：
- `001_create_instance_network_table.sql`
- `002_add_user_quota_table.sql`

## 执行迁移

### 手动执行

使用 `psql` 命令行工具执行迁移：

```bash
psql -h localhost -p 5433 -U postgres -d resource -f migrations/001_create_instance_network_table.sql
```

### 自动执行

在应用启动时，可以使用 GORM 的 AutoMigrate 功能自动创建表结构。但对于复杂的迁移（如添加索引、修改列类型等），建议使用 SQL 脚本手动执行。

## 迁移历史

### 001_create_instance_network_table.sql

**创建时间**: 2026-01-26

**描述**: 创建实例网络端口暴露配置表 `instance_network`

**功能**:
- 支持两种端口暴露模式：
  1. **TCP/UDP 模式**: 通过 ClusterIP Service + ingress-nginx ConfigMap 暴露
     - 使用 `external_port` 字段存储外部端口（ConfigMap key）
     - 访问地址格式：`<ingress-lb-ip>:<external-port>`
  2. **HTTP 模式**: 通过 ClusterIP Service + Ingress 暴露
     - 使用 `ingress_name` 字段存储 Ingress 名称
     - 访问地址格式：`http(s)://<domain>/<path>`

**表结构**:
- `instance_id` (BIGINT): 实例ID，主键之一
- `port` (INTEGER): 容器端口，主键之一
- `service_name` (VARCHAR): Kubernetes Service 名称
- `service_port` (INTEGER): Service 暴露的端口
- `external_port` (INTEGER): TCP/UDP 模式的外部端口（可选）
- `ingress_name` (VARCHAR): HTTP 模式的 Ingress 名称（可选）
- `protocol` (VARCHAR): 协议类型（TCP/UDP/HTTP）
- `access_url` (TEXT): 最终访问地址
- `enabled` (BOOLEAN): 是否启用
- `created_at` (TIMESTAMP): 创建时间
- `updated_at` (TIMESTAMP): 更新时间

**索引**:
- `idx_instance_network_instance_id`: 按实例ID查询
- `idx_instance_network_protocol`: 按协议类型查询
- `idx_instance_network_enabled`: 按启用状态查询

## 实例网络端口暴露功能设计

### 架构概览

实例网络端口暴露功能允许将容器内的端口暴露到外部，支持两种模式：

#### 1. TCP/UDP 模式

**实现方式**: ClusterIP Service + ingress-nginx ConfigMap + ingress-nginx Service 端口

**工作流程**:
1. 创建 ClusterIP Service 指向 Pod
2. 分配外部端口（从端口池）
3. Patch ingress-nginx ConfigMap（`tcp-services` 或 `udp-services`）添加映射：`<external-port>: <namespace>/<service-name>:<port>`
4. Patch ingress-nginx Service 添加端口（关键步骤，否则端口无法访问）
5. 获取 ingress-nginx LoadBalancer IP
6. 返回访问地址：`<lb-ip>:<external-port>`

**适用场景**: 需要直接 TCP/UDP 连接的服务（如数据库、Redis、自定义协议）

**端口范围**: 30000-32767（可配置）

**访问方式**: `<ingress-lb-ip>:<external-port>`

#### 2. HTTP 模式

**实现方式**: ClusterIP Service + Ingress

**工作流程**:
1. 创建 ClusterIP Service 指向 Pod
2. 创建 Ingress 配置路径规则
3. 返回访问地址：`http(s)://<domain>/<namespace>/<instance-id>/<port>`

**适用场景**: HTTP/HTTPS Web 服务

**访问方式**: `http(s)://<domain>/<path>`

### 命名规范

- **Service**: `instance-{instance_id}-{port}`
- **Ingress**: `ingress-{instance_id}-{port}`
- **ingress-nginx Service 端口名**: `tcp-{external_port}` 或 `udp-{external_port}`（必须小写）

### 配置参数

在 `configs/config.yaml` 中配置：

```yaml
data:
  kubernetes:
    kubeconfig: k3s-config.yaml
    ingress_domain: "47.110.74.199.nip.io"
    ingress_nginx_namespace: "ingress-nginx"
    ingress_nginx_lb_service: "ingress-nginx-controller"
    tcp_udp_port_range_start: 30000
    tcp_udp_port_range_end: 32767
```

### 相关代码

- **API 定义**: `api/resource/v1/resource.proto` (SetInstancePort RPC)
- **业务逻辑**: `internal/biz/resource.go` (NetworkBinding, NetworkRepo)
- **数据访问**: `internal/data/network.go` (网络配置持久化)
- **K8s 操作**: `internal/data/k8sInstance.go` (Service/Ingress/ConfigMap 管理)
- **数据库迁移**: `migrations/001_create_instance_network_table.sql`

### 端口池管理

**当前实现**: 简单自增分配（`nextAvailablePort`）

**局限性**:
- 不支持端口回收和重用
- 重启后端口计数器重置
- 不支持分布式环境

**生产环境建议**:
- 使用数据库存储端口分配状态
- 实现端口回收机制
- 支持端口预留和配额管理
- 考虑使用分布式锁避免端口冲突

### 关键发现

**问题**: TCP/UDP 端口暴露后无法访问

**原因**: ingress-nginx 通过 ConfigMap 配置 TCP/UDP 端口后，还必须在 Service 上暴露对应端口才能访问。

**解决方案**: 
- 添加 `patchIngressNginxServicePort` 方法自动添加端口到 ingress-nginx Service
- 添加 `removeIngressNginxServicePort` 方法删除端口时清理
- 端口名称必须小写（`tcp-30000` 而不是 `TCP-30000`）

### 测试

使用 `tests/NetworkPort.http` 测试端口暴露功能：

```http
### 打开端口（TCP/UDP 和 HTTP）
POST http://localhost:7999/api/instances/{{instanceID}}/ports
Content-Type: application/json

{
  "ports": [
    {"port": 80, "protocol": "TCP", "open": true},
    {"port": 8080, "protocol": "HTTP", "open": true}
  ]
}

### 关闭端口
POST http://localhost:7999/api/instances/{{instanceID}}/ports
Content-Type: application/json

{
  "ports": [
    {"port": 80, "protocol": "TCP", "open": false}
  ]
}
```

### 部署要求

#### K3s 集群

1. **安装 ingress-nginx**:
   ```bash
   kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.8.1/deploy/static/provider/cloud/deploy.yaml
   ```

2. **配置 ingress-nginx Service 的 externalIPs**（如果没有 LoadBalancer）:
   ```bash
   kubectl -n ingress-nginx patch svc ingress-nginx-controller \
     -p '{"spec":{"externalIPs":["47.110.74.199"]}}'
   ```

3. **确保云服务器安全组开放端口**:
   - 6443 (K3s API Server)
   - 80, 443 (HTTP/HTTPS)
   - 30000-32767 (TCP/UDP 端口范围)

#### Minikube 环境

1. **启用 ingress 插件**:
   ```bash
   minikube addons enable ingress
   ```

2. **将 ingress-nginx Service 改为 LoadBalancer**:
   ```bash
   kubectl -n ingress-nginx patch svc ingress-nginx-controller \
     -p '{"spec":{"type":"LoadBalancer"}}'
   ```

3. **运行 minikube tunnel**（模拟 LoadBalancer）:
   ```bash
   minikube tunnel
   ```

### 故障排查

1. **端口无法访问**:
   - 检查 ingress-nginx ConfigMap 是否有对应条目
   - 检查 ingress-nginx Service 是否暴露了对应端口
   - 检查 LoadBalancer IP 是否正确
   - 检查云服务器安全组是否开放端口

2. **证书验证失败**（K3s）:
   - 在 kubeconfig 中添加 `insecure-skip-tls-verify: true`
   - 移除 `certificate-authority-data` 字段

3. **HTTP 代理干扰**:
   - 使用 `--noproxy "*"` 参数
   - 或临时取消代理：`unset http_proxy https_proxy`
