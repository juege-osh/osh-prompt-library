# 部署与接手说明

给接手开发 `prompt.osh.lol` 的同事。服务器基础信息于 2026-09-25 核对，本手册于 2026-09-26 按账户/会员/积分版本更新。此次仅推送代码，未部署服务器；部署前先核对现状，避免覆盖正在更新的图库及业务数据。

## 现在怎么运行

| 项目 | 当前值 |
| --- | --- |
| 私有代码仓库 | `github.com/juege-osh/osh-prompt-library`，默认分支 `master` |
| 站点 | `https://prompt.osh.lol/`；图片图库 `/images.html` |
| 服务器 | `43.242.200.72`，SSH 端口 `16328`；请向管理员领取个人登录方式，文档不保存密码 |
| 系统 | Linux `x86_64` |
| 应用目录 | `/opt/prompt-osh/app` |
| 服务 | systemd `prompt-osh.service`，以 `nobody` 用户运行，工作目录为应用目录 |
| 程序 | `/opt/prompt-osh/app/prompt-osh-linux --port 18766`，只监听 `127.0.0.1` |
| 入口 | Nginx 配置 `/www/server/panel/vhost/nginx/prompt.osh.lol.conf`，反代到 `127.0.0.1:18766` |
| 数据 | 同目录 `catalog.sqlite3`（视频）和 `images_catalog.sqlite3`（图片）；均不在 Git 中 |
| R2 | 私有桶 `osh-prompt`；只读凭证由 `/etc/prompt-osh/r2.env` 注入服务，不要查看、复制进仓库或输出到日志 |

页面和检索请求经过 Nginx、Go、SQLite。新版视频和原图先经 Go 校验资源解锁权限，再从私有 R2 流式转发，支持 Range；浏览器不再收到 R2 签名地址。**媒体会使用服务器出口带宽**。修改前端、后端时只需部署一个 Go 程序，HTML/CSS/JS、图标和图表库均已嵌入。服务器原配置没有 `go` 命令，**在开发机编译 Linux 程序后上传**。

## 本版首次升级前必读

1. **新增可写业务数据库。** 推荐路径 `/var/lib/prompt-osh/app.sqlite3`，通过 `--app-db` 指定；不存在时程序会建表、初始化套餐及必要配置。资源索引仍只读。不要上传本地测试库或用空库覆盖服务器已有账户数据。
2. **备份后再迁移。** 已有 `app.sqlite3` 时，用 SQLite `.backup`/`VACUUM INTO` 生成一致性备份，不能只复制正在运行的主文件而忽略 WAL。旧版整数积分会一次性转换为百分之一积分的整数存储单位，标记为 `point_hundredths_v1`。回滚不兼容的旧程序时，必须同时恢复对应业务数据库备份。
3. **运行目录权限。** 服务用户需要业务数据库所在目录的写权限，以创建数据库及 `-wal`/`-shm`；不要给程序二进制或整个应用代码目录开放写权限。可单独预建 `/opt/prompt-osh/app/.media-cache`，仅让服务用户写入压缩预览缓存。
4. **独立 SMTP 配置。** 服务器不会自动获得开发机 `.env` 中的授权码。用 root 管理的 `/etc/prompt-osh/smtp.env` 配置下列变量，权限设为 `0600`，由 systemd 读取；保留原来的 R2 `EnvironmentFile`。
5. **管理员需手动指定。** 先正常注册并验证邮箱，再通过 SQLite 将明确选定用户的 `role_code` 改为 `admin`、`role_level` 改为 `3`。第一个用户不再自动获得管理员权限，不要批量提升所有用户。
6. **支付尚未接入。** 前台充值、会员套餐目前不能实际付款，管理员可手动发放；不可对用户宣称已支持在线支付。

SMTP 配置示例只使用占位值，真实授权码不要进入 Git：

```dotenv
SMTP_HOST=smtp.163.com
SMTP_PORT=465
SMTP_SECURITY=tls
SMTP_USER=your-sender@163.com
SMTP_PASSWORD=your-smtp-authorization-code
SMTP_FROM=your-sender@163.com
```

465 使用隐式 TLS，证书验证不可关闭。2026-09-26 已完成真实邮件发送验证；曾在开发机原网络上发现 SMTP 证书替换，切换网络后恢复正常。部署时仍须从服务器实测，不要通过信任异常证书来绕过错误。

