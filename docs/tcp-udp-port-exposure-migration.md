# TCP/UDP 端口暴露功能改造文档

## 改造概述

将 TCP/UDP 端口暴露功能从 **NodePort Service** 改为 **ClusterIP Service + ingress-nginx ConfigMap** 方式。

## 改造动机

### 原实现的问题

1. **NodePort 依赖 NodeIP 可达**: 返回 `<node-ip>:<node-port>`，要求客户端能直接访问 K8s 节点 IP
2. **端口范围受限**: NodePort 范围固定（30000-32767），且每个 Service 占用一个端口
3. **kube-proxy 规则爆炸**: 大量 NodePort Service 会导致 iptables/ipvs 规则过多
4. **不符合生产实践**: 生产环境通常通过统一网关（LoadBalancer/Ingress）暴露服务

### 新实现的优势

1. **统一网关入口**: 所有流量通过 ingress-nginx LoadBalancer IP 进入
2. **更贴近生产**: 使用 ingress-nginx 官方支持的 TCP/UDP 暴露方式
3. **更好的可观测性**: 流量集中在 ingress-nginx，便于监控和日志收集
4. **更灵活的配置**: 可以通过 ConfigMap 动态管理端口映射

## 技术方案

### 架构对比

#### 原架构（NodePort）

```
Client → NodeIP:NodePort → NodePort Service → Pod
```

#### 新架构（ingress-nginx ConfigMap）

```
Client → LB-IP:ExternalPort → ingress-nginx Service → ingress-nginx ConfigMap → ClusterIP Service → Pod
```

### 工作流程

1. **创建 ClusterIP Service**: 指向目标 Pod
2. **分配外部端口**: 从端口池分配（30000-32767）
3. **Patch ConfigMap**: 添加映射 `<external-port>: <namespace>/<service-name>:<port>`
4. **Patch ingress-nginx Service**: 添加端口到 Service（关键步骤）
5. **获取 LoadBalancer IP**: 从 ingress-nginx Service 获取
6. **返回访问地址**: `<lb-ip>:<external-port>`

### 关键发现

**问题**: TCP/UDP 端口暴露后无法访问

**原因**: ingress-nginx 通过 ConfigMap 配置 TCP/UDP 端口后，还必须在 Service 上暴露对应端口才能访问。

**解决方案**: 
- 添加 `patchIngressNginxServicePort` 方法自动添加端口
- 添加 `removeIngressNginxServicePort` 方法删除端口时清理
- 端口名称必须小写（`tcp-30000` 而不是 `TCP-30000`）

## 代码变更

### 1. 配置文件

#### `internal/conf/conf.proto`

添加 ingress-nginx 配置字段：

```protobuf
message Data {
  message Kubernetes {
    string kubeconfig = 1;
    string ingress_domain = 2;
    string ingress_nginx_namespace = 3;
    string ingress_nginx_lb_service = 4;
    uint32 tcp_udp_port_range_start = 5;
    uint32 tcp_udp_port_range_end = 6;
  }
  Kubernetes kubernetes = 4;
}
```

#### `configs/config.yaml`

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

### 2. 数据模型

#### `internal/data/network.go`

```go
type instanceNetwork struct {
    InstanceID   int64     `gorm:"primaryKey;column:instance_id"`
    Port         uint32    `gorm:"primaryKey;column:port"`
    ServiceName  string    `gorm:"column:service_name;size:64"`
    ServicePort  uint32    `gorm:"column:service_port"`
    ExternalPort *uint32   `gorm:"column:external_port"`  // 改：NodePort → ExternalPort
    IngressName  *string   `gorm:"column:ingress_name;size:64"`
    Protocol     string    `gorm:"column:protocol"`
    AccessURL    string    `gorm:"column:access_url"`
    Enabled      bool      `gorm:"column:enabled"`
}
```

### 3. 业务逻辑

#### `internal/biz/resource.go`

