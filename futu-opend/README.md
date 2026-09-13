# Futu OpenD 容器

本目录是独立的 OpenD 打包，之后可以整夹移到单独仓库。它只跑富途网关，不含交易逻辑，也不含工作台代码。

工作台通过 TCP `11111` 拉行情。登录发生在 OpenD 里，工作台不保存牛牛密码。

## 构建并单独运行

```bash
cp .env.example .env
# 填写 FUTU_ACCOUNT_ID 和 FUTU_ACCOUNT_PWD_MD5
# MD5: printf '%s' '密码' | md5sum | awk '{print $1}'
docker compose --env-file .env up --build
```

首次启动会下载官方 Ubuntu 包（约数百 MB），并写入命名卷。登录态也在卷里，减少重复短信验证。

首次登录若要求短信/设备验证，把本机 telnet 接到 `127.0.0.1:22222`，按 OpenD 提示输入验证码。

## 和工作台一起跑

在工作台仓库根目录：

```bash
docker compose -f compose.yaml -f futu-opend/compose.workbench.yaml --env-file futu-opend/.env up --build
```

两个服务在同一 Compose 网络。投资设置里填写：

- 主机：`futu-opend`
- 端口：`11111`
- 允许非本机地址：打开
- 启用夜盘覆盖：打开

API 端口不对外发布，只给工作台容器用。Telnet `22222` 绑在宿主机 loopback，方便首次验证。

## 说明

- 镜像默认监听 `0.0.0.0:11111`，**不配置 RSA**。工作台以明文 JSON 协议连接（`packetEncAlgo=None`）。不要把 `11111` 暴露到公网。
- 行情接口不要求交易私钥；不要在本镜像里打开交易。
- OpenD 是富途官方二进制，本目录只做下载、配置和容器封装。
