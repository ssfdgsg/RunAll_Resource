-- 创建实例网络配置表
-- 支持两种端口暴露模式：
--   1. TCP/UDP: 通过 ClusterIP Service + ingress-nginx ConfigMap 暴露，使用 external_port
--   2. HTTP: 通过 ClusterIP Service + Ingress 暴露，使用 ingress_name

CREATE TABLE IF NOT EXISTS instance_network (
    instance_id BIGINT NOT NULL,
    port INTEGER NOT NULL,
    service_name VARCHAR(64) NOT NULL,
    service_port INTEGER NOT NULL,
    external_port INTEGER,  -- TCP/UDP 模式的外部端口（ConfigMap key）
    ingress_name VARCHAR(64),  -- HTTP 模式的 Ingress 名称
    protocol VARCHAR(10) NOT NULL DEFAULT 'HTTP',  -- TCP/UDP/HTTP
    access_url TEXT NOT NULL,  -- 最终访问地址
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_id, port)
);

-- 创建索引以提高查询性能
CREATE INDEX IF NOT EXISTS idx_instance_network_instance_id ON instance_network(instance_id);
CREATE INDEX IF NOT EXISTS idx_instance_network_protocol ON instance_network(protocol);
CREATE INDEX IF NOT EXISTS idx_instance_network_enabled ON instance_network(enabled);

-- 添加注释
COMMENT ON TABLE instance_network IS '实例网络端口暴露配置表';
COMMENT ON COLUMN instance_network.instance_id IS '实例ID';
COMMENT ON COLUMN instance_network.port IS '容器端口（targetPort）';
COMMENT ON COLUMN instance_network.service_name IS 'Kubernetes Service 名称';
COMMENT ON COLUMN instance_network.service_port IS 'Service 暴露的端口';
COMMENT ON COLUMN instance_network.external_port IS 'TCP/UDP 模式的外部端口（ingress-nginx ConfigMap key）';
COMMENT ON COLUMN instance_network.ingress_name IS 'HTTP 模式的 Ingress 名称';
COMMENT ON COLUMN instance_network.protocol IS '协议类型：TCP/UDP/HTTP';
COMMENT ON COLUMN instance_network.access_url IS '最终访问地址';
COMMENT ON COLUMN instance_network.enabled IS '是否启用';
