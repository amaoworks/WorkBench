# Investment 投资

模块 ID 为 `investment`，显示名称为“投资”，页面为 `/investment`，子页面为 `/investment/positions`（持仓）和 `/investment/orders`（订单）。后端在 [internal/modules/investment](../../internal/modules/investment)，前端在 [web/src/modules/investment](../../web/src/modules/investment)。模块随 Workbench 主程序运行，内部按配置、授权、连接、订阅和历史行情分文件，见[代码导航](../../internal/modules/investment/README.md)。

模块在工作台进程内接入 Charles Schwab Trader API：Go 负责 OAuth、REST、Streamer WebSocket 和后台价格监控，TradingView 终端负责行情、持仓与订单界面。`/investment` 是全屏图表，价格监控在 `/investment/monitor`。行情及提醒均使用真实券商数据。

## 价格监控与预警

在 `/investment/monitor`（旧链接 `/investment?tab=monitor` 会跳转到这里）添加美股或 ETF 代码，选择上涨/下跌和正百分比阈值，最多 100 条规则。涨跌幅为 `(常规时段最新成交价 / 上一交易日收盘价 - 1) × 100%`；阈值支持两位小数，上涨最多 1000%，下跌最多 100%。首次有效检查已满足阈值时也会提醒。尚未授权时可以保存规则，页面显示授权或行情错误。

`investment.monitor_prices` 每分钟在服务端运行，同一标的只查询一次，每批最多 50 个代码，关闭网页不影响检查。进程必须保持运行。交易时段来自 Schwab `markets/equity` 的当天常规时段，日历最多缓存一小时，支持休市、提前收市和美东夏令时；无法确认时段时暂停判断。盘前、盘后和富途夜盘行情当前不参与预警。

查询 `quotes` 的 `quote.closePrice`、`regular.regularMarketLastPrice` 和 `regular.regularMarketTradeTime`。只接受 `assetMainType=EQUITY`、`realtime=true` 的有效正价格；时间必须位于当天常规时段，距检查不超过三分钟且不能显著超前。错误或缺失数据清除本次有效价格并展示原因，不生成提醒。分钟采样可能遗漏短暂越线后回落的波动。

每条规则每个 `America/New_York` 交易日最多触发一次。涨跌两向可以分别建规则；编辑、暂停和重新启用同一规则不会清除当日记录。后台请求期间删除/修改规则、停用投资或变更授权，会阻止迟到结果触发提醒。触发快照、通知和通知事件在同一事务提交，重启后去重仍有效。删除规则会保留历史。

