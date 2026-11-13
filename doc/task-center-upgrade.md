# 任务中心升级与恢复

目标版本以 [最终设计](task-center-final-design.md) 为准。`0001`—`0011` 保持不可变，结构新增使用 `0012`；业务转换使用独立账本标记 `0012_task_center_unification_business`。本说明不执行生产操作，也不提供自动备份功能。

## 切换

1. 记录旧程序版本、数据库迁移账本、数据库备份位置及应用文件备份位置。数据库与应用文件应来自同一次停机窗口；必须包括原 `secret.key`、Compose／环境文件和 Git 工作区。在隔离环境验证备份能恢复，且原凭据可用原密钥解密。不要把备份、密钥或连接串加入仓库或构建上下文。
2. 停止入口接收新操作，等待活动 Task 结束，再停止旧控制平面。不要同时运行新旧版本；也不要通过启动旧版本检查升级后的数据库。
3. 沿用数据库、文件卷和密钥启动候选版本。程序先执行增量结构迁移和业务恢复，再在相同 advisory lock 下事务转换清理及两类 CD 规则，最后启动唯一任务中心扫描器。保留原启用意图；interval 到期时最多执行一次当前检查，过期 Cron 跳过。
4. 在恢复入口流量前，使用只读数据库连接运行 [核对 SQL](task-center-upgrade-check.sql)，将报告保存在权限为 `0600` 的验收目录。连接信息通过既有私密 PostgreSQL service／凭据文件提供，不放在命令参数或报告里。例如：

   ```bash
   umask 077
   PGOPTIONS='-c default_transaction_read_only=on' psql 'service=suma' -X -q -A -t \
     -v ON_ERROR_STOP=1 -f doc/task-center-upgrade-check.sql > task-center-upgrade-report.json
   ```

   service 必须指定应用实际 schema／search_path。报告只包含规则数量、业务开关／Webhook／间隔／周期对照、下一次时间和停摆原因，不读取凭据、环境值、规则参数、快照或自由文本错误。
5. `business_conversion_complete` 必须为 true，`missing_or_mismatched` 必须为 0。逐项核对停摆清单；原本关闭的清理／CD 保持关闭，缺授权的清理不得自动授权，缺执行时间的旧镜像规则不得自动恢复。核对原 Task、触发、候选、交付、清理及通知证据仍可按原标识读取，然后恢复入口流量。

## 失败与恢复

- 结构迁移失败时事务回滚并停止启动。不要修改旧迁移或删除账本。
- 业务转换失败或已存在规则与业务配置冲突时，整批业务转换回滚且不登记完成标记；已成功应用的结构迁移仍保留。核对并修复具体配置后，用兼容新结构的修复版本重启，不猜测启用状态或授权。
- 代码恢复只使用已经验证兼容新结构且没有旧派发循环的修复版本。`bea8204` 及重新启动旧 cleanup／CD／project-CD 循环的版本不属于兼容回退版本。当前分支尚未发布，不宣称存在已验证的历史回退发布包。
- 如果需要恢复升级前备份，先停止新实例。数据库与原应用文件／密钥必须一起恢复到相同时间点；实际恢复生产数据需要针对该次恢复的明确授权。本次隔离演练不授权生产删库、覆盖数据或替换密钥。

## 本次隔离演练

普通 PostgreSQL 门禁覆盖空库、`0010`／`0011` 结构升级、四类业务转换、关闭状态与原证据保留、转换冲突整体回滚及账本防重。额外演练使用专用 PostgreSQL 容器中的随机 `suma_test_<随机值>` schema，真实执行 `pg_dump`／`pg_restore`，并归档、移除、恢复该测试夹具的原密钥与 Compose 文件，再执行相同升级及解密检查：

```bash
SUMA_RUN_UPGRADE_SMOKE=1 SUMA_UPGRADE_SMOKE_POSTGRES=suma-tc-pg-YOUR_FIXTURE \
  go -C server test -tags dockersmoke ./internal/taskcenter \
  -run '^TestRealTaskCenterUpgradeBackupRestore$' -count=1 -v
```

该专用容器需带有非空 `suma.task-center.validation` 标签，测试 DSN 的主机必须是它的私有容器 IP；测试拒绝普通数据库／容器和非随机 schema。删除与恢复只作用于此测试创建的 schema 和临时文件。实际结果见 [验证记录](task-center-validation.md)；不表示已经核验生产机器上的备份或执行生产切换。
