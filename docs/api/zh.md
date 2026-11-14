# RosPanel REST API

**交互式参考：** [Swagger UI](reference.md) — 所有端点，使用同一份 OpenAPI 文档渲染。


外部 REST API 让周边系统（计费服务、Telegram 商店、开通脚本）通过 HTTP 用 API 密钥管理面板。它调用
的是管理员面板所用的同一套内部逻辑，因此两者不会出现偏差。

## 启用 API

打开面板 → **设置 → API**。创建第一个密钥即会启用该接口，并生成一个稳定、无法猜测的基础 URL：

```
https://<your-host>/<api_path>/v1
```

`<api_path>` 段与隐藏的面板路径相互独立，因此轮换面板密钥永远不会破坏集成。你可以从同一页面轮换或
停用该 API 路径（轮换会改变基础 URL；密钥在新地址下继续有效）。

## 交互式文档与机器可读规范

API 会发布自己的 OpenAPI 3.0 规范，由服务器代码生成（schema 从实际的 Go 类型反射而来，因此不会
偏差）：

```
GET $BASE/openapi.json    → OpenAPI 3.0 文档
GET $BASE/docs            → Swagger UI（可在浏览器中试用各端点）
```

两者都不需要密钥即可访问（基础 URL 本身就是秘密）。打开 `…/docs`，点击 **Authorize**，粘贴密钥，
即可在线调用任意端点。把 Postman / `openapi-generator` / 任意客户端生成器指向 `…/openapi.json`，
即可生成带类型的客户端。（Swagger UI 外壳从 CDN 加载；它渲染的规范完全来自本地。）

## 认证

每个请求都必须携带密钥，密钥在 **设置 → API** 中创建。原始密钥只在创建时显示**一次**——请立即
保存；之后只保留其前缀。以 bearer token 的形式发送：

```
Authorization: Bearer rp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

（也接受 `X-API-Key: <key>`。）已撤销的密钥立即失效。

缺少密钥或密钥无效返回 `401`。该接口按 IP 限流。

密钥在创建时可以拥有**完全访问权限**，或只拥有**为它勾选的 API 方法**（`GET /v1/users`、
`POST /v1/signup` 等），之后可修改。它只能调用这些方法；其他任何请求返回 `403` 并带有
`"code": "forbidden"`，MCP 端点也只列出这些工具。
没有人能勾选自己无权调用的方法，只有所有者才能授予完全访问权限（以及备份）。在方法可以勾选之前
创建的密钥，会一直访问其权限所开放的范围，直到被编辑。
`PATCH /v1/settings` 和 `PATCH /v1/nodes/{id}` 会按字段所属的区块校验每个字段（DNS 和
出口需要 `routing.manage`，受信任网络和探测设置需要 `security.manage`）。

完全访问权限可以触达面板中只有所有者本人才能触达的部分——包括备份（`/v1/backup`）和
本地备份计划——因此只有所有者能够创建或撤销这样的密钥。

## 响应信封

成功：

```json
{ "data": { ... } }
```

错误：

```json
{ "error": { "code": "bad_request", "message": "name is required" } }
```

常见代码：`bad_request`（400）、`unauthorized`（401）、`not_found`（404）、
`unsupported_media_type`（415）、`internal`（500）。

面板拒绝的输入会额外携带两个字段——拒绝的原因，以及该原因的参数：

```json
{ "error": {
    "code": "bad_request",
    "key": "err.planHasUsers",
    "message": "the plan is assigned to 12 users — move them to another plan first",
    "args": { "count": 12 }
} }
```

`code` 是用于重试分支的粗粒度类别；`key` 是用于行为分支的具体且稳定的原因——绝不要匹配
`message`，它的措辞随时可能改变。`message` 始终为英文（面板在浏览器中把同样的代码翻译成管理员
所选的语言）；`args` 让客户端无需解析文字即可渲染自己的措辞。当失败背后没有代码时（格式错误的
body、未知路径），两者都不出现。

## 请求 body

端点未定义的字段会报错，而不是被忽略：

```json
{ "error": {
    "code": "bad_request",
    "message": "unknown field \"groups\" — see GET /v1/openapi.json for the fields this endpoint accepts"
} }
```

接受 body 却静默丢弃字段，比直接拒绝更糟：调用方把 `{"groups": [2,4]}` 发给期望 `group_ids`
的端点，会得到成功响应，而成员关系毫无变化，只有下次读取该值时才发觉。所以请对照规范检查拼写——
规范由代码生成，因此不可能列出面板不读取的字段。

规范中只有端点真正必需的字段才标记为 `required`。其余字段都可省略并保留文档中的默认值；你永远
不必为了改动一处而发送完整对象。

### 并发编辑

写入没有版本号，也没有 `If-Match`：两个调用方修改同一对象时按最后写入生效，且双方都不会被告知。
这一点在写入携带了它并不打算修改的字段时最为要紧——编辑节点会重新发送整个节点，因此从这里写入的
路由改动，可能覆盖一秒钟前有人在面板中设置的名字。

实际上很少触发，因为 patch 类端点（`PATCH /v1/settings`、`PATCH /v1/users/{id}`）只应用
body 中携带的字段，所以两个调用方改动*不同*字段时不会冲突。如果你从多个地方自动化修改同一对象，
请在写入后读回，而不是假定你的值已经生效。

## 分页

每个列表端点都接受 `?limit` 和 `?offset`，并返回 `meta` 块：

```json
{ "data": [ … ], "meta": { "total": 1043, "offset": 0, "limit": 100 } }
```

`limit` 默认为 **100**，上限为 **1000**；`limit=0`（或负数）表示“从 `offset` 起的全部”，
这是刻意的请求而非常规默认值——无上限的默认值，正是某个集成平稳运行一年、然后在规模增长的面板上
超时的原因。`total` 统计过滤之后、窗口之前的行数，因此调用方无需猜测即可分页。

日志流（`/v1/events`、`/v1/admin-audit`）的分页方式不同，按游标（`?before=<oldest id held>`）：
它们的行会随新行到来而移动，用 offset 会跳过或重复条目。

## 端点

下文的 Base URL 写作 `$BASE`（例如 `https://vpn.example.com/ab12cd34/v1`）。

### 健康检查

```
GET $BASE/health → { "data": { "status": "ok" } }
```

#### 存活探测（无需 API 密钥）

`GET $BASE/healthz` 是唯一不需要密钥的端点——把运行时间监控或负载均衡器指向它。Xray 未运行时
它返回 **503**（而不是 200）：面板本身可能正常，但节点已不再承载任何 VPN 流量，而这正是你需要
被通知的情况。

```
GET $BASE/healthz
200 → { "data": { "status": "ok",       "xray": "running", "xray_started_at": 1752230400 } }
503 → { "data": { "status": "degraded", "xray": "down",    "xray_started_at": 0 } }
```

它被有意放在 API 路径下，而不是服务器根路径：根路径上未经认证的 `/healthz` 会对任何扫描器返回
JSON，从而暴露面板，使伪装失效。API 路径在密钥轮换后保持稳定，因此指向这里的监控会继续工作。

### 用户

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/users` | 列出用户（过滤 + 分页）；`?telegram_id=`、`?sub_token=` 或 `?external_id=` 可查找单个用户。 |
| `POST` | `/v1/users` | 创建用户。 |
| `POST` | `/v1/signup` | 按自助注册规则，从外部系统或你自己的 bot 为用户注册（见下文）。 |
| `POST` | `/v1/users/bulk` | 对多个用户一次性执行同一操作。 |
| `GET` | `/v1/users/{id}` | 获取单个用户。 |
| `PATCH` | `/v1/users/{id}` | 更新名称 / 限额 / 到期时间，或首次连接起算的期限 / 设备数限制 / 速度限制 / 启用状态 / 外部系统的 `external_id` / `mailing`（群发和自动消息的开关——bot 里的开关显示同样的状态）/ `lang`（`ru`、`en`、`zh`、`ar`，或 `""` 表示跟随 Telegram 报告的语言）。 |
| `DELETE` | `/v1/users/{id}` | 删除用户。 |
| `POST` | `/v1/users/{id}/reset` | 重置该用户的流量计数。 |
| `POST` | `/v1/users/{id}/reset-period` | 设置自动重置周期。 |
| `POST` | `/v1/users/{id}/rotate-sub` | 签发新的订阅 URL（旧链接失效）。 |
| `POST` | `/v1/users/{id}/plan` | 为用户应用套餐。 |
| `POST` | `/v1/users/{id}/plan/cancel` | 立即取消付费订阅。 |
| `GET` | `/v1/users/{id}/connections` | 列出用户最近的来源 IP / 设备：`last_seen`、`count`（出现次数，每 45 秒最多一次）、`approx_seconds`（活跃时间的下界）和 `banned`（已在防火墙处丢弃）。 |
| `GET` | `/v1/users/{id}/devices` | 列出通过 HWID 绑定的安装，以及它们所计入的上限。 |
| `POST` | `/v1/users/{id}/devices/unbind` | 释放一个已绑定设备（或全部），腾出名额。 |
| `GET` | `/v1/users/{id}/events` | 该用户自己的日志（分页）。 |
| `GET` | `/v1/users/{id}/abuse` | 匹配到该用户的黑名单条目（`limit`，默认 20）。 |
| `GET` | `/v1/users/{id}/happ-link` | 该用户的订阅，以加密的 `happ://crypt4/` 链接形式返回。 |

**创建** — `name` 必填；其余字段均可选，并在同一次调用中应用到新账户：

```json
{ "name": "alice", "data_limit": 0, "expire_at": 0,
  "device_limit": 3, "speed_limit": 20000, "plan_id": 2, "group_ids": [4] }
```

**设备**是通过 `x-hwid` 订阅请求头绑定的安装（见面板的 *设置 → 订阅*）。列表把名额上限和名册
一起返回，因此一次调用就能回答“这个用户是否已满”：

```json
{ "data": { "devices": [ { "hwid": "…", "os": "iOS", "os_version": "26.5.2",
    "model": "iPhone 15 Pro Max", "app": "Happ/1.0", "ip": "203.0.113.7",
    "first_seen": 1786500000, "last_seen": 1786568000 } ],
  "limit": 3, "enabled": true } }
```

解绑可以指定一个 id 或全部，并返回释放了多少个名额：

```json
{ "hwid": "…" }        →  { "data": { "removed": 1 } }
{ "all": true }        →  { "data": { "removed": 3 } }
```

从未绑定过的设备返回 `404`，而不是静默成功。释放的名额立即可用：来自新安装的下一次获取即被接受。

`speed_limit` 是以 **kbit/s** 为单位的单用户带宽上限（0 = 不限），双向生效。它由宿主内核针对
用户连接的地址实施，而不是由 Xray——因此同一 NAT 地址后的所有人共用一个上限，且使用 Hysteria2
时该上限表现为丢包，而不是平滑降速。套餐自带 `speed_limit`，应用时会覆盖用户自己的值，与它对
流量和设备限额的处理完全一致。访问组也可以携带 `speed_limit`，当用户所属的某个组带有时，
**以组的上限为准**——用户自己的字段（来自套餐或手工设置的值）会保留但被覆盖；参见 *访问组*。

