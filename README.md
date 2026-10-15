# Agent OS Telemetry

可独立部署、按需启用的事件导出服务。只拥有 `telemetry_inbox`、`telemetry_exports`，通过 Runtime 的 claim/ack HTTP 协议消费事件，先持久化 inbox 再确认。丢失确认可重投，事件身份变化会拒绝；不读取 Runtime 表，不导出提示词、工具参数、结果或凭证。

## 交付语义与当前状态

导出具有有界批次、超时和退避。远端成功但回复丢失可能产生重复观测，因此是**至少一次投递**。Telemetry 不可用不阻塞 Agent 执行。

当前正式 Windows 部署为零实例，原账号尚无所需 inbox，必须完成本服务迁移后再启用；没有配置 Langfuse 不能宣称导出可用。

`telemetry --execution-access-profile` 在读取配置或连接服务前返回编译的 `telemetry-http-v1`。Windows 维护器在启动、升级、回滚和中断恢复时核对程序、数据库身份及 inbox SELECT/INSERT/UPDATE、exports SELECT/INSERT。单纯 readiness SELECT 不足以证明写权限。缺少声明按旧权限需求处理，格式错误不回退；能力声明本身不执行迁移或启用 Langfuse。

## 构建与部署

需要 Go 1.27.2、PostgreSQL 18；PowerShell 7 可执行 `scripts/Build.ps1`。也可运行 `GOWORK=off go build -mod=vendor ./cmd/telemetry`。固定 SDK 与提交的 `vendor` 不依赖相邻仓库或私有模块令牌。

按 [配置示例](deploy/service.example.json) 设置本域数据库、独立服务令牌和对端哈希（含自身）、`RUNTIME_URL`、`REGISTRY_URL`、`LANGFUSE_URL`。供应商凭证从 Registry 命名解析，使用 `bin/telemetry --config <文件>` 启动。

默认动态回环端口避免多实例冲突，`LISTEN` 可显式设定；Docker 私网端口为 8099。就绪检查只验证本域。多机/容器显式配置地址。

## 迁移、回滚与测试

先创建非所有者应用角色，授予 schema USAGE、禁止 CREATE。单独以 `MIGRATION_DATABASE_URL` 执行 `bin/migrate --role <账号>`，事务仅管理两张本域表与授权；服务启动不做 DDL。按 [发布契约](deploy/release-contract.json) 更新程序并保留 inbox 和导出标记。

运行 `go test -mod=vendor -count=1 ./...`、`go vet -mod=vendor ./...`。`TEST_DATABASE_URL` 启用隔离库/NOLOGIN 角色，验证事务、权限、先入库后确认和丢确认重投；缺少数据库时明确跳过。

`telemetry/export_test.go` 把旧集成仓的并发导出、Runtime 接收失败时排空已落盘 inbox、OTLP 部分拒绝后重试移交到本 Owner：使用隔离 PostgreSQL、本域迁移及受限 SET ROLE，Registry/Runtime/OTLP 均为 HTTP 协议夹具，不能称为独立数据库登录、真实 Langfuse 或跨 Owner 组合验收。旧实现仅截取确认回包前 4096 字节，可能把有效 JSON 前缀误当完整确认；新增 4097 字节反例已在旧实现和真实隔离 PostgreSQL 上失败，证明它会错误写成功标记。修复后拒绝超限回包、保留待导出状态，并仍接受恰好 4096 字节的有效确认。2026-10-15 维护者 macOS 15.3/arm64 的 7 项定向测试（这 5 项及既有 inbox/迁移权限测试）使用真实 PostgreSQL 18.6、Go 1.27.2 与 race 检查全部通过，零跳过；vet/build 通过，证据保存在本机 `local/evidence/macos-telemetry-owner-20261015`。Runtime 接收失败场景顺序调用 receive/export，只证明已落盘数据的独立导出，不代表长期循环活性。旧主仓测试继续保留。

仅手动/reusable 的 `langfuse.yml` 使用真实 Langfuse 4.56.0、隔离 PostgreSQL 和本服务导出器，Runtime/Registry HTTP 为明确的协议夹具；检查接收、回读、隐私字段排除与重投的重复观测。该工作流读取 Go JSON 测试事件，要求指定 live 测试实际通过且零跳过；没有 PR 自动触发。新仓首次实跑仍待统一 Linux 验收，旧集成结果不算新仓通过。Actions 日常关闭。

[历史说明](docs/history/service-notes-20261014.en.md) 保留权限声明检查、发布检查及尚未正式启用的边界。