提醒保留在站内；可在「设置 → 通知推送」配置 Telegram，见 [Telegram 通知](../notifications.md)。监控页不重复 Telegram 的启用状态和设置入口，只在已启用的推送最近失败时提示；规则卡片不显示「已用/上限」条数，后台检查空闲时也不显示状态标记，没有规则时显示空状态。页面展示最新价、前收盘价、涨跌幅、行情时间、检查状态和分页触发记录。后台超过三分钟未检查会显示中断。

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/modules/investment/monitor` | 规则、行情快照和后台检查状态 |
| POST | `/api/modules/investment/monitor/rules` | 创建 `{ symbol, direction: "up"或"down", thresholdPercent, enabled }` |
| PUT / DELETE | `/api/modules/investment/monitor/rules/{id}` | 完整更新规则 / 删除规则 |
| GET | `/api/modules/investment/monitor/history?cursor=...` | 每页 50 条历史，返回 `nextCursor` |

## Schwab 连接

在「设置 → 业务模块 → 投资」填写 Developer Portal 的 App Key、App Secret，以及与登记完全一致的 HTTPS 回调地址；路径必须为 `/oauth/schwab`。该 URL 必须能从用户浏览器到达工作台进程，反向代理需实际支持 HTTPS，应用以 HTTP 提供后端并配置对应的 `WORKBENCH_PUBLIC_URL`。部署示例见[部署与发布](../deployment.md)。HTTP 页面不会自动生成可用回调；旧 HTTP 回调也会在发起授权前被拒绝。Secret 读取时不回显。更换 App Key 或回调必须重新填写 Secret；Key、Secret 或回调变化均清除旧令牌和长连接。

保存后点击「登录 Schwab」，并在账户关联确认页点击 Done。公开回调 `/oauth/schwab` 使用有效期十分钟的一次性 state 校验，再由服务端换取并保存令牌。回调独立于工作台会话，因为跨站跳转不携带 `SameSite=Strict` 会话 Cookie。成功页先建立工作台同站点文档，再自动返回 `/investment`，保留手动返回链接；`no-referrer` 避免授权查询参数进入下一跳 Referer。重新授权成功也会关闭旧 Streamer，终端重连后重新读取账户列表。

`investment.schwab_refresh` 每二十分钟检查访问令牌；REST 请求和新连接也会按需刷新。令牌刷新后关闭旧流，浏览器自动重新登录连接。断开操作保留应用配置，清除券商令牌。所有券商访问令牌仅在服务端使用，代理不转发浏览器 Cookie。

刷新时确认令牌失效（`invalid_grant`，或 `invalid_client` 明确指出 refresh token 无效、过期或撤销）会持久保存 `reauthorizationRequired=true`，清除失效令牌并关闭旧连接。投资页、总览卡片及设置页显示「Schwab 授权已失效」和「重新授权」入口；点击后进入现有 OAuth 流程，只有成功回调才恢复已连接状态。终端断开会通知父页立即读取状态，其他页面的状态轮询作为补充。此机制处理实际失效，不发送提前到期提醒。

网络错误、429、5xx 和普通应用认证错误保留原授权，在页面显示可读错误，允许重试；原始 OAuth 错误正文不会回显。REST 返回 401 时仅对该请求使用的访问令牌检查刷新，迟到的旧请求不会改变新授权，也不会自动重放原业务请求。定时任务不再重复尝试已确认失效的刷新令牌。

## 富途夜盘覆盖

Schwab 行情覆盖常规、盘前、盘后和延长时段（约 04:00–20:00 ET），不含美股夜盘（20:00–04:00 ET）。夜盘由本机或 Compose 中的 Futu OpenD 补齐，**只做行情，不接入富途交易**。

在「设置 → 业务模块 → 投资设置」集中管理 Schwab、富途牛牛和夜盘。Linux x86_64 原生部署在保存账号密码并启用富途牛牛后，自动下载、校验并启动 OpenD；页面显示进程状态和行情登录状态，失败可重试。容器部署可使用配套 OpenD。停用富途牛牛会关闭夜盘，重新启用不会自动恢复夜盘。主机、端口和非本机许可由部署配置管理，不在设置页面或账号保存 API 中修改。兼容版本与首次登录验证见 [OpenD 部署说明](https://github.com/amaoworks/WorkBench/blob/main/futu-opend/README.md)。密码和登录摘要不回显。

- Regular / Premarket / Postmarket / Extended：仍只走 Schwab。
- 「行情时段」集成在 TradingView 图表顶部工具栏，通过官方 `headerReady()` / `createDropdown()` 接入，不再单独占据一行。桌面在右侧显示「行情：常规/扩展/夜盘/全天」；窄屏使用简短标题并移至工具栏靠前位置。菜单位置随宽度切换时仅重建按钮，不重建图表；仅提供常规 (RTH)、扩展 (ETH)、夜盘 (Night)、全天 (24H) 四个行情选项，不另加说明菜单项。仅在夜盘覆盖启用且标的支持时显示 Night/24H。原生底部时段按钮隐藏，避免重复入口。
- 扩展行情使用分钟图；从日、周、月线选择扩展时自动切到 5 分钟，扩展时段切换到这些周期也会回到 5 分钟。
- Night 仅提供美股正股行情，K 线只从 OpenD 获取，不与 Schwab 拼接；仅支持 `1/5/15/30` 分钟，其他周期自动回到 5 分钟并短暂提示。
- 全天 (24H) 是同一张连续分钟图：按 `America/New_York` 时间拼接 Schwab 的 04:00–20:00（盘前、常规、盘后）与 Futu 的 20:00–次日 04:00 数据，跨午夜和夏令时按真实时区处理，排序去重，不为休市或无成交时段补造 K 线；因此不代表周末也交易。仅支持 `1/5/15/30` 分钟，其他周期自动回到 5 分钟。只有夜盘覆盖启用且标的是受支持的美股正股时才显示/使用，不能用只有 Schwab 的半天数据替代完整 24H。24H 的 Futu 历史回补失败会明确报错，不静默返回部分时段；Futu 历史额度限制仍适用。
- 夜盘设置可在过去日期使用 `request_history_kline` 做额度兜底（7 天 100 只标的，同标的 7 天内不重复占额），结果写入本地 `investment_futu_bars`。行情时段不改变下单面板中的「交易时段」，也不新增富途交易能力。

OpenD 容器封装在源码仓库根目录 [futu-opend](https://github.com/amaoworks/WorkBench/tree/main/futu-opend)，可整夹移到独立仓库。Release 部署包和二进制包不包含该目录，配套容器部署需额外取得同版本源码中的完整目录；Compose 叠加见[部署与发布](../deployment.md)。工作台以 JSON 明文协议连接（`packetEncAlgo=None`），不要把 OpenD `11111` 暴露到公网。

## 行情与交易

连接后用 iframe 打开 `/investment/terminal`。终端从 Schwab 获取历史与实时行情，读取所选账户的真实多空持仓；终端启动、切换账户或重连时并行读取最近 30 天的全部订单和 30–180 天内 `status=WORKING` 的订单（Schwab 的 `status` 参数一次只能指定一个状态）。随后补读 30–180 天的全部订单（TradingView 打开账户管理器时请求订单历史即触发；未请求时在启动约 3 秒后进行），确保其他非终态订单（如等待条件的单、括号单中的子单）不遗漏。「订单」列表先显示近期订单及较早的 WORKING 订单，「订单历史」等补读完成后返回；补读失败时历史查询返回错误，切换账户或关闭终端时旧查询也不会返回其他账户的数据。后台补读失败每次连接只提示一次，后续轮询重试；成功结果静默并入，不覆盖较新的订单。

之后的十五秒轮询与 ACCT_ACTIVITY 刷新通常读取最近 3 天；订阅确认（ACCT_ACTIVITY 的命令响应和 `SUBSCRIBED` 消息）不是账户变化，不触发刷新。若有更早的非终态订单，则从该订单起读取，最多覆盖 180 天，按订单 ID 合并进已有结果。同一终端内的并发订单读取复用同一请求。Schwab 没有订单游标，单次请求 `maxResults=1000`，达到上限时递归拆分时间范围并按订单 ID 去重；无法完整读取时明确报错，不展示被截断的完整列表。

图表的标的、周期和行情时段按项保存到工作台数据库，刷新、重启工作台或换浏览器后恢复。TradingView 通过 `settings_adapter` 输出的用户设置（图表样式、收藏、交易显示、下单偏好和面板设置等）独立保存到服务端，不写入浏览器本地存储。终端不保存或加载完整图表布局，已添加的指标和画线不会在刷新后自动恢复；旧 `layout` 快照保留在数据库中，终端读取设置时通过 `includeLayout=false` 排除。首次使用默认 AAPL、日线、常规时段；终端网址的 `symbol` 参数优先于保存的标的。夜盘覆盖关闭或标的不支持保存的 Night/24H 时，恢复到常规时段并更新保存状态。图表主题跟随工作台。

无法读取服务端设置时显示重新加载入口；保存失败时显示「重试保存」，其他窗口已保存同一项时显示「加载工作台版本」。各项独立保存，同一页面迟到的请求不会覆盖较新的改动；换浏览器在打开或刷新时恢复。

页面已有窄屏布局，终端使用 TradingView 的触屏自适应界面。Schwab 和 Futu 行情连接根据当前页面生成绝对 `ws://` / `wss://` 地址，保留域名与端口，避免旧版 Safari/WebView 不接受相对 WebSocket 地址而在创建图表前中断。浏览器回归包含真实图表的手机模拟模式，见[脚本说明](../../scripts/README.md#投资账户管理器)。