**从首次连接起算的期限** — 用 `hold_seconds` 代替 `expire_at`：它是一段在用户首次连接之前
不计时的期限长度。面板记录到的首次连接会把 `expire_at` 设为该时刻加上 `hold_seconds`，
并把 `hold_seconds` 归零（日志中记为 `user.term_started`）。两者互斥——同时带有非零
`expire_at` 和 `hold_seconds` 的 body 会被拒绝。等待开始的期限最长为 3650 天。

```json
{ "name": "gift-card-17", "hold_seconds": 2592000 }
```

在 `PATCH` 中，`hold_seconds` 会把用户置为暂停（替换任何日期），而 `0` 会移除待开始的期限；
`expire_at: 0` 表示*永不过期*，同样会移除待开始的期限。两者都不涉及的 `PATCH` 会保持期限完全
不变——包括刚刚开始的期限。`/v1/users/bulk` 中的 `extend` 会把待开始的期限延长给定天数，
应用套餐则会用套餐自己的期限替换它。用户对象以待开始期限的形式携带 `hold_seconds`（没有时为 0）。

**Happ 链接** — `GET /v1/users/{id}/happ-link` 返回
`{ "data": { "link": "happ://crypt4/…" } }`：用户的订阅 URL，用 Happ 应用内置的密钥加密，
Happ 添加时不会显示地址。当 `sub_happ_crypt` 关闭时该链接为 `""`（见 *服务器配置*）。用单独的
调用而不是用户上的字段，是因为每个链接都是一次 RSA-4096 加密且填充随机，所以两次调用会对同一
订阅返回不同的链接。

`plan_id` 会用套餐自己的值重写 `data_limit` 和 `expire_at`——套餐*就是*限额——而显式的
`device_limit` 在其之后应用并生效。如果某个额外项失败，账户**不会**回滚（凭据可能已经在别人
手上）：调用返回错误，用户停在已经执行到的状态。成功返回 `201`，并带有用户实际的结果。

**取消订阅** — 立即把用户移到免费套餐（剩余付费时间作废），或在未配置免费套餐时结束其访问。
这与手工应用免费套餐不同：此事记为 `plan.cancelled`，而计费集成正是对该事件作出反应。

**列表** — 查询参数：`status`（`active` / `disabled` / `expired` / `limited`
/ `device_limited`）、`search`（名称的子串）、`limit`、`offset`
（`limit<=0` = 从 `offset` 起的全部）。响应会附加 `meta` 块：

```json
{ "data": [ ... ], "meta": { "total": 42, "offset": 0, "limit": 20 } }
```

**批量** — body：

```json
{ "ids": [1, 2, 3], "action": "extend", "days": 30 }
```

`action` 取 `enable`、`disable`、`delete`、`reset`、`extend` 之一（`days` 仅在 `extend`
时必需）。响应：`{ "data": { "affected": 3 } }`。

**重置周期** — body：`{ "period": "monthly" }`（`none` / `daily` / `weekly`
/ `monthly` / `yearly`）。

**创建** — body：

```json
{ "name": "alice", "data_limit": 0, "expire_at": 0 }
```

`data_limit` 以字节为单位（0 = 不限）；`expire_at` 是 Unix 时间戳（0 = 永不过期）。响应的
`data` 是完整的用户对象，包括 `sub_url`、内置线路的 `vless` / `reality` / `hysteria2` 分享
链接，以及 `links`——该用户在此服务器上的每一条线路（含自定义入站），每条都带有客户端将显示的
名称。

**Patch** — 只发送你想修改的字段：

```json
{ "name": "alice2", "data_limit": 107374182400, "expire_at": 1767225600, "device_limit": 3, "enabled": true }
```

**应用套餐** — body：

```json
{ "plan_id": 2, "extend_from_current": false }
```

### 计费

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/billing/providers` | 列出已启用的支付方式（客户端可以用什么付款）。 |
| `GET` | `/v1/billing/plans?include_disabled=true` | 列出套餐。 |
| `POST` | `/v1/billing/plans` | 创建套餐（不带 `id`）或更新套餐（带 `id`）。 |
| `DELETE` | `/v1/billing/plans/{id}` | 删除套餐（仍有用户在该套餐上时拒绝）。 |
| `POST` | `/v1/billing/plans/{id}/migrate` | 把该套餐上的所有用户迁移到另一个套餐。 |
| `GET` | `/v1/billing/orders?status=pending&user_id=5` | 列出支付订单（`status` 和 `user_id` 可选）。 |
| `POST` | `/v1/billing/orders` | 为用户 + 套餐开单，或为余额充值（`"kind": "topup"`）。 |
| `GET` | `/v1/billing/orders/{id}` | 获取单个订单（轮询支付状态）。 |
| `POST` | `/v1/billing/orders/{id}/confirm` | 把订单标记为已支付（激活套餐）。 |
| `POST` | `/v1/billing/orders/{id}/cancel` | 取消订单。 |
| `GET` | `/v1/billing/settings` | 计费配置。 |
| `POST` | `/v1/billing/settings` | 替换计费配置（整个对象）。 |
| `GET` | `/v1/billing/stats` | 收入总额、按服务商拆分、待处理积压。 |
| `GET` | `/v1/billing/promos` | 列出优惠码。 |
| `POST` | `/v1/billing/promos` | 创建优惠码（不带 `id`）或更新优惠码（带 `id`）。 |
| `DELETE` | `/v1/billing/promos/{id}` | 删除优惠码。 |
| `GET` | `/v1/users/{id}/wallet` | 用户的余额、推荐关系，以及最新的流水记录。 |
| `GET` | `/v1/users/{id}/referrals` | 该用户邀请的用户，以及他们的消费。 |
| `GET` | `/v1/billing/promos/{id}/uses` | 谁使用了某个优惠码，以及它的订单带来了多少钱。 |
| `GET` | `/v1/billing/referrals` | 推荐统计和邀请最多的人。 |
| `GET` | `/v1/billing/funnel?days=30` | 在过去 N 天加入的用户中（0 = 全部时间）：多少人使用了试用、付费、再次付费；以及召回码的效果，和流失用户未续费的原因（`churn`）。 |
| `POST` | `/v1/billing/orders/{id}/refund` | 把已支付套餐订单的金额退回余额（一次）。 |
| `POST` | `/v1/users/{id}/balance` | 修正用户余额。 |
| `POST` | `/v1/users/{id}/autorenew` | 打开或关闭从余额自动续费（`{"on": true}`）。 |
| `POST` | `/v1/users/{id}/promo` | 为用户输入优惠码（`{"code": "SPRING"}`）。 |
| `POST` | `/v1/users/{id}/churn` | 用户对退出问卷的回答（`{"reason": "price", "comment": "..."}`）。 |
| `GET` | `/v1/users/{id}/quotes` | 用户现在能买什么，按期限列出，并已计入其优惠码和余额。 |
| `GET` | `/v1/users/{id}/subscription?lang=ru` | 订阅页面的数据形式，供你自行绘制。 |
| `POST` | `/v1/users/{id}/telegram` | 绑定用户的 Telegram ID（`{"chat_id": 123}`；0 表示解绑）。 |
| `POST` | `/v1/users/{id}/referrer` | 记录是谁邀请的该用户（`{"ref_code": "..."}` 或 `{"referrer_id": 5}`）。 |
| `GET` | `/v1/payments` | 所有支付服务商及其设置表单。 |
| `POST` | `/v1/payments` | 配置某个服务商。 |

套餐可以指定它授予的访问组（`"group_ids": [3]`）：被放到该套餐上的人——通过付费订单、
`POST /v1/users/{id}/plan` 或注册时——会加入这些组，并在套餐变更时离开它们。通过
`POST /v1/users/{id}/groups` 直接分配的成员关系会保留；只会收回套餐所授予的部分。空列表表示
该套餐对访问不做任何规定。

**创建订单** — body `{ "user_id": 5, "plan_id": 2 }`。响应携带订单，以及在配置了支付服务商时
一个托管支付页 `pay_url`，用于把用户送到那里：

```json
{ "data": { "order": { ... }, "pay_url": "https://..." } }
```

手工订单——不带 `provider`，或 `"manual"`——返回空的 `pay_url` 并等待 `/confirm`；手工支付
关闭时会被拒绝。你自己收到的钱——你自己 bot 里的 Telegram Stars、你自己的收银台——使用
`"provider": "external"`，无论手工支付设置如何：`"paid": true` 会在同一次调用中确认它，或者
稍后用 `/confirm`——在此之前订单保持待处理，`/cancel` 会丢弃它；`external_ref` 会把你的支付 id
保存为 `provider_id`。它和其他支付一样计入收入。创建订单**不是**按键幂等的，但也不会堆叠重复：
同一用户、同一套餐、同一服务商的仍处于待处理的订单会被复用，而不是再开一个，因此重试的调用会
返回已经存在的订单。

**计费设置** — 整个对象，写入时替换（没有部分更新：“没有免费套餐”是一种真实状态，必须与
“未指定”区分开）：

```json
{ "enabled": true, "free_plan_id": 1, "trial_plan_id": 2, "payment_note": "card 1234",
  "manual": true, "manual_label": "", "wallet": true, "topup_min": 100,
  "topup_max": 15000,
  "ref_mode": "percent", "ref_percent": 10, "ref_days": 7, "ref_first": false }