```go
type NetworkBinding struct {
    InstanceID   int64
    Port         uint32
    ServiceName  string
    ServicePort  uint32
    ExternalPort *uint32  // 改：NodePort → ExternalPort
    IngressName  *string
    Protocol     string
    AccessURL    string
    Enabled      bool
}

type K8sRepo interface {
    // 改：返回 serviceName, externalPort, error
    CreateServiceForTCPUDP(ctx context.Context, namespace, instanceID string, port uint32, protocol string) (string, uint32, error)
    
    // 新增：删除 ConfigMap 条目
    DeleteTCPUDPConfigMapEntry(ctx context.Context, protocol string, externalPort uint32) error
    
    // 新增：获取 LoadBalancer IP
    GetIngressNginxLBIP(ctx context.Context) (string, error)
}
```

### 4. K8s 操作

#### `internal/data/k8sInstance.go`

**核心方法**:

1. `CreateServiceForTCPUDP`: 创建 ClusterIP Service + patch ConfigMap + patch Service
2. `patchIngressNginxConfigMap`: 添加 ConfigMap 条目
3. `patchIngressNginxServicePort`: 添加 Service 端口（新增）
4. `removeIngressNginxServicePort`: 删除 Service 端口（新增）
5. `DeleteTCPUDPConfigMapEntry`: 删除 ConfigMap 条目和 Service 端口
6. `GetIngressNginxLBIP`: 获取 LoadBalancer IP
7. `allocateExternalPort`: 分配外部端口（简单自增）

### 5. 数据库迁移

#### `migrations/001_create_instance_network_table.sql`

```sql
CREATE TABLE IF NOT EXISTS instance_network (
    instance_id BIGINT NOT NULL,
    port INTEGER NOT NULL,
    service_name VARCHAR(64) NOT NULL,
    service_port INTEGER NOT NULL,
    external_port INTEGER,  -- 改：node_port → external_port
    ingress_name VARCHAR(64),
    protocol VARCHAR(10) NOT NULL DEFAULT 'HTTP',
    access_url TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_id, port)
);
```

## 部署指南

### 1. K3s 集群部署

#### 安装 ingress-nginx

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.8.1/deploy/static/provider/cloud/deploy.yaml
```

#### 配置 externalIPs（如果没有 LoadBalancer）

```bash
kubectl -n ingress-nginx patch svc ingress-nginx-controller \
  -p '{"spec":{"externalIPs":["47.110.74.199"]}}'
```

#### 验证安装

```bash
# 检查 ingress-nginx Pod
kubectl -n ingress-nginx get pods

# 检查 Service
kubectl -n ingress-nginx get svc ingress-nginx-controller

# 检查 ConfigMap
kubectl -n ingress-nginx get cm tcp-services udp-services
```

#### 配置安全组

确保云服务器安全组开放以下端口：
- 6443 (K3s API Server)
- 80, 443 (HTTP/HTTPS)
- 30000-32767 (TCP/UDP 端口范围)

### 2. Minikube 环境部署

```bash
# 启用 ingress 插件
minikube addons enable ingress

# 改为 LoadBalancer 类型
kubectl -n ingress-nginx patch svc ingress-nginx-controller \
  -p '{"spec":{"type":"LoadBalancer"}}'

# 运行 tunnel（模拟 LoadBalancer）
minikube tunnel
```

### 3. 应用配置

#### 连接 K3s 集群

1. 从 K3s 服务器获取 kubeconfig：
   ```bash
   sudo cat /etc/rancher/k3s/k3s.yaml
   ```

2. 创建 `k3s-config.yaml`：
   ```yaml
   apiVersion: v1
   clusters:
   - cluster:
       server: https://47.110.74.199:6443
       insecure-skip-tls-verify: true
     name: default
   contexts:
   - context:
       cluster: default
       user: default
     name: default
   current-context: default
   kind: Config
   users:
   - name: default
     user:
       client-certificate-data: <base64-cert>
       client-key-data: <base64-key>
   ```

3. 更新 `configs/config.yaml`：
   ```yaml
   data:
     kubernetes:
       kubeconfig: k3s-config.yaml
       ingress_domain: "47.110.74.199.nip.io"
   ```

#### 执行数据库迁移

```bash
psql -h localhost -p 5433 -U postgres -d resource -f migrations/001_create_instance_network_table.sql
```

#### 重新生成配置代码

```bash
make config
make generate
```

#### 重新构建和运行

```bash
make build
./bin/resource -conf configs/config.yaml
```

## 测试验证

### 1. 使用 HTTP 测试文件

```http
### 打开 TCP 端口
POST http://localhost:7999/api/instances/{{instanceID}}/ports
Content-Type: application/json