可用 `systemctl edit prompt-osh` 添加如下覆盖配置。应用路径、服务用户和原有 R2 环境文件应先核对；以下不会清除原有 `EnvironmentFile`：

```ini
[Service]
EnvironmentFile=/etc/prompt-osh/smtp.env
StateDirectory=prompt-osh
StateDirectoryMode=0700
ExecStart=
ExecStart=/opt/prompt-osh/app/prompt-osh-linux --port 18766 --app-db /var/lib/prompt-osh/app.sqlite3
```

修改 unit 后需 `systemctl daemon-reload`，随后按常规发布流程重启。若之前在应用目录已有 `app.sqlite3`，应停服并用一致性备份迁入新路径，不能只切换路径而遗失原数据。

**Nginx/CDN 注意事项：** 保留 `Host`、`X-Forwarded-Proto`，不要缓存 `/api/`、受保护媒体或带用户会话的响应；旧版公开媒体缓存应失效处理。鉴权后的原图和视频应返回 `200/206`，不再是 R2 的 `302`。检查媒体下载超时与带宽。

**已知限制：** 当前验证码 IP 限流按 TCP 对端地址计算，未信任 `X-Forwarded-For`。部署在 Nginx 后，所有用户会共享该代理地址每小时 20 次的发送上限；大规模开放注册前需要增加显式可信代理支持。每邮箱 60 秒间隔、每小时 5 次的限制仍独立生效。

## 常规代码更新：约几分钟

下面命令在有仓库读取权限、Go 1.26.4 或更新版本的开发机执行。私仓克隆需要各自的 GitHub 身份验证。把改动先合入 `master`，确认拿到的提交就是要部署的版本。macOS Apple Silicon 也可交叉编译。

```sh
git clone git@github.com:juege-osh/osh-prompt-library.git
cd osh-prompt-library
git rev-parse --short HEAD
go test ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o prompt-osh-linux.next .
file prompt-osh-linux.next
```

若已经克隆过，先在干净的工作区更新代码：`git pull --ff-only origin master`。`go.mod` 使用纯 Go 的 SQLite 驱动，交叉编译时关闭 CGO。`go test ./...` 覆盖账户、邮箱验证码、积分精度、资源扣费和使用统计；上线后仍需检查实际 SMTP、页面和媒体。

Windows PowerShell 编译 Linux 程序：

```powershell
go test ./... -count=1
go vet ./...
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags='-s -w' -o prompt-osh-linux.next .
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

上传新程序，然后在服务器上备份当前程序、替换、重启。以下命令使用当前服务器地址；`scp` 与 `ssh` 会通过你自己的登录方式认证。**不要把密码写在命令、脚本、仓库或聊天记录里。**

```sh
scp -P 16328 prompt-osh-linux.next root@43.242.200.72:/opt/prompt-osh/app/prompt-osh-linux.next
ssh -p 16328 root@43.242.200.72 '
  set -eu
  cd /opt/prompt-osh/app
  test -s prompt-osh-linux.next
  chmod 755 prompt-osh-linux.next
  cp -p prompt-osh-linux "prompt-osh-linux.prev.$(date +%Y%m%d-%H%M%S)"
  mv -f prompt-osh-linux.next prompt-osh-linux
  systemctl restart prompt-osh
  systemctl is-active --quiet prompt-osh
  curl -fsS -o /dev/null http://127.0.0.1:18766/
