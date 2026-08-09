# OCI 与 k3s 靠拢路线图

## 目标

fish-container 是一个教学与作品集性质的手写 Linux runtime。目标不是复制 Docker，而是沿着真实的容器技术栈逐层实现并观察：

```text
kubelet
  -> CRI
  -> k3s embedded containerd
  -> containerd-shim-runc-v2
  -> fish-container
  -> OCI bundle / Linux kernel
```

第一阶段复用 containerd 官方 runc v2 shim，并通过 `BinaryName` 调用 fish-container。containerd 负责 CRI、镜像、snapshot、CNI 和 Pod 编排；fish-container 只负责 OCI bundle 的创建、启动、信号、状态与回收。

这条路径比直接实现 CRI 或自研 shim 更小、更符合分层，也能优先验证 runtime engine 本身。独立的 `containerd-shim-fish-v2` 是后续学习目标，不是首次接入 k3s 的前置条件。

## 边界

| 组件 | 负责内容 |
| --- | --- |
| k3s / kubelet | Pod 调度与 CRI 客户端 |
| containerd CRI plugin | sandbox、镜像、snapshot、CNI、OCI spec 生成 |
| `containerd-shim-runc-v2` | Task API、I/O FIFO、事件、进程监督 |
| fish-container | 执行 OCI bundle、管理 Linux 隔离与容器生命周期 |

接入 k3s 时不再由 fish-container 拉取镜像或创建 overlay snapshot；现有 image 子系统保留用于教学和 standalone 演示。

## 当前差距

当前实现已能消费外部 bundle，并用同步屏障实现真实的 `create` / `start` 分离；距离 OCI 兼容仍有这些主要差距：

- 只消费 process 的部分字段，未完整执行 mounts、credentials、capabilities、rlimits 和 hooks
- OCI spec 声明的 network namespace、masked paths 等配置未完全落到内核
- cgroups 只创建目录，尚未加入进程或应用资源限制
- 已记录 PID、退出码并支持强制回收，但 CLI 参数、进程组信号和恢复语义尚未完全兼容 runc

在这些差距补齐前，项目不能宣称 OCI compliant，也不能安全运行不可信负载。

## 实施阶段

### M0：固定基线

状态：**已完成。** 验收入口为 `make test` 与 `make test-linux-e2e`，CI 在 Linux root 环境重复执行两条路径。

目标：让当前能力可重复验证。

- [x] 只支持 Linux amd64/arm64，按宿主平台选择镜像 manifest
- [x] 增加特权 Linux CI 和端到端测试
- [x] 覆盖 local rootfs 与 OCI image 两条启动路径
- [x] 为所有未实现字段显式报错，避免“spec 写了但运行时忽略”
- [x] 加固 layer 解包：whiteout、UID/GID、xattrs、symlink escape 和特殊文件

验收：在干净 Linux VM 中可重复运行 Alpine，并明确拒绝未支持的 OCI 配置。

### M1：OCI 生命周期

状态：**进行中。** 核心生命周期已经可运行和回归测试；异常恢复与完整 runc 语义仍待收口。

目标：把 bundle 作为 runtime engine 的唯一事实来源。

- [x] `create --bundle <path> --pid-file <path> <id>`
  - 校验 `config.json`
  - 创建 namespaces、rootfs、mounts 和容器 init
  - init 在同步管道上等待，状态保持 `created`
- [x] `start <id>`
  - 释放启动屏障
  - 执行用户进程，状态进入 `running`
- [x] `state <id>` 输出 OCI State JSON，并持久化退出码与退出时间
- [ ] `kill <id> <signal>`：已可靠处理 init，仍需补齐进程组 / `--all` 语义
- [x] `delete [--force] <id>` 回收 mount、namespace、cgroup 和状态
- [ ] `run` 收敛为 `create + start + wait + delete` 的便捷命令
- [x] 状态文件采用原子替换，容器 ID 限制为安全的单路径组件

异常恢复进度：进程提前退出、失效 PID 和 kill/delete 竞态已有确定结果；runtime 重启、幂等 delete 与残留 mount 的系统恢复仍待实现。

验收：`create` 后用户程序尚未执行；`start` 后才执行；状态迁移与退出码稳定可测。

### M2：OCI Linux 配置

目标：让 k3s/containerd 生成的常用 OCI spec 可以真实执行。

按以下顺序实现：

1. `root`、`process.args/env/cwd/user`、hostname
2. mount propagation、bind/proc/sysfs/tmpfs/devpts/cgroup mounts
3. PID、mount、UTS、IPC、network、user namespace，包括加入已有 namespace path
4. UID/GID、additionalGids、rlimits、oomScoreAdj、scheduler
5. capabilities、`noNewPrivileges`、maskedPaths、readonlyPaths
6. cgroups v2：加入进程、resources update、systemd cgroup 兼容
7. sysctl、devices、seccomp、AppArmor/SELinux 标签
8. create/start/poststart/poststop hooks

网络设备、IP 和路由在 k3s 场景由 CNI 管理；fish-container 的职责是正确创建或加入 spec 指定的 network namespace。

