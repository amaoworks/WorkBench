# Android

在 Android Studio 打开 `client/android`，安装 Android SDK Platform 36、Build-Tools 36.0.0 和 Platform-Tools。命令行构建可使用 JDK 17，把 Android SDK 路径设为 `ANDROID_HOME`。连接并授权 USB 调试后，从仓库根目录运行：

```sh
cd client/android
./gradlew :app:assembleDebug :app:lintDebug
./gradlew :app:installDebug
"$ANDROID_HOME/platform-tools/adb" shell am start -n com.amaoworks.workbench/.MainActivity
```

Debug APK 位于 `app/build/outputs/apk/debug/app-debug.apk`。应用 ID 为 `com.amaoworks.workbench`，最低支持 Android 8.0（API 26），目标版本为 Android 16（API 36）。

## 待办

- [ ] **服务器与会话**。设置 HTTPS 服务器地址并检查 `/health/ready`，使用启用密码模式且证书受手机信任的 Workbench 实例。使用 OkHttp、kotlinx.serialization 和协程接入 JSON API，实现登录、首次改密、刷新、退出。原生客户端禁用 Cookie，access token 保存在内存，refresh token 和设备会话标识使用 Android Keystore 管理的密钥加密保存。合并并发刷新，自动重放限于幂等读取。
- [ ] **首版页面**。登录后用 Compose 导航组织总览、待办、通知和个人设置。完成待办增删改查、创建幂等与编辑冲突处理、通知已读状态、个人主题。列表使用 `LazyColumn`。
- [ ] **首版验收与发布**。在网页和真机交叉验证待办同步，覆盖会话过期、并发刷新、断网、账号切换、系统字号、TalkBack 和深色模式。用 Android Studio Profiler 或 Macrobenchmark 检查冷启动和列表滚动。发布前确认目标 SDK，准备应用图标、签名和安装包。
- [ ] **实时通知与推送**。接入前台通知 SSE 和 FCM，验证重连、推送 token 更新和删除、通知跳转。
- [ ] **扩展业务**。接入资讯和 AI 对话。使用 Android WebView 加载投资终端，验证与原生网络层的 Cookie 隔离、行情重连和 Schwab 浏览器往返。

[客户端路线与接口](../README.md) · [移动端接入](../../doc/mobile.md) · [移动端 OpenAPI](../../doc/mobile.openapi.yaml)