{
  "ports": [
    {"port": 80, "protocol": "TCP", "open": true}
  ]
}

### 打开 HTTP 端口
POST http://localhost:7999/api/instances/{{instanceID}}/ports
Content-Type: application/json

{
  "ports": [
    {"port": 8080, "protocol": "HTTP", "open": true}
  ]
}
```

### 2. 使用测试脚本

```bash
chmod +x tests/test_port_exposure.sh
./tests/test_port_exposure.sh <instance_id>
```

### 3. 手动验证

```bash
# 1. 检查 ConfigMap
kubectl -n ingress-nginx get cm tcp-services -o yaml

# 2. 检查 Service 端口
kubectl -n ingress-nginx get svc ingress-nginx-controller

# 3. 获取 LoadBalancer IP
kubectl -n ingress-nginx get svc ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}'

# 4. 测试连接
curl --noproxy '*' http://<lb-ip>:<external-port>
```

## 故障排查

### 问题 1: 端口无法访问

**症状**: curl 连接超时或拒绝连接

**排查步骤**:

1. 检查 ConfigMap 是否有条目：
   ```bash
   kubectl -n ingress-nginx get cm tcp-services -o yaml | grep <external-port>
   ```

2. 检查 Service 是否暴露端口：
   ```bash
   kubectl -n ingress-nginx get svc ingress-nginx-controller -o yaml | grep <external-port>
   ```

3. 检查 LoadBalancer IP：
   ```bash
   kubectl -n ingress-nginx get svc ingress-nginx-controller
   ```

4. 检查容器内服务是否运行：
   ```bash
   kubectl exec -it <pod-name> -- curl localhost:<port>
   ```

5. 检查安全组是否开放端口

### 问题 2: 证书验证失败（K3s）

**症状**: `x509: certificate is valid for 10.x.x.x, not 47.110.74.199`

**解决方案**: 在 kubeconfig 中添加 `insecure-skip-tls-verify: true`

### 问题 3: HTTP 代理干扰

**症状**: curl 返回 502 Bad Gateway

**解决方案**: 使用 `--noproxy '*'` 参数或取消代理：
```bash
unset http_proxy https_proxy
```

### 问题 4: 端口名称验证失败

**症状**: `spec.ports[x].name: Invalid value: "TCP-30000"`

**解决方案**: 端口名称必须小写（`tcp-30000`）

## 局限性与改进方向

### 当前局限性

1. **端口池管理简单**: 使用简单自增，不支持端口回收和重用
2. **重启后端口计数器重置**: 可能导致端口冲突
3. **不支持分布式环境**: 多实例部署时可能分配相同端口
4. **ConfigMap 条目过多**: 大规模场景下 ConfigMap 可能变得很大

### 改进方向

1. **数据库端口池管理**:
   - 在数据库中记录端口分配状态
   - 实现端口回收和重用机制
   - 支持端口预留和配额管理

2. **分布式锁**:
   - 使用 Redis 或 etcd 实现分布式锁
   - 避免多实例并发分配相同端口

3. **端口范围分段**:
   - 为不同租户/项目分配不同端口段
   - 提高端口利用率和隔离性

4. **动态配置更新**:
   - 监听 ConfigMap 变化
   - 自动同步 Service 端口

5. **更高级的方案**:
   - 使用 Gateway API 替代 ConfigMap
   - 使用 Envoy 动态配置替代静态 ConfigMap

## 参考资料

- [ingress-nginx TCP/UDP 暴露文档](https://kubernetes.github.io/ingress-nginx/user-guide/exposing-tcp-udp-services/)
- [Kubernetes Service 类型](https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types)
- [K3s 文档](https://docs.k3s.io/)
- [nip.io 魔法域名](https://nip.io/)

## 总结

这次改造将 TCP/UDP 端口暴露功能从 NodePort 改为 ingress-nginx ConfigMap 方式，使其更贴近生产实践。关键发现是必须同时 patch ingress-nginx Service 才能使端口可访问。虽然当前端口池管理较简单，但已满足学习和小规模使用需求。生产环境建议实现更完善的端口池管理和分布式锁机制。