内部图表启用 TradingView 官方的 `iframe_loading_same_origin`，通过同源代理 `/charting_library/sameorigin.html` 创建子页面，避免依赖 `blob:` iframe 导航。加载提示保留到图表 `chartReady()` 完成，行情 WebSocket 就绪不会提前隐藏它。加载屏显示当前步骤：加载图表库、读取图表设置、初始化图表、查询标的信息、读取 K 线数据、绘制图表。约 20 秒后提示「还在加载」并提供重新加载，图表继续加载；约 90 秒没有步骤进展时显示失败及停留步骤。首个标的查询超时为 45 秒，常规/扩展时段首批 K 线超时为 75 秒。Night/24H 的首批 K 线明确报错时切回常规时段，无响应时由 90 秒无进展计时处理。图表模块加载失败或初始化异常显示重试入口。外层投资页在终端文档没有回报时另行计时：20 秒提示，90 秒显示失败和重新加载入口。终端启动时，并发的持仓读取共用一次 Schwab 请求；同一账户 2 秒内先后发生的持仓读取复用上一次结果，账户事件、轮询或下单后的刷新会提升订单代次并重新读取，设置与自选表加载共用预取的 CSRF 令牌。参见[官方 Featuresets](https://www.tradingview.com/charting-library-docs/latest/customization/Featuresets/#iframe_loading_same_origin)。

`/investment` 只显示图表，铺满顶部导航下方的全部区域，图表上不叠加工作台自己的按钮。券商未连接或需要重新授权时，图表区域居中显示提示和设置/授权入口。旧链接中的 `view` 网址参数会被忽略，`?tab=monitor` 跳转到 `/investment/monitor`。页面铺满、浏览器全屏和新窗口打开暂不提供，见[后续计划](#后续计划)。

账户初始化在 `broker_factory` 返回后的异步任务中等待 `chartReady()`，但最长延后 15 秒，两者先到者开始读取；连接状态通知发生在 TradingView 完成适配器注册后。尚未实现的首屏优化见[后续计划](#后续计划)。持仓与订单的标的列使用数据中的 `symbol` 字段，分别传给券商标的和图表标的参数，避免渲染异常或将订单 ID 当作图表标的。

账户管理器显示末四位账号、净值、现金和购买力，不把完整账号当成金额。持仓方向用「持有 / 卖空」，避免 TradingView 把普通多头译成「做多」。下单保留两个独立字段：TradingView 原生「有效期」提供DAY(当天有效)、GTC(取消前有效)、FOK(立即全部成交，否则取消) 和 IOC(立即成交，否则取消)，FOK/IOC 仅在限价单中显示；「交易时段」提供常规(9:30-16:00 ET)、盘前(7:00-9:25 ET)、盘后(16:05-20:00 ET)、延长时段(7:00-20:00 ET)，分别映射 `NORMAL`、`AM`、`PM`、`SEAMLESS`。新订单默认当天有效、常规时段。延长时段包含盘前、常规和盘后，不含隔夜；时间范围内仍有常规开盘前、收盘后各五分钟的暂停，参见[嘉信时段说明](https://www.schwab.com/stocks/extended-hours-trading)。

按 Schwab `OrderRequest` 的 session 枚举，不提供或提交 `EXTO`；查询可以保留外部平台已有的隔夜订单，修改此类订单提示回 Schwab 操作。`END_OF_WEEK`、`END_OF_MONTH`、`NEXT_END_OF_MONTH` 暂未确认适用范围，不开放下单；`UNKNOWN` 不作为用户选项。适配器校验数量、价格及有效期/时段组合，盘前、盘后和延长时段仅支持 DAY/GTC 股票限价单。改单先读取原订单，并将原时段回填界面，保留原有效期和买卖指令。当前支持股票和股票期权单腿订单，复杂策略及未支持的订单类型（如跟踪止损）提示回 Schwab 修改，避免转换时丢失原订单语义。未实现的反转持仓与附加止盈止损能力不在界面声明为可用。

下单成功使用响应 Location 中的真实订单 ID；成功写入后的状态读取失败只提示刷新错误，不把已成功的交易改报为下单失败。撤单检查上游 HTTP 状态，并通过 REST 查询确认最终状态，不把“接受撤单”直接当成“已取消”。

下单、改单或撤单遇到网络异常、HTTP 408 或 5xx 时，提示结果未确认并要求核对原账户，不自动重试交易。已经提交的交易继续使用提交时的账户，切换账户不取消该写请求，也不将其回读结果注入新账户。

当前交易账户保存到服务端；刷新或换浏览器后使用同一账户，账户已不可用时回落到第一个可用账户并更新偏好。切换账户会清空缓存、取消旧请求并通知 TradingView 重载。异步响应带账户版本校验，旧账户响应不会写入新账户界面。ACCT_ACTIVITY 仅作为刷新信号，不直接解析事件中的订单和数值：收到事件后重新读取当前账户的订单、成交数量/均价和持仓，并每十五秒补充刷新。刷新用逐行更新，不清空账户表。当前订单只含未完成单，已完成、已撤销和已过期进入历史页，按更新时间新到旧排列。

每次启动、切换账户或重连后的第一份订单数据仅建立快照，通过 `orders()` / `ordersHistory()` 提供给 TradingView，不逐条触发 `orderUpdate`。后续刷新只推送新增或内容变化的订单，避免左下角重复弹出历史成交、撤单通知；新订单、成交及撤单仍正常更新。下单/改单/撤单后的 REST 回读同步更新比较基准，下一次轮询不会重复推送同一结果。

历史 K 线按时间排序、去重并排除查询结束边界；日、周、月线时间与 UTC 周期起点对齐。终端分钟线统一支持 `1/5/15/30` 分钟，不提供或请求 10 分钟线；Schwab 仍提供日、周、月线，Night/24H 仍只支持上述分钟周期。从日线切换至分钟线时限制单次回补区间（1 分钟最多 46 天，其余分钟线最多 240 天），避免把过宽的日线视窗一次性请求给短周期接口。历史回补不会覆盖更新的实时 K 线，同一品种周期的多个订阅者只计算一次成交量增量，交给图表的数据使用副本，避免图表修改内部缓存。

Night 视图只读取 Futu 的历史数据；24H 历史并发读取 Schwab 与 Futu，任一请求失败都会报错；读取期间任一行情连接变更都会丢弃旧结果。24H 实时更新按 ET 时段分源，并以独立会话缓存，避免 Night、24H 和普通时段互相污染。

图表、自选表和详情使用同一 Datafeed 提供行情。常规主报价、当日开高低及成交量使用 Schwab；冷启动缺少报价时批量读取 Schwab REST，随后接收 Streamer 更新，迟到的 REST 不覆盖新报价。盘前/盘后报价映射为 TV 的 `rtc/rch/rchp/rtc_time`，涨跌以最近常规时段价格为基准，时间使用源端成交时间。富途夜盘映射为当前库使用的 `ntc/nch/nchp`；夜盘期间，有效的富途夜盘成交价同时作为主报价，主报价的涨跌额和涨跌幅统一按 Schwab 最近常规价格计算，缺少该价格时使用前收盘价，再缺少则留空。当日开高低和成交量仍使用 Schwab。夜盘没有可确认的成交时间，因此不填 `ntc_time`。字段缺失保持缺失，零涨跌保留，无任何有效报价时返回暂无行情数据。自选表和详情单独订阅富途 BASIC 报价，无需依赖夜盘 K 线订阅；取消一个报价监听不会释放仍有用户的 K 线租约。时段状态沿用终端声明的美东时间表，夜盘状态随夜盘开关和工作日边界变化。

当前 TV 的 `symbol-details.a7454a82deebf6dcbfda.js` 在详情状态中遗漏四个夜盘报价字段。库代理按 `chart/tv-quotes-compat.json` 对这一已确认分包补齐字段，未知分包保持原样；不改面板布局。终端采用 `quotes-v2/` 库路径刷新浏览器已有分包，服务端仍复用原始上游资源缓存并兼容 `quotes-v1/` 请求。库升级时用真实 TV 回归确认分包和兼容修复，回归脚本遇到未验证的详情分包会报错。

自选表自动保存到工作台数据库。新增、删除、重命名、标的顺序、分组和当前选中的列表，在刷新、重启工作台或另一台设备打开时恢复。第一次使用时保存图表库的默认列表；之后以工作台保存的内容为准，空列表也会保留。接入使用 [TradingView Watchlist API](https://www.tradingview.com/charting-library-docs/latest/trading_terminal/Watch-List/)。

保存失败时显示「重试保存」。自选表改动直接提交服务端，待确认的内容只保留在页面内存。同一页面连续编辑按序号提交，迟到的旧请求不会覆盖新内容。其他窗口已保存新版本时，本页显示冲突并保留本页内容；点击「加载工作台版本」会放弃本页未保存修改并载入工作台版本。跨设备在打开或刷新时恢复，不做实时协同。

## 长连接恢复

Go 对一条共享 Streamer 串行登录，收到 LOGIN 与账户订阅确认后才通知浏览器 ready。行情服务首次使用 SUBS，之后使用 ADD；HTTP 成功仅在 Schwab 确认订阅后返回。浏览器端把 100 毫秒内各组件（自选表、图表、详情、下单面板）新增的品种合并为每个服务一条 ADD，已在订阅或待确认的品种不重复发送。

上游断开时同时关闭浏览器连接，浏览器三秒后重连。首次 ready 只建立订阅，不重置正在加载的历史 K 线。断线后重新 ready 时恢复所有活动品种订阅、清空行情快照，并调用 TradingView 的 `resetCache` 与各图表的 `resetData` 重载历史，同时重新读取账户状态。历史回补即使不是首次请求也会恢复实时 K 线基准；断线前的历史响应和已关闭 WebSocket 的迟到事件会被丢弃。终端显示连接恢复中的状态提示。慢客户端不静默丢弃订单事件，而是断开后重新获取状态。参见 [TradingView 数据恢复约定](https://www.tradingview.com/charting-library-docs/latest/connecting_data/Datafeed-Issues/#internet-connection-issues)。

配置变更使正在登录中的旧连接失效。最后一个浏览器离开、模块停用或进程关闭时清理连接；停用不删除 Schwab 配置、凭据或总览布局。

## TradingView 资源

当前 `/charting_library/` 从 `https://trading-terminal.tradingview-widget.com/charting_library/` 同源反代，保存图纸和新闻等依赖外部服务的功能关闭。常规及扩展时段行情与交易数据来自 Schwab；单独选中 Night 时，图表 K 线只来自 Futu OpenD；选中 24H 时按 ET 将两路行情合并为一张图。

图表代理按浏览器支持情况统一使用 gzip 或未压缩正文，避免预热与浏览器请求因 br/zstd 协商不同而重复下载。内容哈希文件名的 JavaScript 与样式表成功取回后缓存在内存和数据目录旁的 `chart-library/`，进程重启后可复用。条件请求在本机校验 ETag；上游失败不写入缓存。这些哈希分包对浏览器使用 `Cache-Control: public, max-age=31536000, immutable`，响应不写入会话 Cookie，也不把 Cookie 列入 Vary，避免空闲续期把每次打开变成整库 304 校验。`charting_library.esm.js` 与 `sameorigin.html` 使用最多五分钟的内存缓存（上游要求更短时从其要求），过期后重新取回；浏览器每次打开时向工作台校验，命中内存时无需访问外部入口资源。

工作台启动后在后台加载磁盘缓存，并预取入口列出的首屏分包；不会遍历运行时清单下载数百个非首屏分包。其余分包首次使用时缓存。并发请求共用一次有超时的下载，离开页面会及时停止该页面的等待，但不会取消新页面仍需使用的共用下载。预热失败会记录日志，可在下次请求重试。

内嵌终端脚本用内容 ETag 校验缓存，未变化时返回 304，程序升级后会返回新脚本；终端 HTML 保持 `no-store`。行情的买入/卖出价还提供给图例区快捷下单按钮和侧栏「详情」面板；详情面板大号价格下方的 bid/ask 行与 K 线重复，由 `custom.css`（经 `custom_css_url` 注入）单独隐藏，报价数据本身保留。该规则依赖 TradingView 语义类名的构建哈希，库升级后若失效应从 symbol-details 分包重新提取；`investment-terminal.e2e.cjs` 用真实库校验该行已被隐藏且其余面板内容仍在。数据源就绪回调在下一个任务中执行，遵循 [TradingView 异步回调约定](https://www.tradingview.com/charting-library-docs/latest/connecting_data/Datafeed-API/#asynchronous-callbacks)，无需固定等待一秒。图表就绪后先显示图表，再异步恢复自选表。标的解析和夜盘连接只读取 `/api/modules/investment/overnight` 的本地开关配置，不等待富途设置页的 OpenD 连接、订阅额度和历史额度检查；24H 对历史额度兜底采用严格模式，失败时不返回伪装完整的半天数据。

这个演示资源接入不等同于取得库的部署授权。TradingView FAQ 区分了 Widgets、Advanced Charts 和 Trading Platform；自托管交易功能需使用获得授权的 Trading Platform 包。参见 [官方 FAQ](https://www.tradingview.com/charting-library-docs/latest/resources/Frequently-Asked-Questions/)。

## 接口与资源

| 方法 | 路径 | 用途 |
|---|---|---|
| GET/PUT | `/api/modules/investment/watchlists` | 工作台自选表读取/保存（需要编辑版本、会话和序号） |
| GET/PUT | `/api/modules/investment/preferences` | 投资偏好读取/按项保存；`chart`、`account`、`view` 和 `tv:` 设置键，保存需要编辑版本、会话和序号。`layout` 保留旧版兼容，终端 GET 使用 `?includeLayout=false` 排除旧快照 |
| GET | `/api/modules/investment/schwab` | 应用配置、连接状态、`reauthorizationRequired` 及最近错误 |
| PUT | `/api/modules/investment/schwab` | 保存 `appKey`、`appSecret`、`callbackUrl` |
| POST | `/api/modules/investment/schwab/disconnect` | 清除令牌和连接 |
| GET | `/api/modules/investment/schwab/oauth/login` | 跳转到 Schwab 授权页 |
| GET | `/oauth/schwab` | 公开 OAuth 回调 |
| POST | `/api/modules/investment/schwab/oauth/refresh` | 检查并刷新访问令牌 |
| GET/POST/PUT/DELETE/PATCH | `/api/modules/investment/schwab/trader/v1/*` | Trader REST 代理 |
| GET/POST/PUT/DELETE/PATCH | `/api/modules/investment/schwab/marketdata/v1/*` | Market Data REST 代理 |
| GET | `/api/modules/investment/schwab/trader/ws`、`marketdata/ws` | 浏览器事件连接 |
| POST | `/api/modules/investment/schwab/marketdata/ws/command` | LEVELONE 服务的 ADD 订阅命令 |
| GET/PUT | `/api/modules/investment/futu` | Futu 账号密码、服务开关与连接状态 |
| POST | `/api/modules/investment/futu/disconnect` | 停用 Futu，同时关闭夜盘 |
| GET/PUT | `/api/modules/investment/overnight` | 独立夜盘开关，启用前校验 Futu 依赖 |
| GET | `/api/modules/investment/futu/kline` | 夜盘当前窗口及额度兜底历史；24H 可用 `requireHistory=true` 要求历史回补失败时明确报错 |
| GET | `/api/modules/investment/futu/quote/ws` | 夜盘推送（覆盖关闭时 409，不升级；就绪帧 `ready` 含 `owner` 与 `generation`，54 秒 ping / 60 秒读取超时，断开自动回收该连接订阅引用） |
| POST | `/api/modules/investment/futu/quote/ws/command` | 夜盘 ADD/UNSUB；`resolution` 为空时只订阅 BASIC 报价，否则订阅指定 K 线及 BASIC；浏览器等待 `ready` 后必传 `owner` 与 `generation`，缺 `owner` 返回 409（升级前已打开的终端需刷新页面加载新协议，不再允许匿名引用），缺或旧 `generation`、旧 `owner` 返回 `applied:false` 和当前代次 |
| GET | `/investment/terminal`、`/investment/terminal/*` | 终端 HTML 与适配器 |
| GET/HEAD | `/charting_library/*` | 图表库静态文件代理 |

导航 pageKey 为 `investment.overview`。总览卡片为 `investment.snapshot`（资产快照）、`investment.quotes`（持仓与自选行情）和 `investment.alerts`（提醒与异动），分别读取 `/api/modules/investment/overview/snapshot`、`overview/quotes`、`overview/alerts`。

### 持仓与订单子页面

前端 `investment.module.ts` 通过 `subpages` 注册了三个页面，入口在顶部导航「投资」的下拉菜单中，菜单项包括 图表（`/investment`）、价格监控（`/investment/monitor`）、持仓、订单。TradingView 终端内部结构没有改动。

- `/investment/monitor`（`MonitorPage`）：价格监控规则与触发记录；`/investment?tab=monitor` 重定向到这里，站内通知的 `actionRoute` 仍可使用旧地址。

- `/investment/positions`（`PositionsPage`）：显示账户总值、持仓市值、现金、今日盈亏和持仓盈亏，下面是持仓表，列出代码、数量、均价、现价、市值、今日盈亏、持仓盈亏和占比。有多个账户时可以切换账户。点击代码会在交易终端中打开对应标的。每 30 秒刷新一次。
- `/investment/orders`（`OrdersPage`）：页头不放「打开交易终端」，终端从顶部导航「投资 → 图表」进入。按最近 1、7、30、90 天（`?range=`）和 全部/working/filled/canceled（`?status=`）筛选委托。条件单和组合单会按 `childOrderStrategies` 展开。页面只读，改单和撤单仍在交易终端中操作。
- 两个页面分别读取 `/api/modules/investment/positions` 和 `/api/modules/investment/orders?days=1|7|30|90&status=all|working|filled|canceled&tz=`，使用模块的 Zod schema 校验。`BrokerGate` 处理未配置、读取失败和需要重新授权的状态；页面提供手动刷新、重试和最近更新时间。
- 持仓现价来自行情报价，缺失数值返回 `null` 并显示 `—`；账户标识使用 Schwab 哈希，界面仅显示掩码标签。订单达到 500 条时按时间拆分读取并去重，无法完整读取时返回错误。多腿订单按买卖方向、腿比例和实际成交计算净价，同时展示每腿委托数量、成交数量与均价；成交比例不匹配时不显示组合净价。止损限价单同时显示限价和触发价。
- 子页面不能命名为 `terminal`，因为后端已经占用 `/investment/terminal*`。Vite 同时代理终端资源和 `/api` WebSocket。

### 净值历史

资产快照展示 1 个月、3 个月、1 年或全部净值记录，并支持选择账户。`GET /api/modules/investment/networth?range=1m|3m|1y|all&account=` 返回 USD 日快照；日期按 `America/New_York` 计算。

启动、重新启用业务及每个工作日美东时间 16:15 采集账户净值、现金、持仓市值和当日盈亏，同一账户同一天覆盖更新。仅保存字段完整的账户，部分账户缺值时保留其他账户记录并报告失败。数据存放于 `investment_networth_snapshots`，当前授权可见账户由 `investment_networth_account_scopes` 限定。切换授权后保留历史存储，并按新授权账户范围读取。

曲线在缺记录或账户集合变化时断开，避免将账户范围变动显示为投资收益。每日明细提供对应日期、账户数与净值；尚无记录时显示空态。浏览器回归见[脚本说明](../../scripts/README.md#前端改版回归)。

## 后续计划

- 新窗口打开、页面铺满（隐藏工作台导航）、浏览器全屏等图表窗口操作后续再做；恢复时沿用服务端 `view` 偏好（`expanded`）记住页面铺满，入口位置另定，不遮挡 TradingView 工具栏和时间轴。
- 首屏加载待评估：合并首屏所需的账户摘要读取；预取首个标的的报价与 K 线；订单跨页缓存（按授权代次、账户和范围隔离并失效，首屏未完成订单覆盖全部非终态，`fromEnteredTime` 按下单时间）；服务端记录 `/investment/terminal` 相关接口耗时，便于与 `investment-startup.e2e.cjs` 的浏览器耗时对照；Schwab 订单代理加短时缓存（数秒至十几秒，按授权代次、账户和查询参数隔离，账户活动时失效），并由服务端按非终态状态汇总未完成订单，减少终端启动和补读的请求耗时。

## 数据、验证与参考

模块构造时要求数据库和通知服务。价格监控通过行情接口读取交易时段与报价，Schwab 适配器在提交前保护授权版本检查。Streamer 接收凭据读取、上游地址和日志依赖；Futu 网关接收配置读取、查询、OpenD 服务和日志依赖。配置表为 `investment_schwab` 与 `investment_futu`，夜盘 K 线缓存在 `investment_futu_bars`。升级迁移移除历史演示表 `investment_quotes` 和 `investment_summary`，旧迁移文件保留。SQL 和 sqlc 生成代码均在模块目录。

React 投资页目前不读取偏好的 `view` 项（接口与 schema 保留，供后续页面铺满使用）；图表选择、账户和 TradingView 设置由终端各自校验。删除偏好后的空值保留版本号，后续保存仍参与版本冲突检查。

Go 测试覆盖配置、OAuth、代理、连接确认、断线恢复和配置变更竞态；Node 测试直接导入终端 JS，覆盖交易语义、未确认的交易结果、真实持仓、订单时间分段、跨账户异步响应、订阅重放及历史/实时 K 线衔接。`./scripts/test.sh` 执行 sqlc 漂移检查、Go 测试、React/终端 JS lint、Node 测试与生产构建。模拟服务验证不替代真实券商端到端验收。

实际 TradingView 库的浏览器回归使用虚构的 Schwab 响应，覆盖初始化时序、持仓与订单列渲染、标的跳转、账户切换和重连，以及下单面板的有效期/交易时段名称、可选值和已有订单回填；运行方式见[脚本说明](../../scripts/README.md#投资账户管理器)。终端资源嵌入 Go 程序，修改后需重新编译并启动调试进程才能生效。

参考：[原始适配器 demo](https://github.com/invmy/schwab_API-tradingview_adapter)、[Cloudflare 代理](https://github.com/invmy/CF_schwab-API)、[Schwabdev](https://github.com/tylerebowers/Schwabdev)、[TradingView 账户切换契约](https://www.tradingview.com/charting-library-docs/latest/api/interfaces/Charting_Library.IBrokerConnectionAdapterHost/)。
