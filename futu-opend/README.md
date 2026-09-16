# 富途牛牛 OpenD

OpenD 提供富途行情网关；夜盘是工作台投资模块的独立功能，当前依赖富途牛牛。

## 原生工作台自动启动

Linux x86_64 原生部署可直接在「设置 → 业务模块 → 投资设置」填写账号密码，勾选「启用富途牛牛」并保存。首次启用由工作台从官方地址下载 OpenD（约 445 MiB），校验 SHA-256 后安装并启动；页面显示安装、启动、行情登录状态。安装或启动失败时可点击「重试启动」。

程序缓存和私有登录数据默认保存在数据库同级的 `futu-opend` 目录；用 `WORKBENCH_FUTU_RUNTIME_DIR` 可修改目录，已有完整安装可用 `WORKBENCH_FUTU_OPEND_BINARY` 指定其中的可执行文件。地址和端口仍由部署配置管理。需 glibc、libstdc++、libgcc、zlib；安装包自带 OpenSSL 等其余库。ARM64、scratch/Alpine 容器及远程 OpenD 使用下方的配套容器部署。

停用富途牛牛或投资模块会停止本机 OpenD；工作台正常关闭和异常退出也会回收其子进程。工作台重启后，仅在投资模块和富途牛牛都已启用时恢复服务。更新账号密码会重启 OpenD；停用富途牛牛还会关闭夜盘，重新启用不会自动恢复夜盘。

自动登录固定使用 **10.9.6908**。其官方安装包明确支持 `login_pwd_md5` 私有配置文件；[10.10 更新日志](https://openapi.futunn.com/futu-api-doc/changelog/changelog.html)已移除公开的配置账密方式。此集成不会自动升级至 10.10，手工指定程序也应使用兼容版本。

## 在工作台管理账号密码

使用当前版本工作台镜像和本目录的配套镜像：

```bash
docker compose -f compose.yaml -f futu-opend/compose.workbench.yaml --env-file .env up --build -d
```

根目录 `.env` 配置工作台版本、HTTPS 地址等，参见根目录 `.env.example`；此模式无需填写 `FUTU_ACCOUNT_ID`、`FUTU_ACCOUNT_PWD_MD5`。

1. 在「设置 → 业务模块 → 投资设置」填写 Futu 账号和登录密码，保存并启用 Futu。主机 `futu-opend`、端口 `11111` 和非本机许可已由 Compose 配置，不在页面填写。
2. 保存后，OpenD 自动加载登录配置。账号或密码变化会重启 OpenD，工作台重新建立行情连接。
3. 在「设置 → 业务模块 → 投资设置」单独启用夜盘行情。

停用 Futu 会关闭夜盘及配套 OpenD；重新启用 Futu 后不会自动恢复夜盘开关。Futu 设置与接口归属投资模块，投资停用时不开放业务接口。

登录由官方 OpenD 完成。首次登录如要求短信/设备验证，使用本机 Telnet 连接 `127.0.0.1:22222`，按 OpenD 提示操作。保存成功不代表已经完成登录，以页面的连接和行情登录状态为准。

工作台将 OpenD 所需的密码 MD5 保存在私有数据库，并通过 `futu_login` 共享卷的 `login.json` 发布。密码和摘要均不回显；密码留空保留，更换账号需重新填写，清除密码会停用 Futu。摘要仍是有效登录凭据，需和数据库备份一起妥善保管。共享目录权限为 `0750`、文件 `0640`，OpenD 通过专用组只读访问。

也可通过 `WORKBENCH_FUTU_CONFIG_DIR`（或 `-futu-config-dir`）选择共享目录模式，让 `manage.py` 使用 `FUTU_LOGIN_CONFIG=<该目录>/login.json`、`FUTU_OPEND_DIR` 和 `FUTU_CONFIG_TEMPLATE`。配置共享目录后，工作台交由配套进程启动 OpenD，不再启动本机子进程。容器首次部署需执行上述 Compose 命令；之后在设置里控制 OpenD 启停，无需让工作台访问 Docker socket。

## 连接地址由部署配置管理

工作台读取 `WORKBENCH_FUTU_OPEND_ADDRESS`（默认 `127.0.0.1:11111`）和 `WORKBENCH_FUTU_ALLOW_NON_LOCAL`（默认 `false`）。原生二进制也可用 `-futu-opend-address`、`-futu-allow-non-local`。配套 Compose 已自动设置服务地址与非本机许可。

设置 API 只接受启用状态、账号、密码及清除密码标记；不再接受主机、端口和非本机许可。旧数据库中的连接字段不再控制连接，升级独立部署时请把旧地址迁入环境变量或启动配置。

## 独立运行 OpenD

在本目录：

```bash
cp .env.example .env
# 填写 FUTU_ACCOUNT_ID 和 FUTU_ACCOUNT_PWD_MD5
docker compose --env-file .env up --build
```

独立模式仍在 OpenD 配置登录信息；工作台的连接地址由部署配置指定。工作台新保存的账号密码需要配套登录管理脚本才能用于 OpenD 登录。首次构建下载官方 Ubuntu 包；程序和登录态保存在原有命名卷中。

API `11111` 使用明文 JSON 协议，不应暴露到公网。配套部署仅向工作台网络开放，Telnet `22222` 仅发布到宿主机 loopback。不接入富途交易。

## 验证

`PYTHONDONTWRITEBYTECODE=1 python3 futu-opend/manage_test.py` 在仓库根目录运行，使用模拟进程验证登录配置更换、XML 转义及停用清理；不连接真实富途账号。

参考：[官方 OpenD 配置参数](https://openapi.futunn.com/futu-api-doc/opend/opend-cmd.html)、[官方运维命令](https://openapi.futunn.com/futu-api-doc/opend/opend-operate.html)。
