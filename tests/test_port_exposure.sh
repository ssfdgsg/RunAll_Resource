#!/bin/bash
# 测试端口暴露功能
# 使用方法: ./tests/test_port_exposure.sh <instance_id>

set -e

INSTANCE_ID=${1:-"6421570121939233912"}
API_BASE="http://localhost:7999/api"

echo "=========================================="
echo "测试实例网络端口暴露功能"
echo "实例ID: $INSTANCE_ID"
echo "=========================================="

# 1. 打开 TCP 端口 80
echo ""
echo "1. 打开 TCP 端口 80..."
curl -X POST "$API_BASE/instances/$INSTANCE_ID/ports" \
  -H "Content-Type: application/json" \
  -d '{
    "ports": [
      {"port": 80, "protocol": "TCP", "open": true}
    ]
  }' | jq .

# 2. 打开 HTTP 端口 8080
echo ""
echo "2. 打开 HTTP 端口 8080..."
curl -X POST "$API_BASE/instances/$INSTANCE_ID/ports" \
  -H "Content-Type: application/json" \
  -d '{
    "ports": [
      {"port": 8080, "protocol": "HTTP", "open": true}
    ]
  }' | jq .

# 3. 验证 ingress-nginx ConfigMap
echo ""
echo "3. 验证 ingress-nginx ConfigMap..."
kubectl -n ingress-nginx get cm tcp-services -o yaml | grep -A 2 "data:"

# 4. 验证 ingress-nginx Service 端口
echo ""
echo "4. 验证 ingress-nginx Service 端口..."
kubectl -n ingress-nginx get svc ingress-nginx-controller -o jsonpath='{.spec.ports[*].name}' | tr ' ' '\n' | grep -E "tcp-|udp-"

# 5. 获取 LoadBalancer IP
echo ""
echo "5. 获取 LoadBalancer IP..."
LB_IP=$(kubectl -n ingress-nginx get svc ingress-nginx-controller -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "LoadBalancer IP: $LB_IP"

# 6. 测试 TCP 端口连接（需要容器内运行 nginx 或其他服务）
echo ""
echo "6. 测试 TCP 端口连接..."
EXTERNAL_PORT=$(kubectl -n ingress-nginx get cm tcp-services -o yaml | grep ":" | head -1 | cut -d'"' -f2 | cut -d':' -f1)
echo "外部端口: $EXTERNAL_PORT"
echo "尝试连接: curl --noproxy '*' http://$LB_IP:$EXTERNAL_PORT"
curl --noproxy '*' -v --connect-timeout 5 "http://$LB_IP:$EXTERNAL_PORT" || echo "连接失败（可能容器内服务未启动）"

# 7. 关闭端口
echo ""
echo "7. 关闭端口..."
read -p "是否关闭端口？(y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
  curl -X POST "$API_BASE/instances/$INSTANCE_ID/ports" \
    -H "Content-Type: application/json" \
    -d '{
      "ports": [
        {"port": 80, "protocol": "TCP", "open": false},
        {"port": 8080, "protocol": "HTTP", "open": false}
      ]
    }' | jq .
  
  echo ""
  echo "验证端口已关闭..."
  kubectl -n ingress-nginx get cm tcp-services -o yaml | grep -A 2 "data:"
fi

echo ""
echo "=========================================="
echo "测试完成"
echo "=========================================="