```

把某个套餐指定为免费或试用套餐，也会同时把它设为免费，并重新应用到已经在它上面的所有人——
这是面板所执行的同一条规则。`manual` 在服务商之外提供手工支付，`manual_label` 是其支付按钮的
标签（空 = 默认措辞），`payment_note` 是向手工付款用户显示的文本。

`wallet` 让用户可以持有余额（充值、用余额购买套餐、用余额续费）；`topup_min` 和 `topup_max`
是卢布计的最小和最大充值额（`topup_max` 默认 15000，最大 1000000）。`ref_mode` 是推荐人在
被邀请者付款时获得什么：`off`、`percent`（付款金额的 `ref_percent` %，含充值，计入推荐人余额
——需要 `wallet`）或 `days`（`ref_days` 天加到其套餐上，用于用钱购买的套餐）；`ref_first`
只对第一笔这样的付款支付。若省略，`wallet`、`topup_min`、`topup_max`、`ref_mode`、
`ref_percent`、`ref_days` 和 `ref_first` 保留其已存储的值。

**订单与余额** — 订单的 `amount_rub` 始终是需要支付的金额。当用户余额覆盖部分价格时，
`balance_kop` 是该部分（戈比），`amount_rub` 是其余部分；确认订单会把钱计入余额，并用套餐把
整个价格从余额中扣除。`kind` 是 `plan` 或 `topup`（充值的 `plan_id` 为 0）。完全用余额购买
的套餐会显示为一笔已支付订单，`provider` 为 `balance`、`amount_rub` 为 0——它不算收入。
`discount_rub` 和 `promo_code` 显示优惠码。当余额覆盖全部价格时，
`POST /v1/billing/orders` 返回 `err.payFromBalance`；请改为发送 `"from_balance": true`
用余额购买套餐。为一笔已取消订单到的钱（被同一优惠码的新结账取代、作为放弃被清扫、手工取消）
与其他任何支付一样计入——并且 `POST /v1/billing/orders/{id}/confirm` 也接受已取消的订单：
套餐被授予，或者在钱包开启时，当套餐已无法再给予（期间买了别的套餐、已持有终身套餐），钱会
计入余额，订单变为 `topup`。一个用户在一小时内最多可以有 3 笔未支付的充值订单。

**优惠码** — `kind` 为 `percent` / `amount`（用户下次付款的折扣，`value` 为百分比或卢布；
`plan_ids` 把它限制在这些套餐上，`first_only` 限制为从未购买过套餐的人）、`days`
（`value` 天，加到 `plan_id` 上；`plan_id` 为 0 时加到用户当前生效的付费套餐上）或
`balance`（`value` 卢布计入余额）。`max_uses` 为 0 表示不限，`expires_at` 为 0 表示永不过期；
每个用户可以使用一次优惠码。优惠码不区分大小写。

**多个周期** — `POST /v1/billing/orders` 中的 `"periods": 3` 会一次购买该套餐的三个周期，
折扣按计费设置中 `periods` 指定的档次（`[{"periods": 3, "percent": 10}]`）；没有对应优惠的
数字会被拒绝。期限就是这么多周期，而套餐不会按自身周期补充的配额，会每个周期补充一次。

**退款** — `POST /v1/billing/orders/{id}/refund` 带 `{ "cancel_plan": false }`，把订单的
金额（现金部分和余额部分）退回用户余额，仅一次；`cancel_plan` 还会把该订单支付的相应天数从
期限中扣掉（只有扣完时套餐才结束）。需要开启钱包。支付系统报告的退款或拒付，会自动把订单标记为
`refund_source: "provider"`：它退出收入，收回它买到的内容，并且不能再退回余额。

**召回** — 计费设置中的
`"winback": {"enabled": true, "after_days": 7, "percent": 20, "valid_days": 7}`：
付费期限失效后经过这么多天，用户会收到一个一次性优惠码，享有该折扣，附加在其下次付款上，并通过
bot 发送。

**流失问卷** — 计费设置中的 `"churn_survey": true`：付费期限失效一天后，用户 bot 会询问为何
没有续费，订阅页面在用户持续流失期间也会问同样的问题。原因有：`price`、`quality`、
`not_needed`、`competitor`、`other`（可附评论）。每次流失只记录一个回答。你自己的用户中心也可以
提问：`GET /v1/users/{id}/subscription` 在有问题可问时携带 `churn`，而
`POST /v1/users/{id}/churn` 接收回答（`source` 为 `api`；之后的回答会替换原因）。
`GET /v1/billing/funnel` 会在 `churn` 下携带它们：
`{"total": 12, "reasons": {"price": 5, ...}, "comments": [{"user_id": 7, "user_name": "...",
"reason": "other", "comment": "...", "source": "bot", "created_at": ...}]}`。

**余额** — `POST /v1/users/{id}/balance` 接受
`{ "amount_kop": 10000, "note": "..." }`（正负号均可；余额不能低于零），并返回
`{ "balance_kop": ... }`。`GET /v1/users/{id}/wallet` 返回
`{ "wallet": {...}, "history": [...], "ref_link": "..." }`——`ref_link` 是用户的邀请
链接，仅在推荐计划和用户 bot 开启时提供。

**从你自己的 bot 销售** — `GET /v1/users/{id}/quotes` 列出用户现在能买什么（付费套餐生效
期间只有当前套餐），并按期限给出：计入其优惠码后的 `total_rub`、余额覆盖的 `balance_kop`，
以及剩余需支付的 `money_rub`。然后当 `money_rub` 为 0 时用 `from_balance` 调用
`POST /v1/billing/orders`（请附上 `expect_expire_at`——即你显示过的 `expire_at`——这样重试
的请求不会买下第二个周期），其余用 `provider` 支付。充值是
`{"user_id": 5, "kind": "topup", "amount_rub": 300}`（可再加 `provider`，或都不带以成
为手工充值）。两者都接受 `lang`（`ru`/`en`/`zh`/`ar`，用于手工支付说明）和 `return_url`
（托管支付把付款人送回哪里；省略时为用户的订阅链接——如果已设置你自己的页面，则为你自己的页面）。

**你自己的 bot 或用户中心** — 密钥留存在你的服务器上，永远不要放进浏览器。勾选它会调用的方法：
创建用户并绑定 Telegram、开单、优惠码、推荐人、续费——并且不要勾选确认订单、修正余额和退款，
这些会转移资金而不经过资金到账。在每条消息上查找用户：`GET /v1/users?telegram_id=<id>`
（带索引的查询；空列表表示新用户）。新用户用带 `plan_id` 的 `POST /v1/users` 创建（试用或
免费套餐——没有套餐的用户不会看到付款区块），然后用 `{"chat_id": <id>}` 调用
`POST /v1/users/{id}/telegram`；如果其 `/start` 带有邀请码，再用
`{"ref_code": "r_…"}` 调用 `POST /v1/users/{id}/referrer`（有无 `r_` 均可）——只调用一次，
在其首次付款之前。用户自己的邀请码是 `GET /v1/users/{id}/wallet` 中的 `ref_code`（在面板的
用户 bot 开启时也有 `ref_link`）。通过订阅链接登录的用户中心，用 `?sub_token=` 找到他们。
`GET /v1/users/{id}/subscription` 拥有订阅页面显示的一切——状态、`blocks`（插件向页面添加的
内容：`{type: text|markdown|notice|button, text, label, url}`）、流量、期限、`sub_url`、
按应用的一键导入（`apps`）、每个配置（`links`）、Clash 下载（`clash_url`）、纯 WireGuard
配置（`wg`：`{name, conf_url, qr_url}`，形式同 `awg`）、按服务器的 Telegram 代理
（`telegram`：`{name, link, web_link, qr_url}`——`link` 是一键添加的 `tg://proxy`）、设备、
付款区块（`billing`）、运营者的配色（`brand`）、`terms_url` / `privacy_url`（运营者的文档，
当其有文本时——`GET /v1/legal` 提供文本本身）以及 `tg_link`——用户 bot 的链接，把账户绑定到
打开该页面的 Telegram（在 `tg_linked` 时会把账户移过去；当 *设置 → Telegram* 中的开关没有
提供任何东西时不存在，有效期为 15 分钟）——措辞按 `?lang`。各项操作通过上面的端点完成。它
保持页面保持的订阅设置：当页面不会列出任何配置时（`show_configs` 关闭，或需要 HWID——原始
链接会绕过设备上限），`links` 为空；当页面的 Clash 按钮关闭或需要 HWID 时，`clash_url`
不存在；Happ 按钮在该项开启时使用加密链接；`maintenance` 是面板的维护模式。`churn` 是要提问
的退出问卷——`{question, reasons: [{key, label}]}`，措辞按 `?lang`——仅在用户的付费期限已
失效且尚未回答时出现；把选中的 `key` 发送到 `POST /v1/users/{id}/churn`。

**法律文档** — `GET /v1/legal`（任意密钥）返回运营者的用户协议和隐私政策，撰写于
*设置 → 品牌 → 文档*：`{"terms": {...}, "privacy": {...}}`，每项都带有 `markdown`、
`html`（已渲染；源文档中的原始 HTML 会被丢弃）、`url`（其公开页面，文档为空时为 ""）和
`updated_at`（0 = 从未设置）。把它们显示在你自己的页面上，或链接其 `url`。

**从外部系统注册** — 用 `{"external_id": "ann@example.com"}` 调用 `POST /v1/signup`，
按用户 bot 所遵循的规则，以外部系统自己的 id 注册一个用户：关闭注册时拒绝
（`err.signupClosed`），邀请模式需要 `invite`（`err.signupBadInvite`），审核模式会登记一条
请求，账户像 bot 注册一样获得试用或免费套餐。每个 `external_id` 一个账户——精确比较，因此请
先把邮箱转为小写——所以再次注册会返回它（`200`、`"status": "existing"`、其 `user_id`；完整
账户只返回给拥有 `users.view` 的密钥），而不是第二份试用。外部系统必须只传递它已验证过的 id
（一个已确认的邮箱）：任何传递该 id 的人都会得到那个账户。新账户返回
`201 "created"` 和账户，一条请求返回 `202 "pending"` 和 `request_id`（在等待期间再次请求
不会登记任何内容）。可选：`name`（省略时为外部 id）、`source`（漏斗的标签）、`ref`（邀请码；
未知的会被忽略）、`lang`（`ru`、`en`、`zh` 或 `ar`，交给账户——立即生效，或在运营者批准时）
以及 `ip`——客户端地址，用于按地址（IPv6 按 /64）和按 id 限制每分钟一次注册，总计每分钟 20 次；
超出时返回 `429` 并带 `Retry-After`。为外部系统的密钥勾选 `POST /v1/signup`（外加它展示和
销售所需的内容），而不要勾选 `POST /v1/users`：这样它就无法按自己的条件创建用户。
`user.registered` 和 `registration.rejected` 会携带 `external_id`，而
`GET /v1/users?external_id=` 可以再次找到该账户。

**从你自己的 bot 注册** — 用 `{"telegram_id": 123456789}` 代替 `external_id` 调用
`POST /v1/signup`（两者取其一），按面板为 Telegram 用户所保持的规则注册：已有账户会返回
（`"existing"`），已解绑的账户会被恢复，共享黑名单会拒绝（`err.signupBlacklisted`），每个
Telegram 只永久享受一次试用——被删除的账户不会获得第二次——并且该 Telegram 会绑定到新账户。
`name`、`source`、`ref`、`invite` 和 `ip` 的用法同上——每个 Telegram 每分钟一次注册，并且
与 `external_id` 注册共享每分钟 20 次的总量；在审核模式下，请求属于该 Telegram，批准时会将其
绑定。**只传递 Telegram 给你的 ID**——发给你的 bot 的消息的发送者，或你验证过的 Mini App
`initData`——绝不要用客户端自己输入的：传递某个 ID 的人会让那个 Telegram 注册、消耗它唯一的
试用，并持有它所绑定的账户。

来自 bot 或 Mini App 的客户端已经有账户：用
`PATCH /v1/users/{id}` `{"external_id": "ann@example.com"}` 把外部系统的登录关联到它，
而不是再注册一次——在用户通过订阅链接到达的页面上操作（`?sub_token=` 可找到账户），这样 id
就是他们自己的。已被另一个账户占用的 id 会被拒绝（`err.externalIDTaken`）；`""` 会移除它。

