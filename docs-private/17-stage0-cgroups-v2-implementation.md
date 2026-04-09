# 17 - Stage0 实施记录：cgroups v2 接线与 OCI 对齐

日期：2026-04-16
关联计划：`docs-private/16-cgroups-v2-multistage-introduction-plan.md`
阶段状态：Stage0 已完成（代码 + 测试）

---

## 1. 本阶段目标

Stage0 目标是“最小侵入接线”，不引入资源限制参数，仅完成：
1. cgroups v2 能力探测
2. 容器 cgroup 目录创建与清理
3. OCI spec 中 `Linux.CgroupsPath` 字段对齐
4. 在 lifecycle create/delete 中闭环接入

---

## 2. 实际改动清单

### 新增文件
- `internal/cgroups/manager_linux.go`
- `internal/cgroups/manager_stub.go`
- `internal/cgroups/manager_linux_test.go`

### 修改文件
- `internal/cli/lifecycle.go`
- `internal/runtime/oci_bundle_linux.go`
- `internal/store/container_config_store.go`

---

## 3. 关键实现点

## 3.1 cgroups manager（Linux）

新增 `NewManager()`，由环境变量驱动是否启用 Stage0：
- `FC_ENABLE_CGROUPS_V2`：启用开关（1/true/yes/on）
- `FC_CGROUPS_V2_ROOT`：v2 挂载根目录（默认 `/sys/fs/cgroup`）
- `FC_CGROUPS_V2_PREFIX`：容器 cgroup 前缀（默认 `fish-container`）

实现行为：
- `Apply(ctx, containerID)`：
  - 校验容器 ID
  - 校验 `cgroup.controllers` 存在（确认 unified hierarchy）
  - 创建 `<root>/<prefix>/<containerID>` 目录
- `Delete(ctx, containerID)`：删除上述目录

非 Linux 平台使用 no-op stub，实现保持编译兼容。

## 3.2 OCI 对齐：Linux.CgroupsPath

在 `store.ContainerConfig` 增加：
- `CgroupsPath string`

在 `runtime.buildSpec` 中写入：
- `spec.Linux.CgroupsPath = cfg.CgroupsPath`

当 Stage0 开关启用时，create 流程会计算并落盘 OCI cgroup path（形如 `/fish-container/<id>`）。

## 3.3 lifecycle 接线

在 `createContainer` 中：
1. 先解析 OCI cgroup path（启用时）
2. 执行 `cgroups.NewManager().Apply(...)`
3. 将 `CgroupsPath` 写入 `ContainerConfig`
4. 后续任一步失败（config/bundle 失败）时执行 cgroup 清理回滚

在 `deleteCommand` 中：
- 原有容器目录与 runtime 状态清理后，补充 `cgroups.NewManager().Delete(...)`

---

## 4. OCI 一致性说明

本阶段与 OCI runtime-spec 的一致性体现在：
1. 通过 `Linux.CgroupsPath` 明确声明容器所属 cgroup 路径。
2. 该路径来自统一策略（prefix + containerID），并与 manager 创建目录一致。
3. 配置落盘与 spec 生成链路一致，不出现“配置有值但 spec 丢失”的分叉。

注意：
- Stage0 不做资源值写入（如 memory.max/cpu.max/pids.max），仅做路径治理与生命周期接线。
- 资源限制值写入属于 Stage1。

---

## 5. 验证结果

已通过全量测试：
- `go test ./...`

新增测试覆盖：
- 启用状态下 Apply/Delete 行为
- 启用/禁用状态下 OCI path 解析行为

---

## 6. 回滚与兼容性

- 默认不开启：`FC_ENABLE_CGROUPS_V2` 未设置时行为与旧版本一致。
- 可快速回滚：关闭环境变量即退回无 cgroup 接线模式。
- 非 Linux 平台自动 no-op，不影响编译。

---

## 7. 下一步（Stage1）

下一阶段建议按以下顺序推进：
1. 为 `create/run` 增加 `--memory` / `--cpu-max` / `--pids-max`
2. 把资源字段扩展到 `ContainerConfig`
3. 在 cgroup 目录中写入资源文件
4. 在 start 流程把 PID 写入 `cgroup.procs`

至此才形成“真正生效的资源限制闭环”。