'
```

首次升级先完成上述业务数据库、SMTP 和缓存策略检查；之后未变更基础配置的常规发布只需替换程序。只改 Nginx 配置时，先在服务器运行 `nginx -t`，通过后再运行 `systemctl reload nginx`。只需手动重启应用时：

```sh
ssh -p 16328 root@43.242.200.72 'systemctl restart prompt-osh && systemctl status prompt-osh --no-pager -l'
```

## 发布后检查

在开发机执行：

```sh
curl -fsSI https://prompt.osh.lol/
curl -fsSI https://prompt.osh.lol/images.html
curl -fsS 'https://prompt.osh.lol/api/cases?page=1' | head -c 300
curl -fsS 'https://prompt.osh.lol/api/images?page=1' | head -c 300
```

再用浏览器核对：游客能看封面和压缩缩略图，不能读取提示词/播放视频/下载原图；注册收到真实验证码并获得 888 充值积分、0 会员额度；购买视频/图片按当前倍率扣费，重复访问不扣费；退出登录后受保护接口返回 401。管理员可免扣费并进入控制台，普通用户直接请求管理员接口返回 403。查看额度进度和使用统计，确认后台日志没有迁移、权限或 SMTP 错误。

受保护媒体由站点返回 `200/206`，不得跳转到签名 R2 地址。接口数量会随着后台图片上传变化，**不要用固定数量判断部署是否成功**。排错时在服务器看 `systemctl status prompt-osh --no-pager -l` 和 `journalctl -u prompt-osh -n 80 --no-pager`，避免贴出含凭证的环境变量。

## 出问题怎么回滚

先列出备份，选本次发布前那一份。不要盲目使用最旧备份。

```sh
ssh -p 16328 root@43.242.200.72 'ls -lt /opt/prompt-osh/app/prompt-osh-linux.prev.* | head'
```

把下面的 `<备份文件名>` 替换成刚才看到的文件名，执行后重新检查网站：

```sh
ssh -p 16328 root@43.242.200.72 '
  set -eu
  cd /opt/prompt-osh/app
  cp -p "<备份文件名>" prompt-osh-linux.rollback-next
  chmod 755 prompt-osh-linux.rollback-next
  mv -f prompt-osh-linux.rollback-next prompt-osh-linux
  systemctl restart prompt-osh
  systemctl is-active --quiet prompt-osh
'
```

这只回滚程序。若目标旧程序使用不同的账户结构或整数积分单位，必须先停服并恢复与旧程序配套的 `app.sqlite3` 备份，再启动；不能让旧程序打开新版数据库。恢复备份会丢失备份之后的业务变更，应先保留当前库并明确接受该影响。Nginx、SMTP/R2 和 systemd 变更也需按各自备份处理。

## 数据与后台任务

- **代码发布不要上传或覆盖 `catalog.sqlite3`、`images_catalog.sqlite3`。** 它们不在私仓。`catalog.sqlite3` 来自素材项目的已验收视频清单；`images_catalog.sqlite3` 由本机后台任务把 R2 中已核验的图片逐批发布到服务器。图库上传尚在进行，图片数量会增长。
- `app.sqlite3` 及其 WAL/SHM 也是非 Git 数据，存储账户、会员、积分批次与流水、会话、验证码、订单、资源解锁和倍率。不要把本地清库或测试脚本用于服务器；本地清理不会随代码发布执行。
- 视频数据重建使用素材项目中的 `00_续作与清单/manifest.json` 和媒体，运行 `python3 prompt-web/build_index.py`。图片源索引使用 `python3 prompt-web/build_images_index.py`。仅有 GitHub 仓库无法重建这两份数据。新增素材要先完成 R2 上传和回读核验，再发布相应索引；不要把只在本地存在的媒体写入线上索引。
- 当前本机图片任务名为 `com.osh.prompt-images-upload` 和 `com.osh.prompt-images-publish`。发布任务会定期替换服务器的图片索引，并重启 `prompt-osh`。在它运行期间部署代码，可能恰好遇到一次额外重启；发布后复查服务即可。需要改数据库结构时，先和任务负责人协调暂停同步并做好数据库备份。
- R2 上传凭证、服务器只读凭证、SSH 登录信息均由管理员单独交接。不要提交 `.env`、`*.sqlite3`、密钥或媒体到 GitHub；`.gitignore` 已排除常见本地产物，但提交前仍应检查 `git diff --cached --name-only`。

## 首次接手时先核对

```sh
ssh -p 16328 root@43.242.200.72 '
  systemctl show prompt-osh -p ActiveState -p ExecStart -p WorkingDirectory -p EnvironmentFiles
  ls -lh /opt/prompt-osh/app/{prompt-osh-linux,catalog.sqlite3,images_catalog.sqlite3}
  nginx -t
'
```

如果服务器地址、目录或服务名已经变更，以上线系统显示的值为准，先更新本文档，再部署。不要把旧 OSH 项目的服务与这个独立站点混用。
