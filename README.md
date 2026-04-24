# autocert

一个用 Go 写的 Let's Encrypt 自动签发/续期程序，走 `DNS-01 + Cloudflare User API Token`，支持：

- 一个证书包含多个域名和通配符域名
- 一份配置管理多个证书任务
- 全局 Cloudflare User API Token，或按证书覆盖 Token
- 按剩余 30 天自动续期，等价于 90 天证书大约第 60 天开始续
- `systemd timer` 或常驻 `daemon` 两种运行方式

## 安装

安装脚本和 `autocert.timer` 放在 [deploy/systemd](/var/home/silver/workspace/agent/autocert/deploy/systemd)。

如果你想把二进制装到 `~/.local/bin`，并使用 `systemd --user` 自动续期，推荐直接运行：

```bash
./deploy/systemd/install-user.sh
```

这个脚本会：

1. 编译并安装 `~/.local/bin/autocert`
2. 把当前工作区的 `config.yaml` 拷贝到 `~/.config/autocert/config.yaml`
3. 把当前工作区的 `.env` 拷贝到 `~/.config/autocert/autocert.env`
4. 创建 `~/.local/share/autocert` 作为默认数据根目录
5. 生成 `~/.config/systemd/user/autocert.service`
6. 安装并启用 `autocert.timer`

安装完成后，请优先检查并按需修改这两个文件：

- `~/.config/autocert/config.yaml`
- `~/.config/autocert/autocert.env`

尤其是首次安装后，通常需要确认 Cloudflare Token 环境变量名、证书域名列表、输出目录和 `reload_command` 是否符合你的实际环境。

补充说明：

1. 安装完成后，运行时默认读取 `~/.config/autocert/config.yaml`，不再依赖项目仓库
2. 如果你的配置里使用了 `./data` 这类相对路径，实际目录会落到 `~/.local/share/autocert/data`
3. `install-user.sh` 会把 `~/.config/autocert`、`~/.local/share/autocert` 等相关目录权限收紧到仅当前用户可访问
4. 如果你后续修改的是仓库里的 `config.yaml` 或 `.env`，需要重新运行一次安装脚本才能同步到部署目录；如果你直接修改 `~/.config/autocert/` 下的文件，则不需要
5. 如果希望用户 timer 在没有登录会话时也继续运行，还需要执行 `sudo loginctl enable-linger $USER`
6. `install-user.sh` 会在安装时自动读取当前 shell 里的 `https_proxy`、`http_proxy`、`all_proxy`、`no_proxy` 及其大写变体；如果这些环境变量没有设置，就不会写入代理配置
7. `install-user.sh` 默认会在 service 启动前等待 30 秒；如果需要调整，可以在执行脚本前设置 `AUTOCERT_START_DELAY_SECONDS`

## 配置

参考 [config.example.yaml](https://github.com/silverling/autocert/blob/main/config.example.yaml)。

- `acme.renew_before: 720h` 表示证书到期前 30 天开始续期，这就是“约第 60 天续期”的实现方式。
- `certificates[].domains` 的第一个域名会作为证书主域名，其余域名会作为 SAN。
- `dns.cloudflare.user_token_env` 是默认 Token 环境变量名，`certificates[].cloudflare` 可以按证书覆盖。
- User API Token 需要至少具备 `Zone:Read` 和 `DNS:Edit` 权限。
- `dns.recursive_nameservers` 可以指定 DNS 传播检查所用的递归解析器，例如 `1.1.1.1`。
- `acme.storage_dir` 和 `certificates[].output_dir` 如果写相对路径，会解析到 `~/.local/share/autocert` 下；如果写绝对路径，则保持原样。

## 运行

安装部署后，可以直接手动触发一次：

```bash
systemctl --user start autocert.service
```

如果你想从源码临时运行，建议先用 Let's Encrypt staging 目录联调，验证通过后再切到生产目录。

```bash
cp config.example.yaml config.yaml
export CLOUDFLARE_USER_TOKEN=...
go build -o bin/autocert ./cmd/autocert
./bin/autocert run --config ./config.yaml
```

如果不显式传 `--config`，程序默认会读取 `~/.config/autocert/config.yaml`。

如果某个证书任务对应另一套 Cloudflare Zone，可以单独设置：

```bash
export CF_USER_API_EXAMPLE_NET_TOKEN=...
```

## 输出目录

每个证书任务会在自己的 `output_dir` 下写出：

- `cert.pem`
- `chain.pem`
- `fullchain.pem`
- `privkey.pem`
- `resource.json`

ACME 账号数据会保存在 `acme.storage_dir/accounts/` 下。