**迁移** — body `{ "to_plan_id": 3 }`，响应 `{ "data": { "migrated": 12 } }`。
把目标套餐的限额、周期和访问组应用到源套餐上的所有用户。这是删除套餐前清空它的唯一方式。

**服务商** — `GET /v1/payments` 返回注册表中每个服务商及其设置表单：它接受的字段、其当前值、
哪些密钥已设置（绝不返回其值），以及要粘贴到服务商后台的 `webhook_url`。字段列表按服务商而
不同，由注册表生成，因此这个 payload 刻意保持自由格式。`POST /v1/payments` 接受
`{ "key": "yookassa", "enabled": true, "config": {...} }`；留空的密钥会保留其已存储的值。

`GET /v1/billing/providers` 是动态的——它返回你在面板中启用的任何支付方式（银行卡、SBP、
加密货币……），每种都带一个 `key`，手工支付开启时 `manual` 排在首位。这个 `key` 就是你在开单
时传给 `provider` 的值。

### 节点

管理服务器集群。本地面板服务器是节点 `0`；其余是远程**节点**，各自向面板保持一条出站的长轮询。
用户和限额会自动同步到每台已启用的节点，并且每台服务器都能独立编辑。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/nodes` | 列出服务器（本地面板是节点 `0`），带状态和今日流量。 |
| `POST` | `/v1/nodes` | 注册节点——返回一行安装命令（加入令牌只显示一次）。 |
| `GET` | `/v1/nodes/{id}` | 获取单个节点。 |
| `PATCH` | `/v1/nodes/{id}` | 编辑名称 / 主机 / 协议 / 路由 / DNS 覆盖，以及 WARP-Opera 出口。 |
| `DELETE` | `/v1/nodes/{id}` | 删除节点（停止为该节点上的用户服务）。 |
| `POST` | `/v1/nodes/{id}/enabled` | 启用或停用节点。 |
| `POST` | `/v1/nodes/{id}/regen-join` | 签发新的安装命令（撤销该节点当前的令牌）。 |
| `POST` | `/v1/nodes/{id}/update` | 请求节点自行更新到最新版本。 |
| `POST` | `/v1/nodes/update-all` | 请求每个已连接的节点自行更新（依次进行）。 |
| `POST` | `/v1/nodes/{id}/proxy` | 配置该服务器的系统代理（`id` 为 0 表示主服务器）。 |
| `POST` | `/v1/nodes/{id}/placement` | 编辑该服务器的位置和流量上限（`id` 为 0 表示主服务器）。 |
| `GET` | `/v1/nodes/{id}/health` | 单台服务器的自检结果。 |
| `GET` | `/v1/nodes/{id}/reach` | 服务器最近一次从其他国家进行的检测（Globalping；`at` 为 0 = 尚未检测）。 |
| `POST` | `/v1/nodes/{id}/reach` | 立即从其他国家检测某台服务器（最多需一分钟）。 |
| `GET` | `/v1/reach` | 该检测的国家、每国探测点数、监控间隔和阈值。 |
| `POST` | `/v1/reach` | 设置上述项；省略 `token`（Globalping）会保留已存储的值，`""` 会清除它。 |
| `GET` | `/v1/alerts` | 告警规则；`firing` 统计当前未恢复的告警数。 |
| `GET` | `/v1/alerts/metrics` | 规则可以监控的指标及其单位。 |
| `GET` | `/v1/alerts/events` | 触发历史，最新在前（`limit`，`before` = 继续往下读取的 id）；`resolved_at` 为 0 = 仍在触发。 |
| `POST` | `/v1/alerts` | 添加规则：`name`、`metric`、`op`（`gt` / `lt`）、`threshold`、`for_secs`（条件持续多久后触发）、`node_id`（-1 表示每台服务器，0 表示主服务器）、`enabled`。 |
| `POST` | `/v1/alerts/{id}` | 替换规则的设置。 |
| `DELETE` | `/v1/alerts/{id}` | 删除规则；其历史会保留。 |
| `GET` | `/v1/nodes/{id}/logs` | 服务器的最近日志行（0 为主服务器）。 |
| `GET` | `/v1/logs` | 面板自身的最近日志行。 |

**注册节点** — body `{ "name": "NL #1", "host": "nl1.example.com" }`。响应携带节点 id、
一次性加入令牌，以及可在全新 Ubuntu 服务器上直接运行的安装命令：

```json
{
  "data": {
    "id": 3,
    "join_token": "rpn_…",
    "install_command": "curl -Ls https://.../install.sh | sudo bash -s -- --join '…#rpn_…'"
  }
}
```

加入令牌只嵌入一次，24 小时后过期；`/regen-join` 会签发新的令牌。

**位置** — 服务器在订阅中的位置，以及它可以承载多少流量。这是部分更新：只改动发送的字段。
`PATCH /v1/nodes/{id}` 也接受同样的字段；本路由还能触达主服务器（`id` 为 0），它没有节点记录。
返回的是已存储的位置设置。

```json
{ "country": "NL", "sort_weight": 10, "capacity": 300, "hide_when_full": true,
  "traffic_limit": 1099511627776, "traffic_period": "month", "traffic_reset_day": 14,
  "hide_when_over": false }
```

`traffic_limit` 是每个 `traffic_period`（`month` 或 `day`；0 = 不限制）的字节数。
`traffic_reset_day`（1–31）是月度上限重新起算的日子，用来对齐主机商计费的日期；没有该日的月份
从当月最后一天起算，而 `1` 会按默认值 `0` 存储。清除上限会同时清除其周期、重置日和
`hide_when_over`。

**系统代理** — 该服务器上的 SOCKS5 和/或 HTTP 转发监听，用于非 VPN 客户端的流量：爬虫、bot，
或另一块把出口接在此处的面板。没有任何用户凭据能打开它，没有访问组能限制它，它也永不出现在订阅
中。流量按该服务器的路由发出，因此 WARP、Opera 和代理通道的作用与对 VPN 客户端完全一样。

```json
{ "socks_enabled": true, "socks_port": 1080,
  "http_enabled": true,  "http_port": 3128,
  "accounts": [ { "user": "scraper", "pass": "…" },
                { "user": "bot",     "pass": "…" } ] }
```

`accounts` 每次都是完整列表——删除其中一项的办法就是发送不带它的列表。登录名必须唯一，且不能
包含冒号或空格（冒号是 `user:pass` 的分隔符）；只要任一监听开启，就至少需要一个完整账户：公开
端口上的开放代理会在几小时内被扫描器发现，随后就变成别人用这台服务器 IP 发出的垃圾邮件中继。
启用了协议却没给端口时，会得到 1080（SOCKS）/ 3128（HTTP）。每台服务器都有**自己**的账户和
端口——节点从不继承主服务器的，因此泄露一个登录只打开一台机器。响应是已存储的配置，因此只发送了
`{"socks_enabled": true}` 的调用方可以得知它得到了哪个端口。

### 自定义入站

运营者定义的入站，位于三个内置线路之外，每台服务器一套（服务器 `0` = 主服务器）。每个入站都是
公开监听，因此创建/更新会在**目标机器上**校验（`xray -test` + 端口绑定探测）之后才保存。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/servers/{id}/inbounds` | 列出某台服务器的自定义入站（`id` = 服务器 id，`0` = 主服务器）。 |
| `POST` | `/v1/servers/{id}/inbounds` | 在该服务器上创建自定义入站。 |
| `POST` | `/v1/inbounds/{id}` | 更新自定义入站（以入站自身的 id 为键）。 |
| `DELETE` | `/v1/inbounds/{id}` | 删除自定义入站。 |

**创建 / 更新** — body 与面板的入站编辑器一致：`name`、`protocol`
（`vless` / `trojan` / `hysteria2` / `shadowsocks` / `turn-vk` / `wg`）、`transport`（`tcp` / `ws` / `xhttp` /
`grpc` / `httpupgrade`）、`port`、`security`（`none` / `tls` / `reality`）及对应的键
（REALITY 的 dest 与密钥、指纹、path/host、Hysteria2 的端口跳跃范围），外加可选的高级区块
（XHTTP 的 `extra`、TCP HTTP 伪装、`sockopt`、额外的 TLS 键）。完整字段列表——以及哪些组合
有效——见 `openapi.json` / Swagger。
`shadowsocks` 是 Shadowsocks-2022（`method` 选择 AEAD；服务器密钥自动生成，每用户密钥由
UUID 派生）——支持多用户，因此按用户的统计和配额可用，但只有现代客户端（sing-box、mihomo、
v2rayN、Shadowrocket、Streisand）支持它。
`turn-vk`（原名为 `wireguard`，输入时仍然接受）是 TURN 中继背后的 WireGuard：`port` 是中继
监听的公开 UDP 端口，`turn_link` 是可选的 VK 通话链接（`https://vk.com/call/join/…`），
交给用户的 app；服务器密钥、Xray 监听的环回端口（响应中的 `wg_public_key`、
`wg_local_port`）以及 Free Turn Proxy 的伪装密钥（永不返回）都会自动生成。它不在任何订阅格式
中——用户从订阅页面将其导入 app（`vkturnproxy://`、`freeturn://`、`wingsv://`），或从订阅
页面下载 `<sub>/wg/<inbound id>.conf`。
`wg` 是普通 WireGuard：Xray 自带的 WireGuard 入站，监听公开 UDP `port`，任何原版 WireGuard
客户端都能直接连上。服务器密钥自动生成（响应中的 `wg_public_key`）；transport 和 security 固定，
`turn_link` 不适用。用户使用他们在 AmneziaWG 中拥有的隧道身份作为对端。它同样不在任何订阅格式
中——用户从订阅页面下载 `<sub>/wg/<inbound id>.conf`（或 `.png`，即同一文本的二维码）。
`enc_mode` 在 `vless` 入站上开启 VLESS 加密——`native`、`xorpub` 或 `random`（握手在链路上
的样子），空表示关闭；密钥自动生成（响应中的 `encryption` 即客户端那一半）。开启后 `tcp` 可以
搭配 `security: none`。
`cdn_host`（以及 `cdn_port`，为 0 时是 443）把 `vless` / `trojan` 入站放到 `ws` /
`httpupgrade` / `xhttp` 之后并通过 CDN 提供服务：链接指向该域名的 TLS，CDN 访问入站自己的
端口（`security: none` 时为明文），入站信任其 X-Forwarded-For。
`pin_cert`（TLS，且不在 CDN 之后）让客户端按指纹而不是按名称校验服务器证书，因此 `sni`
可以是任意域名——比如运营者白名单上的某个域名。
schema 只能标为 `string` 的两种格式：`reality_dest` 是裸主机名（`www.microsoft.com`——拒绝
`host:port` 形式），而 `hop_interval` 是以秒为单位的范围（`"5-10"`，而不是 `"30"`）。
响应的 `data` 是已保存的入站；被拒绝的配置（端口已被占用、组合无效、节点离线）返回 `400` 并
带上原因。客户端无法表示的入站会从 Clash/sing-box 订阅中静默剔除，而不是输出为损坏的条目。

