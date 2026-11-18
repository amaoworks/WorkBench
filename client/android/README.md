# Android

使用 Android Studio、Kotlin 和 Jetpack Compose。登录完成后通过 Compose 导航切换一级页面和详情，列表用 `LazyColumn`。适配系统字体缩放、TalkBack 和深色模式。

使用 OkHttp、kotlinx.serialization 和协程接入现有 JSON API。原生会话禁用 Cookie，access token 保存在内存，refresh token 与设备会话标识使用 Android Keystore 管理的密钥加密保存。令牌刷新由会话层合并为一次请求。

系统推送使用 FCM，投资终端使用 Android WebView，并验证其 Cookie 会话与原生网络层的隔离。用 Android Studio Profiler 或 Macrobenchmark 检查冷启动和列表滚动。

[客户端路线与接口](../README.md)
