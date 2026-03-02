-- 诊断查询脚本 - 检查为什么查询没有输出

\echo '========================================='
\echo '数据诊断：检查实例数据'
\echo '========================================='
\echo ''

-- 1. 检查表是否存在
\echo '【1】检查 instance 表是否存在'
SELECT EXISTS (
    SELECT FROM information_schema.tables
    WHERE table_schema = 'public'
    AND table_name = 'instance'
) AS "表是否存在";
\echo ''

-- 2. 检查表中是否有数据
\echo '【2】检查 instance 表总记录数'
SELECT COUNT(*) AS "总记录数" FROM instance;
\echo ''

-- 3. 检查未删除的记录数
\echo '【3】检查未删除的记录数'
SELECT COUNT(*) AS "未删除记录数" FROM instance WHERE deleted_at IS NULL;
\echo ''

-- 4. 检查该用户是否有记录
\echo '【4】检查用户 e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5 的记录'
SELECT COUNT(*) AS "该用户记录数"
FROM instance
WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5';
\echo ''

-- 5. 检查该用户未删除的记录
\echo '【5】检查该用户未删除的记录'
SELECT COUNT(*) AS "该用户未删除记录数"
FROM instance
WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5'
  AND deleted_at IS NULL;
\echo ''

-- 6. 查看 user_id 的数据类型
\echo '【6】检查 user_id 列的数据类型'
SELECT column_name, data_type, character_maximum_length
FROM information_schema.columns
WHERE table_name = 'instance' AND column_name = 'user_id';
\echo ''

-- 7. 查看所有不同的 user_id（前10个）
\echo '【7】查看数据库中存在的 user_id（前10个）'
SELECT DISTINCT user_id, COUNT(*) as "记录数"
FROM instance
WHERE deleted_at IS NULL
GROUP BY user_id
ORDER BY COUNT(*) DESC
LIMIT 10;
\echo ''

-- 8. 查看最近的10条记录（不限用户）
\echo '【8】查看最近的10条记录（所有用户）'
SELECT
    instance_id,
    user_id,
    name,
    status,
    created_at,
    deleted_at
FROM instance
ORDER BY created_at DESC
LIMIT 10;
\echo ''

-- 9. 尝试模糊匹配该用户ID
\echo '【9】尝试模糊匹配用户 ID（部分匹配）'
SELECT
    user_id,
    COUNT(*) as "记录数"
FROM instance
WHERE user_id LIKE '%e58c0ac3%' OR user_id LIKE '%b2c1%'
GROUP BY user_id;
\echo ''

-- 10. 检查是否所有记录都被标记为已删除
\echo '【10】检查该用户的所有记录（包括已删除）'
SELECT
    instance_id,
    name,
    status,
    created_at,
    deleted_at,
    CASE
        WHEN deleted_at IS NULL THEN '未删除'
        ELSE '已删除'
    END as "删除状态"
FROM instance
WHERE user_id = 'e58c0ac3-b2c1-4229-b58d-5dd4657a3ac5'
ORDER BY created_at DESC;
\echo ''

\echo '========================================='
\echo '诊断完成'
\echo '========================================='
\echo ''
\echo '如果以上查询都没有数据，可能的原因：'
\echo '1. 该用户 ID 在数据库中不存在'
\echo '2. user_id 字段类型不匹配（UUID vs String）'
\echo '3. 所有记录都被软删除了（deleted_at 不为 NULL）'
\echo '4. DG 连接的是不同的数据库或服务器'
\echo ''

