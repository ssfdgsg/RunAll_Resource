#!/bin/bash

# 网络端口暴露验证脚本
# 用于验证 SetInstancePort 功能是否正常工作

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 配置
INSTANCE_ID="${1:-5237967844223404952}"
NAMESPACE="${2:-550e8400-e29b-41d4-a716-446655440001}"

echo -e "${BLUE}=== 网络端口暴露验证 ===${NC}"
echo "Instance ID: $INSTANCE_ID"
echo "Namespace: $NAMESPACE"
echo ""

# 1. 检查 Service
echo -e "${YELLOW}[1/5] 检查 Service 资源${NC}"
SERVICES=$(kubectl get svc -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" --no-headers 2>/dev/null || echo "")
if [ -z "$SERVICES" ]; then
    echo -e "${RED}✗ 未找到 Service${NC}"
else
    echo -e "${GREEN}✓ 找到 Service:${NC}"
    kubectl get svc -n "$NAMESPACE" -l instance-id="$INSTANCE_ID"
    echo ""
    
    # 显示详细信息
    echo -e "${BLUE}Service 详细信息:${NC}"
    kubectl get svc -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" -o wide
fi
echo ""

# 2. 检查 Ingress
echo -e "${YELLOW}[2/5] 检查 Ingress 资源${NC}"
INGRESSES=$(kubectl get ingress -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" --no-headers 2>/dev/null || echo "")
if [ -z "$INGRESSES" ]; then
    echo -e "${YELLOW}⚠ 未找到 Ingress (如果只使用 TCP/UDP 则正常)${NC}"
else
    echo -e "${GREEN}✓ 找到 Ingress:${NC}"
    kubectl get ingress -n "$NAMESPACE" -l instance-id="$INSTANCE_ID"
    echo ""
    
    # 显示详细信息
    echo -e "${BLUE}Ingress 详细信息:${NC}"
    kubectl describe ingress -n "$NAMESPACE" -l instance-id="$INSTANCE_ID"
fi
echo ""

# 3. 检查 Pod 状态
echo -e "${YELLOW}[3/5] 检查 Pod 状态${NC}"
POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
if [ -z "$POD_NAME" ]; then
    echo -e "${RED}✗ 未找到 Pod${NC}"
else
    echo -e "${GREEN}✓ 找到 Pod: $POD_NAME${NC}"
    kubectl get pod "$POD_NAME" -n "$NAMESPACE"
    echo ""
    
    # 检查 Pod 是否 Running
    POD_STATUS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}')
    if [ "$POD_STATUS" = "Running" ]; then
        echo -e "${GREEN}✓ Pod 状态: Running${NC}"
    else
        echo -e "${RED}✗ Pod 状态: $POD_STATUS${NC}"
    fi
fi
echo ""

# 4. 获取访问地址
echo -e "${YELLOW}[4/5] 获取访问地址${NC}"

# NodePort 访问地址
NODEPORT_SERVICES=$(kubectl get svc -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" -o json 2>/dev/null)
if [ -n "$NODEPORT_SERVICES" ]; then
    NODE_IP=$(minikube ip 2>/dev/null || echo "127.0.0.1")
    echo -e "${BLUE}NodePort 访问地址:${NC}"
    echo "$NODEPORT_SERVICES" | jq -r '.items[] | select(.spec.type=="NodePort") | .spec.ports[] | "  \(.protocol)/\(.port) -> '"$NODE_IP"':\(.nodePort)"' 2>/dev/null || echo "  (无 NodePort Service)"
fi
echo ""

# Ingress 访问地址
if [ -n "$INGRESSES" ]; then
    echo -e "${BLUE}Ingress 访问地址:${NC}"
    kubectl get ingress -n "$NAMESPACE" -l instance-id="$INSTANCE_ID" -o jsonpath='{range .items[*]}{.spec.rules[*].host}{"\n"}{end}' | while read -r host; do
        if [ -n "$host" ]; then
            echo "  http://$host"
        fi
    done
fi
echo ""

# 5. 测试连接
echo -e "${YELLOW}[5/5] 测试连接${NC}"

# 测试 NodePort
if [ -n "$NODEPORT_SERVICES" ]; then
    NODE_IP=$(minikube ip 2>/dev/null || echo "127.0.0.1")
    NODEPORTS=$(echo "$NODEPORT_SERVICES" | jq -r '.items[] | select(.spec.type=="NodePort") | .spec.ports[] | "\(.nodePort)"' 2>/dev/null)
    
    for PORT in $NODEPORTS; do
        echo -e "${BLUE}测试 NodePort $NODE_IP:$PORT${NC}"
        if timeout 2 bash -c "echo > /dev/tcp/$NODE_IP/$PORT" 2>/dev/null; then
            echo -e "${GREEN}✓ 端口 $PORT 可访问${NC}"
        else
            echo -e "${YELLOW}⚠ 端口 $PORT 无响应 (可能服务未监听此端口)${NC}"
        fi
    done
fi
echo ""

# 6. 数据库验证（可选）
echo -e "${YELLOW}[额外] 数据库记录验证${NC}"
echo "可以通过以下 SQL 查询验证数据库记录:"
echo -e "${BLUE}SELECT * FROM instance_network WHERE instance_id = $INSTANCE_ID;${NC}"
echo ""

# 总结
echo -e "${GREEN}=== 验证完成 ===${NC}"
echo ""
echo "验证步骤:"
echo "1. ✓ 检查 K8s Service 资源"
echo "2. ✓ 检查 K8s Ingress 资源"
echo "3. ✓ 检查 Pod 运行状态"
echo "4. ✓ 获取访问地址"
echo "5. ✓ 测试端口连接"
echo ""
echo "如需查看更多详情，可以运行:"
echo "  kubectl describe svc -n $NAMESPACE -l instance-id=$INSTANCE_ID"
echo "  kubectl describe ingress -n $NAMESPACE -l instance-id=$INSTANCE_ID"
echo "  kubectl logs -n $NAMESPACE $POD_NAME"