入站可以通过 `inbound:<id>` 授权令牌被访问组引用（见 **访问组**）。

`GET /v1/inbounds/catalog` 公布什么能与什么组合——每种协议 × 传输、各自允许的 `security`
模式、哪些订阅格式无法承载它，以及高级字段接受的枚举值。它与面板编辑器使用的是同一张表，因此
依据它构建入站的客户端无法构造出校验器会拒绝的入站。

### 访问组

访问组决定用户可以使用哪些连接。**不在**任何组中的用户可以访问一切；属于一个或多个组的用户，
可以访问这些组授权内容的**并集**。只有**限制访问**的组（下文 `limits_access`）参与其中：
如果一个用户所属的组都不限制访问，他同样可以访问一切。
限制由**服务器端**执行——被禁止的线路的凭据根本不会下发给 Xray，而不只是被隐藏——因此用户对象
的 `links`、订阅以及手工构造的链接，都只暴露已被授予的内容。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/groups` | 列出访问组，每个都带 `grants`、`member_ids` 和成员数。 |
| `POST` | `/v1/groups` | 创建访问组。 |
| `POST` | `/v1/groups/{id}` | 更新访问组（名称、授权、速度上限）。 |
| `DELETE` | `/v1/groups/{id}` | 删除访问组（成员若不再属于任何限制访问的组，则恢复为不受限）。 |
| `POST` | `/v1/groups/{id}/members` | 替换该组的成员。 |
| `POST` | `/v1/users/{id}/groups` | 替换某个用户的组成员关系。 |

**创建 / 更新** — body：

```json
{ "name": "VIP", "grants": ["builtin:0:vless", "builtin:0:reality", "inbound:7"],
  "speed_limit": 20000 }
```

`speed_limit` 以 **kbit/s** 限制成员的速度（0 = 该组不设置上限）。设置后会**优先于**成员的
套餐或卡片给出的上限；属于多个设置了上限的组的成员，获得其中**最高**的上限。黑名单限速永远不会
因此放宽——两者取更严格的那个。更新时省略该字段会保留原上限。

**授权令牌**（grant token）指向一条连接：

- `builtin:<server_id>:<lane>` — 内置线路，其中 `<lane>` 取 `vless`、`xhttp`、`reality`、
  `hysteria2`、`naive`、`trusttunnel`、`anytls`、`mieru`、`awg` 或 `mtproto`，`<server_id>` 为 `0`
  表示主服务器，或一个来自 `GET /v1/nodes` 的节点 id。
- `inbound:<id>` — 自定义入站，id 来自 `GET /v1/servers/{id}/inbounds`。

空的 `grants` 数组表示该组**不限制访问**：这样保存时，它仅作为速度上限的层级存在，其成员保留
全部连接。响应以 `limits_access` 携带这一点——它在组被保存时确定，而不是从授权列表读出。当授权
所指向的入站、节点或外部服务器被删除时，授权也会被自动清理，而这样失去最后一条授权的组会继续
保持 `limits_access: true`：其成员什么也访问不到，而不是突然能访问一切。要打开它，请在不带任何
授权的情况下再保存一次。在此行为出现之前就已存在的组，会一直限制访问，直到被再次保存。响应的
`data` 是该组。

**设置成员** — body `{ "user_ids": [1, 2, 3] }`；替换整个成员集合。

**设置用户的组** — body `{ "group_ids": [4, 5] }`；替换用户的全部成员关系。空数组会把用户从
所有组中移除（→ 不受限）。

引用已删除入站或节点的授权会被自动清理，在此之前它们是无害的。

**令牌从哪来** — `GET /v1/groups/targets` 列出每台服务器及其可被组授权的连接，每个都带有
现成的令牌：

```json
{ "data": [
  { "server_id": 0, "server_name": "Мастер",
    "lanes": [ { "lane": "vless", "label": "VLESS-TCP-TLS",
                 "token": "builtin:0:vless", "enabled": true } ],
    "inbounds": [ { "id": 7, "name": "WS резерв",
                    "token": "inbound:7", "enabled": true } ] }
] }
```

已禁用的线路和入站也会包含在内，因此可以在连接启用之前先准备好授权。手工拼装这些令牌同样可行——
但一个拼写错误会静默地授予不了任何东西，而这正是该端点要避免的。

### 服务器配置

上面的端点操作一块面板；这些端点则配置一台服务器。这里的一切都对应面板中的某个页面，并调用其
背后的同一套代码，因此 UI 会拒绝的值，这里同样会被拒绝。

有两样东西被刻意省略。**管理员和 API 密钥**没有任何端点：能够铸造密钥的密钥会把一次泄露变成
永久、自我延续的访问，而这个决定应该留在需要密码提示的会话之后。**面板的秘密路径**不会由任何
`/v1` 路由返回——它是面板前面的隐匿层，而不是一个可以读出来的设置。

```
GET   $BASE/v1/settings                        → 运行参数
PATCH $BASE/v1/settings                        → 修改其中一部分
GET   $BASE/v1/servers/{id}/routing            → 路由、DNS 和出口后端
POST  $BASE/v1/servers/{id}/routing            → 修改它们
POST  $BASE/v1/servers/{id}/xray-restart       → 重启该服务器的 Xray
GET   $BASE/v1/config/snapshots                → 配置存档点
POST  $BASE/v1/config/snapshots                → 创建一个
POST  $BASE/v1/config/snapshots/{id}/rollback  → 从某个存档点恢复服务器配置
DELETE $BASE/v1/config/snapshots/{id}          → 遗忘一个
```

`PATCH /v1/settings` **只应用 body 中携带的字段**。所有未出现的字段保持原样，这正是它可以安全
使用的方式：读取对象、改动一个值、再把这个值发回去——重新提交整个对象也会重新应用期间另一位
运营者所做的改动。分组字段（`hwid_*`、`local_backup_*`）在内部由一次写入存储，因此它们会覆盖到
当前行上，而不会把同级字段重置为零。

```bash
curl -X PATCH $BASE/v1/settings -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"maintenance_mode":true}'
```

```json
{ "data": {
  "xray_dns": "https://dns.example/dns-query\n1.1.1.1",
  "decoy_template": "coming-soon",
  "decoy_templates": ["coming-soon", "YouTube", "…"],
  "maintenance_mode": true,
  "probe_detect": true, "probe_block": false,
  "watchdog_enabled": true, "watchdog_restarts": 0,
  "user_autodelete_days": 0,
  "hwid_enabled": false, "hwid_require": true,
  "hwid_fallback_limit": 0, "hwid_ttl_days": 30,
  "device_count_mode": "auto",
  "local_backup_cron": "", "local_backup_keep": 7,
  "sub_path": "sub", "warp_enabled": false, "warp_registered": true,
  "sub_order_mode": "manual", "sub_happ_crypt": false, "sub_page_url": "",
  "trusted_nets": ["198.51.100.0/24", "203.0.113.10/32"]
} }
```

`sub_order_mode` 是订阅对服务器排序的方式：`manual`（按权重，然后按列表顺序）、`nearest`、
`load`、`nearest_load` 或 `random`——每次获取都重新排序，因此连接第一个条目的客户端会分散到
整个集群；隐藏已满和隐藏超量的规则仍然生效。`sub_happ_crypt` 让订阅页面的 Happ 按钮和
`GET /v1/users/{id}/happ-link` 给出加密的 `happ://crypt4/` 链接，而不是明文的地址。
`sub_page_url` 是你自己的订阅页面：浏览器打开订阅链接时会被重定向到那里（`302`），`{token}`
替换为用户令牌——例如 `https://example.com/cabinet?sub={token}`，随后
`GET /v1/users?sub_token=` 即可找到用户。获取订阅的 app 以及 `?format=` 下载不会被重定向；
`""` 会恢复为面板自己的页面。指向本面板自己订阅路径的链接会被拒绝（`err.subPageLoop`），并且
无论用面板可达的任何名称都同样拒绝；未知令牌永远不会被重定向；用户卡片上的“打开订阅页面”按钮
则通过一个有效期为 1 小时的签名链接打开面板自己的页面。`trusted_nets` 是面板永不会自行封禁的 IP
和网段——不会因为猜测 SOCKS/HTTP 密码、不会因为扫描面板、也不会因来源策略而封禁（拒绝仍会记入
日志）。发送它会替换整个列表；条目按前缀存储（`198.51.100.7/24` 变成 `198.51.100.0/24`），
比 IPv4 /8 或 IPv6 /32 更宽的网段会被拒绝，列表中已存在的封禁会被解除。未知的
`sub_order_mode`、错误的 `sub_page_url` 或 `trusted_nets` 条目，会在写入 body 的任何字段之前
被拒绝。

`device_count_mode` 决定用户的设备限制统计什么。`auto`（默认）统计最近两分钟内见过的不同来源
地址——这是唯一能限制同一份订阅**同时**在多少处使用的机制——并且只在用户连续超过上限两分半钟后
才切断，这样换网或运营商更换地址不会影响用户。`hwid` 完全不再统计地址，把已绑定安装的名册变成
唯一限制，并为此放弃并发限制：HWID 限制谁能获取订阅，而不是谁能连接。`both` 在校验两种模式合并
之前的旧数据行时会被接受，其行为等同于 `auto`。其他任何值都会被拒绝，而不会静默回退到默认值。

用户上的 `active_devices` 是窗口内见到的地址的原始数量，无论是否有任何限制正在执行都会报告；
`status` 只有在切断真正生效后才会变成 `device_limited`。

其中三项的效果无法仅由数据库承载——伪装模板、扫描器检测和维护模式在每次请求时都会被查询——因此
API 会和面板一样实时切换它们。未知的 `decoy_template` 会在写入任何内容之前被拒绝；不符合 IP、
`host:port`、URL 或 `localhost` 的 `xray_dns` 条目也会以面板拒绝它的同样方式被拒绝，因为 Xray
无法解析的 DNS 服务器会让之后每一次配置重新生成都失败。

**路由**按服务器设置，服务器 `0` 就是面板所在的机器——与
`/v1/servers/{id}/inbounds` 使用同一套编号。`GET` 和 `POST` 使用相同的结构：

```json
{ "data": {
  "routing": { "block_ads": true, "block_bittorrent": false, "lanes": [], "…": null },
  "xray_dns": "1.1.1.1",
  "warp_enabled": false,
  "opera_enabled": false,
  "opera_country": "EU"
} }
```

省略的字段**保持不变**；`routing` 只要出现，就会整体替换规则集。这种区分是刻意的：合并规则*列表*
没有任何调用方可以预测的含义（更短的列表是删除规则还是保留规则？），而因为 body 没有提到某个
出口后端就把它静默关掉，正是隧道流量开始从服务器自己的 IP 发出的原因。所以，要改规则就发送
`routing`，并且只点名你确实想迁移的后端。