验收：containerd 生成的 pause sandbox 和普通容器 spec 均能执行，Pod 内多个容器可以共享目标 namespaces。

### M3：runc CLI 兼容层

目标：由 `containerd-shim-runc-v2` 把 fish-container 当作 OCI runtime binary 调用。

实现 shim 实际需要的全局参数与子命令，并保持 stdout/stderr 和退出码兼容：

- 全局：`--root`、`--log`、`--log-format`、`--systemd-cgroup`
- 核心：`create`、`start`、`state`、`kill`、`delete`
- k3s 所需：`exec`、`update`、`pause`、`resume`、`events`、`ps`
- 探测：`features`、`--version`

命令解析层只做协议适配，生命周期逻辑必须复用 `internal/runtime`，避免 CLI、standalone 和 containerd 三套实现分叉。

验收：使用 containerd 官方 runc v2 shim 和 `ctr`，指定 `BinaryName=/usr/local/bin/fish-container` 后可运行、停止并删除容器。

### M4：接入 k3s

目标：通过 `RuntimeClass` 选择 fish-container，先运行独立测试 Pod，再扩大覆盖面。

以使用 containerd 2.x/config v3 的 k3s 为首个目标版本。在节点安装 runtime：

```text
/usr/local/bin/fish-container
```

扩展 k3s 的 containerd 模板：

```toml
# /var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl
{{ template "base" . }}

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.'fish']
  runtime_type = "io.containerd.runc.v2"

[plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.'fish'.options]
  BinaryName = "/usr/local/bin/fish-container"
  SystemdCgroup = true
```

创建 RuntimeClass：

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: fish
handler: fish
```

测试 Pod 显式选择 runtime：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: fish-smoke
spec:
  runtimeClassName: fish
  restartPolicy: Never
  containers:
    - name: alpine
      image: alpine:latest
      command: ["/bin/sh", "-c", "echo hello-from-fish; sleep 10"]
```

验证顺序：

1. standalone OCI bundle
2. standalone containerd + `ctr`
3. k3s 单容器 Pod
4. `kubectl logs`、`exec`、liveness probe 和资源限制
5. 多容器 Pod、共享 namespaces、volume 和优雅终止

在稳定之前只通过 `RuntimeClass` 选择 fish，不替换节点默认 runtime。

### M5：可观察性

目标：能够从 Kubernetes 请求一直追踪到内核进程。

- 每个操作输出结构化日志：namespace、container ID、PID、state、operation、duration、error
- 记录 OCI 状态迁移和关键 syscall/mount/cgroup 操作
- 保留 containerd task create/start/exit/delete 事件顺序
- 提供只读 `inspect`/`debug` 命令，展示 PID、namespace inode、mounts、cgroup 和 bundle
- 可选 Prometheus 指标：操作耗时、运行容器数、失败数、OOM 和退出码
- 文档化观察入口：`kubectl describe`、`kubectl logs`、k3s/containerd 日志、runtime debug 输出

验收：一次 Pod 启动失败可以通过 container ID 关联 Kubernetes event、containerd task 和 fish-container 日志。

### M6：独立 containerd shim（进阶）

目标：学习并掌控 containerd Runtime v2 的完整适配层。

- 新增 `containerd-shim-fish-v2`
- 实现 ttRPC Task Service、bootstrap、I/O FIFO、Wait 和事件发布
- shim 作为 subreaper，负责进程监督和异常恢复
- runtime type 改为 `io.containerd.fish.v2`

这一阶段不应复制 runtime engine：shim 只负责 containerd 协议、I/O 与监督，OCI 执行仍复用 fish-container engine。

## 兼容性与测试矩阵

每个 milestone 必须维护明确矩阵：

| 层级 | 最小验证 |
| --- | --- |
| OCI engine | Alpine bundle 的 create/start/state/kill/delete |
| Linux 配置 | namespaces、mounts、user、capabilities、cgroups v2 |
| containerd | `ctr` create/run/kill/delete、I/O、exit status |
| CRI/k3s | RuntimeClass Pod、logs、exec、probe、limits、termination |
| 恢复 | runtime/shim/containerd 重启与残留资源清理 |

测试必须运行在独立 Linux VM 或专用 CI runner。嵌套容器中的 overlayfs、cgroups 和 systemd 行为不能作为唯一结论。

## 非目标

在完成 k3s 接入前不优先实现：

- 自有 CRI server
- 自有镜像 registry、snapshotter 或 CNI
- Docker API 兼容
- 跨平台容器
- 生产级多租户安全承诺

## 参考规范

- [OCI Runtime Specification](https://github.com/opencontainers/runtime-spec)
- [runc：OCI runtime CLI 参考实现](https://github.com/opencontainers/runc)
- [containerd Runtime v2](https://github.com/containerd/containerd/blob/main/docs/runtime-v2.md)
- [containerd 多 runtime 配置](https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.toml.5.md#multiple-runtimes)
- [Kubernetes CRI](https://kubernetes.io/docs/concepts/containers/cri/)
- [k3s containerd 配置](https://docs.k3s.io/advanced#configuring-containerd)
