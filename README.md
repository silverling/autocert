# autocert

一个用 Go 写的 Let's Encrypt 自动签发/续期程序，走 `DNS-01 + Cloudflare User API Token`，支持：

- 一个证书包含多个域名和通配符域名
- 一份配置管理多个证书任务
- 全局 Cloudflare User API Token，或按证书覆盖 Token
- 按剩余 30 天自动续期，等价于 90 天证书大约第 60 天开始续
- `systemd timer` 或常驻 `daemon` 两种运行方式

## 配置

参考 [config.example.yaml](/var/home/silver/workspace/agent/autocert/config.example.yaml)。

- `acme.renew_before: 720h` 表示证书到期前 30 天开始续期，这就是“约第 60 天续期”的实现方式。
- `certificates[].domains` 的第一个域名会作为证书主域名，其余域名会作为 SAN。
- `dns.cloudflare.user_token_env` 是默认 Token 环境变量名，`certificates[].cloudflare` 可以按证书覆盖。
- User API Token 需要至少具备 `Zone:Read` 和 `DNS:Edit` 权限。
- `dns.recursive_nameservers` 可以指定 DNS 传播检查所用的递归解析器，例如 `1.1.1.1`。

## 运行

建议先用 Let's Encrypt staging 目录联调，验证通过后再切到生产目录。

```bash
cp config.example.yaml config.yaml
export CLOUDFLARE_USER_TOKEN=...
go build -o bin/autocert ./cmd/autocert
./bin/autocert run --config ./config.yaml
```

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

## systemd

安装脚本和 `autocert.timer` 放在 [deploy/systemd](/var/home/silver/workspace/agent/autocert/deploy/systemd)。

如果你想把二进制装到 `~/.local/bin`，并使用 `systemd --user` 自动续期，推荐直接运行：

```bash
./deploy/systemd/install-user.sh
```

这个脚本会：

1. 编译并安装 `~/.local/bin/autocert`
2. 把当前工作区的 `.env` 拷贝到 `~/.config/autocert/autocert.env`
3. 生成 `~/.config/systemd/user/autocert.service`
4. 安装并启用 `autocert.timer`

补充说明：

1. 这个用户级 service 仍然会读取当前工作区里的 `config.yaml`
2. 如果你移动了仓库路径，需要重新运行一次安装脚本
3. 如果希望用户 timer 在没有登录会话时也继续运行，还需要执行 `sudo loginctl enable-linger $USER`
4. `install-user.sh` 默认会给 service 设置 `https_proxy/http_proxy=http://192.168.9.3:7897`，并在启动前等待 30 秒