**级联**是一种通道，其上游是我们自己的服务器而不是代理：用户连接到本服务器，再经由 `via` 中的
服务器，各自通过其一条内置连接出去。

```json
{ "id": "nl", "name": "Through NL", "enabled": true,
  "urls": [], "manual": [], "domains": [], "ips": [],
  "via": [ { "server": 0, "bridge": "reality" }, { "server": 3, "bridge": "hysteria2" } ] }
```

`server` 是服务器 id（`0` 是面板自己的机器），绝不能是本服务器。`bridge` 是级联到达出口所用的
出口连接：`reality`（默认）、`vision` 或 `hysteria2`，且必须在该服务器上启用。
`/v1/nodes`（以及 `/v1/nodes/{id}`）中的每台服务器都会列出它们：

```json
"bridges": [
  { "bridge": "reality",   "name": "VLESS-XHTTP-REALITY", "enabled": true },
  { "bridge": "vision",    "name": "VLESS-TCP-TLS",       "enabled": true },
  { "bridge": "hysteria2", "name": "HYSTERIA-UDP",        "enabled": false }
]
```

`name` 是该服务器对这条连接的称呼（运营者可能已经改过名）；只有 `enabled` 的才能使用。把该通道
放在 `routing_order` 的最后，即可让其他所有流量都经过它。

面板自行派生桥接连接的凭据，并且只允许它从本服务器的地址进入出口；用户的流量只在这里计一次。
该通道的流量从不从本服务器发出：没有任何出口被判定为存活时，它仍会走向其中一个；一个出口都没有
时，流量被丢弃。级联通道不接受 `urls` 或 `manual`：它的上游只有出口。
拒绝的情况：`err.cascadeSelf`、`err.cascadeNoServer`、`err.cascadeLaneOff`（该连接在那个
出口上是关闭的）、`err.cascadeLoop`（某个出口自己的级联又绕回这里）、`err.laneBadBridge`。
只有本次保存新增或改动的通道会被校验，因此某个后来消失的出口不会阻塞其他编辑。在出口一侧，关闭
某个级联正在使用的连接会被拒绝（`err.cascadeExitLaneOff`），关闭或删除该服务器也会被拒绝
（`err.cascadeExitInUse`）。

**快照**把整台服务器的配置——协议、端口、REALITY、路由、出口、DNS、伪装和自定义入站——捕获为
一个存档点。回滚会恢复以上全部，*唯一例外*是证书和域名，因此撤销永远不会破坏正在使用的证书；并在
此之前自动创建当前状态的存档点，所以回滚本身也可以撤销。它会重新生成配置并重启 Xray；由于节点
会继承主服务器的字段，这次重启是全集群范围的，所有现有连接都会断开。

```bash
curl -X POST $BASE/v1/config/snapshots -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"label":"before the egress change"}'
```

响应按 id 携带本次调用创建的存档点——而不是“最新的那个”，如果期间有别的创建操作落地，那就会是
别人的快照。

`xray-restart` 对服务器 `0` 是直接的，对节点则排队，节点会在下次同步时取走——那里的 `200`
意味着*已接受*，而不是*已完成*。

### 统计

```
GET $BASE/v1/stats/series?user_id=5&from=2026-01-01&to=2026-01-31        → 每日流量
GET $BASE/v1/stats/nodes?user_id=5&from=2026-01-01&to=2026-01-31         → 每台服务器的合计
GET $BASE/v1/stats/nodes/series?user_id=5&from=2026-01-01&to=2026-01-31  → 按服务器分列的每日数据
GET $BASE/v1/stats/users?from=2026-01-01&to=2026-01-31                   → 每个用户的合计
```

`user_id` 把 `series`、`nodes` 和 `nodes/series` 缩小到单个账户；省略它得到全面板的数字。
`users` 没有 `user_id`——它本来就是按用户返回的。
`from`/`to` 是面板配置时区中的 `YYYY-MM-DD`，两者都默认为**最近 30 天**——省略窗口只是采用
一个窗口，而不是请求空结果。格式错误或颠倒的范围返回 400，而不是空答案。

范围内的每一天都会出现，包括什么都没承载的日子：

```json
{ "data": [
  { "day": "2026-01-01", "up": 52711616, "down": 3901246326 },
  { "day": "2026-01-02", "up": 0, "down": 0 },
  { "day": "2026-01-03", "up": 46059475, "down": 1488367869 }
] }
```

安静的一天在内部不写任何记录，因此存储的历史是繁忙日期的列表，而不是一条序列；这些端点会填补
空缺，因为对任何计算移动平均值的东西来说，一个空洞和一个零是不同的形状。

`nodes` 把同样的流量按承载它的服务器拆分，最繁忙的在前——`series` 告诉你多少，这个告诉你
在哪里。面板自己服务器的 `node_id` 为 `0`；名称会为你解析，包括此后被删除的服务器（它们的
流量记录比服务器活得更久）。

```json
{ "data": [
  { "node_id": 2, "name": "NL", "up": 46059475, "down": 1488367869 },
  { "node_id": 0, "name": "Мастер", "up": 52711616, "down": 3901246326 }
] }
```

`nodes/series` 同时给出两个维度——每天每台服务器一行，因此一次调用就能为每台服务器画一条线：

```json
{ "data": [
  { "day": "2026-01-01", "node_id": 0, "name": "Мастер", "up": 52711616, "down": 3901246326 },
  { "day": "2026-01-01", "node_id": 2, "name": "NL",     "up": 46059475, "down": 1488367869 },
  { "day": "2026-01-02", "node_id": 0, "name": "Мастер", "up": 0,        "down": 0 },
  { "day": "2026-01-02", "node_id": 2, "name": "NL",     "up": 12000000, "down": 340000000 }
] }
```

在整个窗口内什么都没承载的服务器会完全省略；出现的服务器则每天都有记录。

连接来自哪里，而不是承载了多少：

```
GET $BASE/v1/stats/countries  → 最近连接按国家分组
GET $BASE/v1/stats/asns       → 同样，按网络运营商分组
```

两者都统计连接保留窗口内的**不同来源 IP** 及其活跃度，最繁忙的在前。没有路由表覆盖的地址
（私有或未路由的），`asn` 为 `0`、`org` 为空：

```json
{ "data": [
  { "asn": 15169, "org": "GOOGLE", "ips": 23, "hits": 2548 },
  { "asn": 13335, "org": "CLOUDFLARENET", "ips": 4, "hits": 61 }
] }
```

每个答案都从整张连接表计算而来，因此两者都会缓存约半分钟——每秒两次调用得到相同的数字，面板
则把唯一的数据库连接留给其他所有事情。

### 各种合计分别意味着什么

存在两个系列的计数器，它们回答的是不同的问题。把它们混为一谈，看起来就完全像一个重复计数的
bug：

| 数字 | 来源 | 何时重置 |
|---|---|---|
| `/v1/summary` 上的 `total_up` / `total_down` | 用户的配额计数器 | 流量重置，或套餐滚动到下一个周期 |
| `/v1/stats/series`、`/v1/stats/nodes` | 每日流量历史 | 从不——它持续累积 |

因此，summary 的合计是**当前配额周期内的用量**，也就是流量限额所衡量的对象；它们经常*小于*
同一时间跨度的历史记录。这不是流量失踪：而是某个用户的计数器被重置了，而历史仍然保留。计费以
配额计数器为准，报表以历史为准，不要指望两者一致。

```
GET $BASE/v1/stats/abuse?limit=50   → 整个集群最近的黑名单命中
```

### 日志流

两条只读的轨迹。**事件**是用户身上发生的事；**管理员审计**是管理员和 API 密钥对面板做了什么
——包括通过此 API 完成的一切，归属到密钥的名称。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/events` | 用户事件，可过滤。 |
| `GET` | `/v1/events/catalog` | 一条记录可以携带的事件键。 |
| `GET` | `/v1/admin-audit` | 管理员轨迹。 |
| `GET` | `/v1/admin-audit/catalog` | 它的类别以及每类中的操作。 |

两者都**向前翻页**：传入 `before=<你手上最旧一行的 id>`，并从响应中读取 `next_before`——`0`
表示已经到达末尾。id 单调递增，因此在新行落到顶部时游标依然稳定。

```json
{ "data": { "events": [ { "id": 812, "user_id": 5, "action": "plan.cancelled", … } ],
            "next_before": 780 } }
```

`/v1/events` 接受 `action`（目录中的一个键）、`actor`（`admin` / `user` / `system` /
`api`）、`user_id` 和分页那一对参数。`/v1/admin-audit` 接受 `category`（展开为它包含的
操作）、`action`、`actor` 和分页那一对参数。未知的 `action` 或 `category` 返回 `400`，
而不是空页——一个静默匹配不到任何内容的过滤器，与一段平静期无法区分。

### Webhook

集成的推送部分，可以从集成本身进行配置。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/webhooks` | 列出已配置的端点。 |
| `GET` | `/v1/webhooks/events` | Webhook 可以订阅的事件键。 |
| `POST` | `/v1/webhooks` | 添加端点。 |
| `POST` | `/v1/webhooks/{id}` | 更新一个端点（整个对象）。 |
| `DELETE` | `/v1/webhooks/{id}` | 删除一个端点。 |
| `POST` | `/v1/webhooks/{id}/test` | 发送一次测试投递。 |

Body：`{ "url": "https://…", "events": ["user.created"], "enabled": true }`。空的
`events` 数组表示**全部**事件。`enabled` 在创建时可选（默认为开）。投递格式、签名和重试策略
见下文的 **Webhook** 一节。

测试投递即使端点失败也会返回 `200`——调用成功，投递才是结果：
`{ "data": { "status": 502, "ok": false, "error": "…" } }`。

### 注册申请

