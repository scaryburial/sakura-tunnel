# 常见问题

## 客户端一直 `client exit success` 然后重连

服务端开了 `auth.additionalScopes` 但客户端没开（或反过来）。两边保持一致，最简单的做法是服务端留空：

```toml
auth.additionalScopes = []
```

## `login rejected for user [alice]: invalid token`

- token 抄错（注意 `sk_` 前缀）；
- 用户在面板里被「重置密钥」，旧 token 已失效；
- 客户端 `user` 与服务端用户库里的名字不一致（大小写敏感）。

## `timeout trying to get work connection` / `invalid work connection`

同样是 `additionalScopes` 不一致导致的签名缺失，见上。

## 面板登录后所有接口 401

浏览器没保存 Basic 凭据。刷新页面重新登录；或检查 `webServer.user/password`。

## 一键接入脚本下载 404

- `clients/` 目录里没有对应架构的二进制；
- 或 Release 里没有该文件名（必须是 `sakura-linux-amd64` 这种命名）。

验证：

```bash
curl -I http://服务器:7500/dl/sakura-linux-amd64
```

## 隧道通了但速度很慢

两个限额取小值：

- 用户限速（面板里 `限速 (Kbps)`）
- 隧道自己声明的 `transport.bandwidthLimit`（且 `bandwidthLimitMode = "server"` 才在服务端生效）

`1024 Kbps ≈ 128 KB/s`。

## 想限制用户只能申请某些端口

```toml
allowPorts = [
  { start = 6000, end = 6999 }
]
```

超出范围的代理会注册失败，客户端能收到原因。

## UPX 版被杀软报毒

UPX 壳的 Go 程序常被误报。用未压缩版（体积 20 MB）即可；或自己重新用 `garble` 混淆。

## 改了产品名，面板还是「樱隧」

面板文案在 `pkg/webui/dist/index.html`，产品名常量在 `pkg/brand/brand.go`；改完必须重新 `go build`（静态资源是 `//go:embed` 编进二进制的）。

## Windows 服务端怎么重启

```powershell
Stop-ScheduledTask  -TaskName SakuraTunnel
Start-ScheduledTask -TaskName SakuraTunnel
Get-ScheduledTaskInfo -TaskName SakuraTunnel
```

## 数据在哪、怎么备份

服务端工作目录下的：

- `users.json` —— 用户库（含 token、配额、累计流量）
- `sakura.log` —— 日志

备份这两个就够；`users.json` 是原子写，热备份也安全。
