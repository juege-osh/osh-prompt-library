# 部署与接手说明

给接手开发 `prompt.osh.lol` 的同事。以下是 2026-09-25 核对过的线上配置；部署前先看一次现状，避免覆盖正在更新的图库数据。

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

页面和检索请求经过 Nginx、Go、SQLite。视频和图片由 Go 返回短时签名链接，浏览器直接向 Cloudflare R2 读取媒体；服务器不转发媒体字节。修改前端、后端时只需部署一个 Go 程序，HTML/CSS/JS 已嵌入其中。服务器当前没有 `go` 命令，**在开发机编译 Linux 程序后上传**。

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

若已经克隆过，先在干净的工作区更新代码：`git pull --ff-only origin master`。`go.mod` 使用纯 Go 的 SQLite 驱动，交叉编译时关闭 CGO。`go test ./...` 主要验证能编译；上线后仍要按下文检查页面、接口和媒体。

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

本次操作不需要改 Nginx、R2 或 SQLite。只改 Nginx 配置时，先在服务器运行 `nginx -t`，通过后再运行 `systemctl reload nginx`；普通代码发布不用重载 Nginx。只需手动重启应用时：

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

再用浏览器看视频卡片播放、图片缩略图，以及“查看并复制提示词”。媒体地址应从站点返回 `302`，最终由 R2 提供；不要把带签名的完整跳转地址贴进日志或工单。接口数量会随着后台图片上传变化，**不要用固定数量判断部署是否成功**。排错时在服务器看 `systemctl status prompt-osh --no-pager -l` 和 `journalctl -u prompt-osh -n 80 --no-pager`，避免贴出含凭证的环境变量。

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

这只回滚程序。若本次还修改了数据库结构、Nginx 或 R2 配置，必须按各自备份单独回滚，不能只换二进制。

## 数据与后台任务

- **代码发布不要上传或覆盖 `catalog.sqlite3`、`images_catalog.sqlite3`。** 它们不在私仓。`catalog.sqlite3` 来自素材项目的已验收视频清单；`images_catalog.sqlite3` 由本机后台任务把 R2 中已核验的图片逐批发布到服务器。图库上传尚在进行，图片数量会增长。
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