审核模式下的注册队列（只有在自助注册处于审核模式时才有意义——`moderation` 说明是否如此）。
一条请求要么属于某个 Telegram 聊天（`chat_id`），要么属于某个外部系统客户端（`external_id`，
来自 `POST /v1/signup`）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/v1/registrations` | 待处理的注册。 |
| `POST` | `/v1/registrations/{id}/approve` | 创建账户并绑定其 Telegram 聊天或外部系统 id。 |
| `POST` | `/v1/registrations/{id}/reject` | 丢弃该请求。 |

### 监控

```
GET $BASE/v1/summary          → 用户 / 在线 / 流量合计 / Xray 与证书状态
GET $BASE/v1/system           → 实时 CPU / 内存 / 磁盘 / 网络 / VPN 吞吐
GET $BASE/v1/system/auto-update       → {"cron": "0 4 * * *", "nodes": true, "last_at": …, "last": "latest"}
POST $BASE/v1/system/auto-update {"cron": "0 4 * * *"}  ("" 表示关闭；节点始终跟随)
GET $BASE/v1/health/report    → 完整自检（Xray、配置、TLS、Geo、出口线路）
GET $BASE/v1/nodes/{id}/health → 单台服务器的诊断（id 0 = 主服务器）
POST $BASE/v1/nodes/{id}/reach → {"host": …, "port": 443, "countries": [{"code": "RU", "ok": 9, "blocked": 1, "failed": 0, "probes": […]}]}
POST $BASE/v1/reach {"countries": ["RU", "CN", "IR"], "probes": 10, "every": 60, "threshold": 50}  (every 0 = 仅在需要时)
GET $BASE/v1/nodes/{id}/logs   → 节点的最近日志行
GET $BASE/v1/logs              → 面板自身的日志行
GET $BASE/v1/backup/info       → 现在做一次备份会包含什么
GET $BASE/v1/backup            → 下载该备份（.tar.gz 内容，不是 JSON）
GET $BASE/v1/metrics           → Prometheus 导出格式（文本，不是 JSON）
```

#### 故障与维护（状态页面）

```
GET    $BASE/v1/incidents                → {"incidents": [...], "servers": [{"id": 0, "name": "Main"}, ...]}
POST   $BASE/v1/incidents                {"kind": "maintenance", "title": "Works on NL", "body": "…",
                                          "node_ids": [2], "starts_at": 1767225000, "ends_at": 1767236000,
                                          "notify": true, "lang": "en"}          → 201 {"incident": {...}}
POST   $BASE/v1/incidents/{id}           同样的 body：重写它（是否已解决会被保留）
POST   $BASE/v1/incidents/{id}/updates   {"stage": "identified", "body": "Found it", "notify": true}
POST   $BASE/v1/incidents/{id}/resolve   {"note": "Fixed", "notify": true}       （body 可选）
POST   $BASE/v1/incidents/{id}/compensate {"hours": 3, "notify": true}         (users.manage)
DELETE $BASE/v1/incidents/updates/{id}
DELETE $BASE/v1/incidents/{id}
```

`kind` 是 `incident`（一直开放到被解决）或 `maintenance`（在 `starts_at` 之前为计划状态，
之后一直进行到 `ends_at`——0 表示直到被解决）。`node_ids` 为空表示整个服务；这些 id 就是
`servers` 列出的那些。`hide_in_sub`（仅维护）会在窗口进行期间把点名的服务器从订阅中移除——
除非那会让订阅一台服务器都不剩。更新项的 `stage` 是 `investigating`、`identified` 或
`monitoring`（仅故障适用）或 `update`（两者皆可，为默认值）；解决时给出的 `note` 会以
`resolved` 步骤关闭整个进展记录。`notify` 会通过用户 bot 把它发给所有拥有有效订阅的用户，需要
群发权限；无法发出时，故障仍会被保存，而 `notify_error` 会说明原因。尚未开始的维护不能解决
——请移动或删除它。

`compensate` 为已结束的条目补回时间，且只补一次：`hours`（1–2160）会加到每个已启用用户的订阅
上，前提是该用户的期限在条目开始时正在运行，且他可以使用条目点名的某台服务器。谁在当时拥有正在
运行的期限，会在条目开始时记录（发布时，或维护窗口开启时；`snapshot_at`），因此之后续费的用户
得不到任何补偿。此后已到期的期限从当前时间起加上这些小时；没有结束时间的期限不处理。随后故障会
携带 `compensated_at`、`compensated_seconds` 和 `compensated_users`；`notify` 会通过用户
bot 逐一告知这些用户，需要群发权限。

`/v1/metrics` 是 Prometheus 的抓取目标，用同一把 API 密钥认证：

```yaml
scrape_configs:
  - job_name: rospanel
    scheme: https
    metrics_path: /<api-path>/v1/metrics
    authorization:
      credentials: rp_...
    static_configs:
      - targets: ["vpn.example.com"]
```

它发布用户数、累计流量与每日流量、实时吞吐、Xray 与证书状态，以及面板宿主自身的 CPU/内存/磁盘。

每台服务器也都有自己的序列，带有 `node` 和 `node_id` 标签（主服务器是 `node_id="0"`）：
`rospanel_node_online`、`rospanel_node_enabled`、`rospanel_node_xray_running`、
`rospanel_node_last_seen_seconds`、`rospanel_node_traffic_today_bytes{direction=…}`，
以及它下面的机器——`rospanel_node_cpu_percent`、
`rospanel_node_memory_bytes{state=…}`、`rospanel_node_disk_bytes{state=…}` 和
`rospanel_node_host_uptime_seconds`。从未上报过的节点（或 agent 早于这些字段的节点）单纯没有
样本，这正是图表中的空档应该表示的含义——而不是一行会被读成空闲机器的零。

这里刻意**没有按用户的序列**：一千个用户的面板会为每个指标产生一千条时间序列。

`/v1/nodes/{id}/logs` 由节点的下一次长轮询来应答，因此刚被唤醒的节点可能返回上一批日志——
`at` 是这些行被收集的 Unix 时间。

`/v1/backup` 直接流式输出归档本身（`Content-Type: application/gzip`），因此它是
`{"data": …}` 信封之外唯一的端点——把调度器指向它即可在机器之外保留副本。**恢复被刻意排除**：
它在磁盘上暂存，并在下次启动时应用，所以放在 API 上就会变成一个在下次重启时静默替换所有内容的
请求。

## 订阅响应头

`GET /v1/users/{id}/subscription` 返回地址；客户端实际拉取的那份响应，携带了应用无需解析
配置即可显示的内容：

| 响应头 | 含义 |
| --- | --- |
| `Profile-Title` | 标题，`base64:` 前缀之后的 base64 |
| `Profile-Update-Interval` | 两次刷新之间的小时数；面板设为“从不”时不下发 |
| `Subscription-Userinfo` | `upload=…; download=…; total=…; expire=…`，字节数与 Unix 秒；`total=0` 表示不限，`expire=0` 表示不到期 |
| `Profile-Web-Page-Url` | 用户的页面，存在时才下发 |
| `support-url` | 运营者的支持或 Telegram 链接，设置后下发 |
| `Announce` | 运营者的公告，带 `base64:` 前缀；Happ 和 v2RayTun 会把它显示为一行 |
| `Moved-Permanently-To` | 这条订阅迁移到的地址，用户的 token 已填入 |

`Moved-Permanently-To` 就是迁移域名：在 **设置 → 订阅** 中填一次地址，把 `{token}` 放在
token 该在的位置（`https://new.example.com/sub/{token}`），此后每个读取该响应头的客户端都会
从新地址刷新——用户不必重新导入链接。忽略该响应头的客户端仍可通过旧地址继续工作。当面板
重新成为客户端应该访问的地址时，清空该字段。

开启自动路由时，Happ 和 INCY 还会收到 `routing`（deeplink 本身，而不是它的 URL）和
`routing-enable: 1`；Clash 和 Mihomo 则改为在 YAML 里收到规则。

## MCP（AI 助手）

面板通过 Model Context Protocol 本身把此 API 提供给助手。无需在任何地方安装任何东西：一个接受
URL 的助手即可：

```
$BASE/v1/mcp/<api-key>
```

密钥走在路径中，因为那些对话框只接受 URL，不接受别的：**地址就是凭据**，与其中的密钥同样机密，
并且在密钥被撤销的那一刻失效。请你用 *设置 → API* 给出的两个值手工拼接它——那里显示的基础地址，
以及你创建密钥那一刻的密钥（它不会再显示第二次）。

工具箱就是密钥的权限：助手只会被提供该密钥可调用的路由背后的工具，调用其他任何工具都会得到
“unknown tool”。对于只应查看的助手，请创建一把没有勾选任何写权限的密钥——这样，即使某个助手
因为误读一句话而行动，无论被要求做什么，都无法删除一个客户。

传输层是 MCP 的 Streamable HTTP：每个 `POST` 一条 JSON-RPC 消息，以 `application/json`
应答；通知得到 `202` 和空 body；`GET` 返回 `405`（本端点没有服务器发起的流）。调用在进程内派发
到面板自己的 REST API，因此它们携带与其他任何 `/v1` 客户端相同的权限、相同的审计轨迹和相同的
错误结构。

### 响应大小

不带 `limit` 的列表调用会得到 **50 行**，而不是 REST 默认的 100：助手要为上下文中的每一行付费。
显式要求更多就能得到更多，包括 `limit=0`（全部）。

其上是每次调用 **512 KB** 的硬上限。超出它的响应会按整行裁剪，并由 `meta` 说明——`limit`
变为实际返回的行数，而 `total` 仍报告总共存在多少，因此按 `limit`/`offset` 分页即可取到其余部分。
结果仍然是合法的 JSON。

**备份相关接口完全不作为工具提供。** `GET /v1/backup` 因为其 body 是助手读不了的 tar 包，
`GET /v1/backup/info` 因为知道一份转储会包含什么，只对即将创建它的人有用——那是运营者的工作，
在面板里完成，因为文件实际落在某个地方。恢复不以任何形式存在于 `/v1` 中：它位于面板之后，需要
一个会话和重新输入的密码，因此任何密钥，以及任何持有密钥的助手，都无法把备份放回去。

工具列表由上面的 OpenAPI 文档生成，因此永远不会与 API 偏离：加入 `/v1` 的端点会自动成为工具，
无需任何人记得注册；被移除的端点则会消失。这也包括配置那一半——密钥允许的助手可以读取和修改
设置、重写服务器的路由、创建和回滚配置存档点，以及重启 Xray。

每个工具都带有客户端用来判断是否先询问人类的注解（`readOnlyHint`、`destructiveHint`）。哪些
写入算破坏性，由**路由声明**，而不是从其措辞猜测——被重写的句子不得能关掉警告，而改道流量、
重启 Xray、回滚配置或让面板进入维护的那些调用都被标记。回答以文本返回，当面板回复 JSON 时也以
`structuredContent` 返回；单个回答设有上限，因此一次调用无法填满助手的上下文——请要求更小的
分页（`limit`/`offset`），而不是全部。

## 示例

创建用户：

```bash
curl -sS -X POST "$BASE/v1/users" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"alice","data_limit":0,"expire_at":0}'
```

获取用户的订阅 URL：

```bash
curl -sS "$BASE/v1/users/5" -H "Authorization: Bearer $KEY" \
  | jq -r '.data.sub_url'
```

删除用户：

```bash
curl -sS -X DELETE "$BASE/v1/users/5" -H "Authorization: Bearer $KEY"
```

---

# Webhook

除了轮询 API，你还可以让面板把生命周期事件**推送**到你自己的 HTTP 端点。在面板中配置它们 →
**设置 → API → Webhook**（添加接收方 URL 并勾选你想要的事件——一个都不勾 = 全部），或者直接
通过 API：`POST /v1/webhooks`。

与 API 的出站请求不同，Webhook 目标可以是 `http` **或** `https`，并且**可以指向私有或
localhost 主机**——接收方往往是运营者自己的内部服务，而每次投递都是单向 POST（响应 body 永远
不会被读取）。

## 事件

