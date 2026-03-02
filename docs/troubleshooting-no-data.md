# 数据库查询无输出 - 故障排查指南

## 问题现象
你在 psql 中执行查询没有输出，但 DG (DataGrip/DBeaver 等工具) 中有数据。

## 可能原因分析

### 1️⃣ 连接到了不同的数据库

**检查方法：**
```sql
-- 在 psql 中执行
SELECT current_database();
```

**解决方案：**
- 确保 psql 和 DG 连接的是同一个数据库
- 检查 DG 的连接配置，确认数据库名称

---

### 2️⃣ 配置文件连接的数据库不同

**你的配置文件：**
```yaml
# configs/config.yaml
data:
  database:
    source: postgresql://postgres:123456@localhost:5433/resource
```

**但实际连接的是：**
```
psql -h localhost -p 5432 -U postgres -d resource
```

**注意：端口不同！**
- 配置文件：`5433`
- 实际连接：`5432`

这可能是两个不同的 PostgreSQL 实例！

---

### 3️⃣ 数据在不同端口的数据库中

**立即检查：**

```bash
# 连接到端口 5433 的数据库
export PGPASSWORD=123456
psql -h localhost -p 5433 -U postgres -d resource

# 执行查询
SELECT COUNT(*) FROM instance WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5' AND deleted_at IS NULL;
```

---

### 4️⃣ 权限或角色问题

**检查当前用户：**
```sql
SELECT current_user, session_user;
```

---

### 5️⃣ user_id 类型或格式问题

**检查数据类型：**
```sql
-- 查看列的数据类型
\d instance

-- 或
SELECT column_name, data_type 
FROM information_schema.columns 
WHERE table_name = 'instance' AND column_name = 'user_id';
```

**如果是 UUID 类型：**
```sql
-- 可能需要显式转换
SELECT * FROM instance 
WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5'::uuid 
  AND deleted_at IS NULL;
```

---

## 🔍 立即执行的诊断步骤

### 步骤 1: 运行诊断脚本

在服务器上执行：
```bash
cd /root/runAll/resource
export PGPASSWORD=123456

# 先检查端口 5432
psql -h localhost -p 5432 -U postgres -d resource -f diagnose-data.sql

# 再检查端口 5433
psql -h localhost -p 5433 -U postgres -d resource -f diagnose-data.sql
```

### 步骤 2: 手动检查

在 psql 中执行：

```sql
-- 1. 确认当前数据库
SELECT current_database();

-- 2. 检查表中有多少数据
SELECT COUNT(*) FROM instance;

-- 3. 查看所有 user_id
SELECT DISTINCT user_id FROM instance LIMIT 10;

-- 4. 查看最近的记录
SELECT * FROM instance ORDER BY created_at DESC LIMIT 5;

-- 5. 检查该用户的记录（包括已删除）
SELECT instance_id, name, status, deleted_at 
FROM instance 
WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5';
```

---

## 🎯 最可能的原因

根据你的情况，**最可能的原因是：**

### **配置文件使用端口 5433，但你连接的是端口 5432**

你的程序可能连接到 `localhost:5433` 的数据库，而那里有数据。
但你用 psql 连接的是 `localhost:5432`，这是一个空的或不同的数据库实例。

### 解决方案：

#### 方案 A: 连接到正确的端口（5433）

```bash
# 更新配置文件使用的密码
export PGPASSWORD=123456

# 连接到端口 5433
psql -h localhost -p 5433 -U postgres -d resource
```

#### 方案 B: 检查 Docker 容器

```bash
# 查看所有 PostgreSQL 容器
docker ps -a | grep postgres

# 可能有两个容器：
# - 一个在 5432 端口
# - 一个在 5433 端口

# 进入正确的容器
docker exec -it <container-name> psql -U postgres -d resource
```

#### 方案 C: 检查 DG 的连接配置

在 DataGrip/DBeaver 中：
1. 右键点击连接 → Properties
2. 查看 **Host** 和 **Port**
3. 确认是 `localhost:5432` 还是 `localhost:5433`
4. 使用相同的配置连接 psql

---

## 📋 对比检查清单

| 项目 | psql | DG/配置文件 | 是否一致 |
|------|------|-------------|---------|
| Host | localhost | localhost | ✅ |
| Port | 5432 | 5433 | ❌ |
| User | postgres | postgres | ✅ |
| Database | resource | resource | ✅ |
| Password | (输入的) | 123456 | ？ |

**关键问题：端口不一致！**

---

## ✅ 快速验证

执行这个命令来快速验证：

```bash
# 检查两个端口的数据
echo "=== 端口 5432 ===" && \
PGPASSWORD=123456 psql -h localhost -p 5432 -U postgres -d resource -c "SELECT COUNT(*) as port_5432_count FROM instance;" && \
echo "=== 端口 5433 ===" && \
PGPASSWORD=123456 psql -h localhost -p 5433 -U postgres -d resource -c "SELECT COUNT(*) as port_5433_count FROM instance;"
```

如果端口 5433 有数据而 5432 没有，那就确认了问题！

---

## 🔧 修复配置

更新你的配置文件，使用正确的端口：

```yaml
# configs/config.yaml
data:
  database:
    # 如果数据在 5432 端口
    source: postgresql://postgres:你的密码@localhost:5432/resource
    
    # 或者保持 5433（如果那才是正确的）
    source: postgresql://postgres:123456@localhost:5433/resource
```

---

## 💡 建议

1. **统一端口配置**：决定使用 5432 还是 5433，并在所有地方统一
2. **检查密码**：确保配置文件中的密码正确
3. **使用诊断脚本**：运行 `diagnose-data.sql` 查看详细信息
4. **记录连接信息**：在文档中记录正确的连接参数

需要我帮你进一步诊断吗？

