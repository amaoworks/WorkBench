# iOS

使用 Xcode、Swift 和 SwiftUI。登录完成后通过 `TabView` 切换一级页面，使用 `NavigationStack` 进入详情，列表用 `List` 或 `LazyVStack`。适配 Dynamic Type、VoiceOver 和系统深色模式。

使用 `URLSession` 和 `Codable` 接入现有 JSON API。原生会话禁用 Cookie，access token 保存在内存，refresh token 与设备会话标识写入 Keychain。令牌刷新由会话层合并为一次请求。

系统推送使用 APNs，投资终端使用 `WKWebView` 的独立数据存储。用 Xcode Instruments 检查冷启动、列表滚动和终端内存占用。

[客户端路线与接口](../README.md)