| 事件 | 触发时机 |
| --- | --- |
| `user.created` | 用户被创建（面板或 API） |
| `user.deleted` | 用户被删除 |
| `registration.requested` | 一次注册在等待运营者处理（审核模式）：`data.request_id`、`data.name`，以及 `data.telegram_id` 或 `data.external_id` |
| `user.registered` | 用户自助注册：Telegram 用户 bot、Mini App 或 `POST /v1/signup`（`external_id` 或 `telegram_id`）；若此时 Telegram 已绑定则有 `data.telegram_id`（以 `telegram_id` 注册、审核批准——`data.moderated`、`data.request_id`）——通过 bot 或 Mini App 注册的，会在紧接着的 `user.telegram_linked` 中绑定 |
| `registration.rejected` | 一次审核中的注册在没有自己账户的情况下结束（`telegram_id` 或 `external_id`）：`data.reason` 为 `operator`（被拒绝）或 `has_account`（已批准，但该 Telegram 或 `external_id` 已有账户——`data.user_id`） |
| `user.expired` | 订阅到期失效 |
| `user.limited` | 用户用尽了流量配额 |
| `user.device_limited` | 用户超出设备数限制——或有新设备在达到上限时被拒绝（`data.refused`、`data.device`） |
| `user.expiring` | 期限即将结束——在结束前 14、7、3 和 1 天各触发一次，每个期限各一次（续费会重新开始计数；剩余天数更少时给出的期限只会得到最近的那个阶段）；`data.stage`（14、7、3 或 1）、`data.days_left`——请对你需要的阶段作出反应；使用付费套餐时余额可以续费，`data.auto_renew`、`data.renew_price_kop`、`data.balance_kop` |
| `user.traffic_low` | 用户已用掉 80% 的配额——只触发一次，直到用量回落到该线以下；`data.used`、`data.percent` |
| `user.sub_rotated` | 订阅链接被重新签发（面板、API 或 Telegram 迁移）——旧链接不再可用；`data.sub_url` |
| `user.uuid_reset` | 在面板中重新签发用户的 UUID、协议密码和 WireGuard 密钥（AmneziaWG、TURN-VK）——用旧值构建的链接和配置失效；订阅链接保留 |
| `user.telegram_linked` | 有 Telegram 被绑定到账户（面板、API、bot、Mini App）；`data.telegram_id` |
| `user.telegram_unlinked` | 账户失去某个 Telegram——被解绑、聊天被移到另一个账户，或账户被移到另一个 Telegram；`data.telegram_id` |
| `user.limits_changed` | 运营者修改了限额或期限，或延长了期限（批量延长时 `data.extended_days`），或退款收回了订单的天数（`data.refund_order_id`） |
| `user.term_started` | 等待首次连接的期限开始了——`data.expire_at` 现已设置 |
| `user.traffic_reset` | 流量计数被归零——手工归零（`data.auto` 为 false）或按重置周期归零（`data.auto` 为 true） |
| `user.device_bound` | 有新设备占用了名额（`data.device`、`data.devices`、`data.device_limit`） |
| `user.device_unbound` | 设备名额被释放——一个（`data.hwid`）、全部，或因链接被重新签发（`data.reason`）；`data.devices` |
| `user.enabled` / `user.disabled` | 运营者开启或关闭用户（面板、API、批量操作） |
| `user.abuse` | 黑名单措施：`data.measure` 为 `warned`、`throttled`（`speed_limit`）、`disabled`（`until`）或 `lifted`（`was`） |
| `plan.changed` | 套餐由运营者或 API 设置，或作为套餐变更而购买；`data.plan`、`data.prev_plan` |
| `plan.downgraded` | 付费期限结束，免费套餐接管；`data.plan`、`data.prev_plan` |
| `plan.cancelled` | 付费套餐被放弃——转为免费套餐（`data.plan`）或立即结束（`data.plan` 为空）；`data.prev_plan` |
| `balance.adjusted` | 运营者修正了余额；`data.amount_kop`（正负均可）、`data.balance_kop` |
| `user.referred` | 用户通过某人的邀请注册；`data.referrer_id` |
| `referral.reward` | 被邀请用户的付款为邀请人（`data` 中的用户）带来奖励；`data.reward_kop`、`data.reward_days`、`data.banked`、`data.referred_user_id`、`data.order_id` |
| `promo.winback` | 流失用户得到个人优惠码；`data.code`、`data.percent`、`data.expires_at`、`data.attached`（已附加到其下次付款） |
| `user.churn_answer` | 流失用户说明了为何没有续费；`data.reason`、`data.comment`（写了才有）、`data.source`（`bot`、`page` 或 `api`） |
| `promo.redeemed` | 用户输入的优惠码生效：`data.code`、`data.kind`（`balance`、`days`、`percent`、`amount`）、`data.value`、`data.plan`（天数）、`data.balance_kop` |
| `payment.created` | 支付订单被创建 |
| `payment.paid` | 订单已支付且套餐已应用；`data.renewal` 表示从余额自动续费，`data.undelivered` 表示套餐无法应用、钱进入了余额（此时 `kind` 为 `topup`） |
| `payment.cancelled` | 订单被取消 |
| `payment.refunded` | 订单的钱被退回——由运营者退回余额（`data.refund_kop`），或由支付系统退回（`refund_source`；`data.taken_kop`、`data.returned_kop`）；`data.plan_cancelled` 表示它买到的访问权已被收回 |
| `user.message` | 运营者从面板给用户写了消息：`data.text`（Telegram HTML）、`data.buttons`（`text`、`url`）、`data.media_kind`/`data.media_name`（文件只发往 Telegram）、`data.telegram_sent`——在 bot 没有送达时为 false（没有 Telegram、bot 被封禁、bot 关闭），此时由你负责送达。订阅此事件后，面板可以在没有 Telegram 的情况下给用户写信 |
| `user.auto_message` | 某条自动消息到期：`data.rule_id`、`data.rule`、`data.trigger`、`data.text`（Telegram HTML，变量已填充）、`data.buttons`、`data.code`/`data.percent`/`data.code_expires_at`（个人折扣）、`data.telegram_sent`。订阅此事件后，规则也会覆盖 bot 触达不到的账户——bot 关闭时是全部账户——但在 bot 中关闭了邮件的用户除外；每个账户每次触发只会收到一条规则，无论通过哪种途径 |
| `user.mailing` | 为该用户开启或关闭邮件——通过 bot 的开关、运营者或 `PATCH /v1/users/{id}`；`data.mailing` |
| `node.down` | 服务器停止上报；`data.node_id`、`name`、`host`、`last_seen` |
| `node.up` | 服务器恢复；`data.down_seconds` |
| `xray.down` | 某台服务器上的 Xray 停止（`data.node_id` 为 0 表示主服务器）；主服务器上还有 `data.reason` |
| `xray.up` | Xray 恢复；`data.down_seconds` |
| `alert.fired` | 某台服务器上的告警规则触发；`data.node_id`、`name`、`host`、`rule_id`、`rule_name`、`metric`、`op`、`threshold`、`value`、`fired_at` |
| `alert.resolved` | 告警恢复；同样的字段再加上 `peak`、`resolved_at`、`duration_seconds` |
| `incident.created` | 发布了一条故障或维护窗口；`data` 是该故障（`id`、`kind`、`title`、`body`、`node_ids`、`servers`——点名服务器的名称，以逗号分隔，整个服务时为 `""`——`starts_at`、`ends_at`、`resolved_at`、`phase`、`updates`、`status_url`） |
| `incident.updated` | 它被编辑、发布了进展中的一步（`data.update`：`stage`、`body`、`created_at`），或撤回了某一步（没有 `data.update`；`data.updates` 是当前的进展记录） |
| `incident.resolved` | 它被解决，或维护被标记为结束；只是在 `ends_at` 自然关闭的窗口不会发送任何内容 |
| `incident.deleted` | 它被删除；`data` 是它原来的内容 |
| `broadcast.sent` | 一次群发已发出——每次群发一条投递：`data.id`、`data.text`、`data.buttons`、`data.audience`、`data.group_id`（仅在限定到访问组时）、`data.media_kind`/`data.media_name`、`data.telegram_recipients`（bot 要发送到的聊天）、`data.users`（`id`、`external_id`）——受众中 bot 触达不到的账户，但在 bot 中关闭了邮件的用户除外。订阅此事件后，群发在 bot 关闭时也能工作（仅文本） |

## 投递格式

每次投递都是一个带 JSON body 的 HTTP `POST`：

```json
{
  "id": "3f1c…",                 // unique delivery id
  "event": "user.created",
  "created_at": 1767225600,
  "data": { "id": 7, "name": "alice", "status": "active", "enabled": true,
            "expire_at": 0, "data_limit": 0, "plan_id": 0,
            "external_id": "", "telegram_id": 0, "mailing": true, "lang": "ru" }
}
```

对于所有关于用户的——`user.*`、`plan.*`、`balance.adjusted`、`referral.reward`（邀请人）、
`promo.*`——`data` 都是用户对象（`id`、`name`、`status`、`enabled`、`expire_at`、
`data_limit`、`plan_id`、你用来辨认该用户的 id——`external_id`（没有时为 `""`）和
`telegram_id`（没有时为 0）——`mailing` 和 `lang`（未知时为 `""`）），再加上事件表中为该
事件列出的字段；对于 `payment.*` 是支付订单，并带 `data.user`（该用户对象，处于订单离开它时的
状态）和 `data.user_balance_kop`（其现在的余额；订单自己的 `balance_kop` 是余额覆盖的那部分
价格）；对于 `registration.requested` 和 `registration.rejected` 是请求（`request_id`、
`name`、`telegram_id` 或 `external_id`）；对于 `broadcast.sent` 是群发内容。

请求头：

```
Content-Type: application/json
User-Agent: RosPanel-Webhook/1
X-RosPanel-Event: user.created
X-RosPanel-Signature: sha256=<hex HMAC-SHA256 of the raw body>
```

## 校验签名

每个 Webhook 都有一个密钥（在面板中显示）。对**原始请求 body** 重新计算 HMAC，并以常量时间比较：

```python
import hmac, hashlib

def verify(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

```js
import crypto from "node:crypto";

function verify(secret, body, header) {
  const expected = "sha256=" + crypto.createHmac("sha256", secret).update(body).digest("hex");
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(header));
}
```

## 重试与投递

返回 `2xx` 状态表示已确认。非 2xx 响应或连接错误会以逐渐增大的退避重试（大约 10s、30s、2m、
10m——最多 5 次），然后被丢弃。待处理的投递保存在面板的数据库中，因此对数千用户执行的批量操作
会发出每一个事件，重启也会补发当时在等待的内容。投递可能**乱序**到达，并且在重试或重启后可能
**重复**到达——请把 `id` 字段当作幂等键。面板中的 **Test** 按钮会发送一次 `ping` 投递，以便你
确认可达性和签名校验。每个 Webhook 旁边会显示最近一次投递的状态。
