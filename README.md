# fish-container

<p align="center">
  <img src="docs/assets/fish-container.svg"
       alt="fish-container logo and supported features"
       width="960">
</p>

一个用 Go 手写的 Linux 容器运行时，用于学习 OCI Runtime、Linux namespace、cgroups 和容器生命周期。

项目以**可读、可观察、可验证**为目标，不替代 Docker 或 runc。最终目标是作为自定义 OCI runtime 接入 containerd/k3s，并完整观察 Pod 从创建到退出的运行过程。

> 当前处于实验阶段，仅适合受控的 Linux 学习环境，请勿运行不可信镜像或生产负载。

## 已实现

- 拉取并校验 OCI/Docker 镜像 manifest、config 和 layers
- 内容寻址存储、layer 解包和 overlayfs rootfs
- 生成 OCI bundle 与 `config.json`
- PID、UTS、IPC、mount namespace 和 `pivot_root`
- 带真实启动屏障的 `create` / `start` 生命周期
- `state`、`ps`、`kill`、`delete --force` 与原子状态持久化
- 外部 OCI bundle、PID 文件与初步 cgroups v2 支持

尚未完整支持 OCI mounts、用户与 capabilities、seccomp、网络、`exec` 和 rootless。详见 [OCI 与 k3s 路线图](docs/architecture/oci-k3s-roadmap.md)。

## 定位

fish-container 既可以独立拉取镜像并创建 rootfs，也可以在接入 k3s 后消费 containerd 生成的 OCI bundle，专注于 Linux 隔离与容器进程生命周期。

## 快速体验

要求：Linux、Go 1.22+、root 权限，以及支持 overlayfs、namespaces 和 cgroups v2 的内核。

```bash
make build

sudo ./bin/fish-container pull alpine:latest
sudo ./bin/fish-container run \
  --image alpine:latest \
  --container demo \
  --keep \
  /bin/echo hello-from-fish

sudo ./bin/fish-container state demo
sudo ./bin/fish-container delete demo
```

观察严格的 `created -> running -> stopped`：

```bash
sudo ./bin/fish-container create --image alpine:latest --container observer \
  /bin/sh -c 'echo started; sleep 30'
sudo ./bin/fish-container state observer
sudo ./bin/fish-container start -d observer
sudo ./bin/fish-container kill observer
sudo ./bin/fish-container delete observer
```

foreground `run` 默认在退出后自动删除容器；需要继续执行 `state` 或检查文件时使用 `--keep`。`run -d` 始终保留容器，等待显式删除。

`kill <id>` 默认发送 `SIGKILL`，避免 Linux PID namespace 中的 PID 1 忽略默认终止信号；优雅终止可显式执行 `kill <id> TERM`，向整个容器进程组发送信号可增加 `--all`。

## 验证

```bash
make test
make test-linux-e2e
```

E2E 在 root Linux 环境验证 local rootfs 与 OCI image 两条完整生命周期；测试脚本使用 Docker 生成一次性的本地 rootfs。

## 路线

1. 使现有 engine 严格执行 OCI bundle 和生命周期语义
2. 兼容 `containerd-shim-runc-v2` 所需的 OCI runtime CLI
3. 通过 containerd `BinaryName` 与 Kubernetes `RuntimeClass` 接入 k3s
4. 补齐结构化日志、生命周期事件和运行指标
5. 进阶实现独立的 `containerd-shim-fish-v2`

## 文档

- [OCI 与 k3s 靠拢路线图](docs/architecture/oci-k3s-roadmap.md)
- [项目骨架](docs/architecture/stage1-skeleton.md)
- [镜像存储布局](docs/architecture/stage3-image-layout.md)

## License

[MIT](LICENSE)
