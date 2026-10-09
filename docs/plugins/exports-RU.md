# Точки расширения

[English version](exports.md) · [Оглавление](README-RU.md)

Панель вызывает функции, которые экспортирует `main.js`. Какие из них она будет звать,
решает `provides` в манифесте ([манифест](manifest-RU.md)). На этой странице описано, когда
вызывается каждый экспорт, что он получает, что должен вернуть и что будет при ошибке.

Общие правила для всех:

- **Аргумент и ответ — JSON.** Функция получает один аргумент, а вернуть может любое
  JSON-значение. `undefined` и функции в ответе теряются.
- **Один вызов за раз.** Вызовы плагина обслуживаются в порядке поступления: пока он
  выполняет один, следующий ждёт. Вызов, который ждёт дольше своего времени, завершается как
  «занят». Поэтому тяжёлая задача cron задерживает события этого плагина, а решения
  (регистрация, цена) и меню бота её не ждут: если плагин занят, они считают, что плагин не
  возражает.
- **Время ограничено.** Сверх лимита вызов прерывается ошибкой `interrupted`.
- **Сбои останавливают плагин.** После 10 неудачных вызовов подряд панель останавливает
  плагин и сама пробует запустить его снова ([подробнее](troubleshooting-RU.md#когда-панель-останавливает-плагин)).
  Сбои `onHttp` и `webhook` оплаты пишутся в лог, но не считаются.
- **Точкам нужны права.** У каждой точки ниже указано право, которое ей нужно; манифест без
  него не проходит проверку.
- **Память не сохраняется между вызовами.** Глобальные переменные живут, пока жива VM, а
  панель может пересоздать её в любой момент: после смены настроек, при росте памяти, после
  ошибки. Состояние храните в `panel.kv` или `panel.db`.

| Экспорт | Когда | Время |
|---|---|---|
| `onEnable` | при включении и при каждом старте панели | 10 с |
| `onEvent` | событие панели | 10 с |
| cron-задача | по расписанию | 60 с |
| `payment.*` | оплата | 15 с |
| `onHttp` | запрос по адресу плагина | 10 с |
| `userFields` | открыта карточка пользователя | 0,3 с |
| `onAction` | админ нажал кнопку плагина | 30 с |
| `widget` | открыт обзор | 2 с |
| `subBlocks` | открыта страница подписки | 0,3 с |
| `channel.send` | сообщение, которое бот не доставил | 15 с |
| `beforeSignup`, `beforeDeviceBind`, `quotePrice` | решение панели | 0,3 с |
| `bot.menu` | меню пользовательского бота | 0,3 с |
| `bot.onCallback`, `bot.onCommand` | нажатие или команда в боте | 3 с |
| `transformSubscription` | клиент обновляет подписку | 0,3 с |

## onEnable

```js
export function onEnable() {
  if (!panel.kv.get("installed_at")) panel.kv.set("installed_at", Date.now());
}
```

Необязательный экспорт. Вызывается после миграций, когда плагин включают, и при каждом старте
панели для включённых плагинов. Если `onEnable` бросает исключение, плагин не включится, а
текст ошибки увидит оператор. Здесь удобно проверить настройки (например, токен — запросом к
сервису) и подготовить данные.

## События — onEvent

```json
"permissions": ["users.view", "billing.view"],
"provides": {"events": ["user.created", "payment.paid"]}
```

```js
/** @param {PanelEvent} e */
export function onEvent(e) {
  // e = {id: "3f1c…", event: "payment.paid", created_at: 1767225600, data: {...}}
  if (e.event === "payment.paid") panel.log.info("оплата", e.data.amount_rub);
}
```

- **Права.** `users.view`; для событий `payment.`, `plan.`, `balance.`, `referral.` и
  `promo.` ещё `billing.view`; событиям `node.*` права не нужны.
- **Формат и поля.** `e.data` то же, что у вебхуков панели. Полный список событий и их полей —
  в разделе «Events» [docs/api.md](../api.md#events).
- **Доставка хотя бы один раз.** Одно и то же событие может прийти дважды. Используйте `e.id`
  или уникальный ключ в своей базе, как в [уроке](tutorial-RU.md).
- **Повторы при ошибке.** Если `onEvent` бросил исключение, событие придёт снова через 10 с,
  30 с, 2 мин и 10 мин — всего до 5 попыток.
- **Порядок не гарантирован** между событиями разных пользователей и при повторах.
- **Зацикливание.** Если плагин три минуты подряд получает больше 300 событий в минуту и сам
  делает больше 100 изменений в минуту через `panel.api`, панель его останавливает: так ведёт
  себя плагин, который отвечает на собственные изменения (на `user.limits_changed` меняет
  лимиты). Массовые события панели — сброс трафика у тысяч пользователей — плагин, который их
  только читает, не останавливают.

Часто нужные события:

| Событие | Когда |
|---|---|
| `user.created`, `user.registered` | пользователь создан в панели или зарегистрировался сам (бот, Mini App, сайт) |
| `user.expiring` | до конца подписки 14, 7, 3 и 1 день (`data.stage`) |
| `user.expired`, `user.limited` | подписка кончилась; трафик исчерпан |
| `user.traffic_low` | израсходовано 80% трафика |
| `payment.created`, `payment.paid`, `payment.refunded` | заказ создан, оплачен, возвращён |
| `plan.changed`, `plan.cancelled` | тариф сменился или отменён |
| `user.device_bound`, `user.device_limited` | новое устройство; упёрся в лимит |
| `user.telegram_linked` | к аккаунту привязан Telegram |
| `node.down`, `node.up`, `xray.down`, `xray.up` | сервер или Xray пропал и вернулся |
| `user.message`, `broadcast.sent` | сообщение или рассылка от оператора |

## Задачи по расписанию

```json
"provides": {"cron": [{"name": "sync", "schedule": "*/15 * * * *"}]}
```

```js
export function sync() { /* … */ }
```

- **Расписание** — пять полей в часовом поясе панели (см. [манифест](manifest-RU.md)).
- **Без наложений.** Если предыдущий запуск ещё идёт, новый пропускается.
- **Ошибки.** Исключение попадает в лог и считается ошибкой; повтора нет — задача просто
  запустится в следующий раз по расписанию.

## Способ оплаты — payment

```json
"permissions": ["payments.manage"],
"provides": {"payment": {"label": {"ru": "Оплата картой (Lava)", "en": "Card (Lava)"}, "note": "…"}}
```

```js
export const payment = {
  create({ amount_rub, order_id, description, return_url, webhook_url, email }) {
    // создать счёт у платёжной системы
    return { provider_id: "inv-123", pay_url: "https://pay.example/inv-123" };
  },
  status(provider_id) {
    return { status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
  webhook({ body, headers }) {
    // проверить подпись! headers — имена в нижнем регистре
    return { provider_id: "inv-123", status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
};
```

Способ появляется в **Настройки → Оплата** как `plugin.<id>`, оператор включает его как
любой другой.

| Функция | Получает | Возвращает |
|---|---|---|
| `create` | `amount_rub` (целые рубли), `order_id`, `description`, `return_url` (куда вернуть плательщика), `webhook_url` (куда платёжной системе слать уведомления), `email` (может быть пустым) | `{provider_id, pay_url}`: id счёта и http(s)-ссылка на оплату |
| `status` | `provider_id` (строка) | `{status, amount_kopecks, currency}` |
| `webhook` | `{body, headers}` — тело уведомления строкой и заголовки | `{provider_id, status, amount_kopecks, currency}` |

- **Адрес уведомлений:** `https://<панель>/<секретный путь>/plugin.<id>` — это `webhook_url`
  в `create`, он же показан в карточке способа оплаты. Пока у панели не задан домен, адрес
  пустой. Если платёжная система принимает адрес вместе со счётом, передайте его там, и
  оператору ничего не придётся вписывать в её кабинет. Если не принимает — оператор
  копирует адрес из карточки в кабинет.
- **Право:** `payments.manage`.
- **Статусы:** `paid`, `pending`, `cancelled`, `refunded`.
- **Сумма обязательна.** Для `paid` и `refunded` нужны `amount_kopecks` и `currency`. Тариф
  выдаётся, только если сумма и валюта совпадают с заказом.
- **Проверка уведомлений.** `webhook` обязан проверить, что уведомление настоящее
  (`panel.crypto.hmac`/`verify`). Если платёжная система ничего не подписывает — вызовите
  `payment.status(id)` и верните его ответ, а не тело.
- **Пропущенные уведомления.** Если уведомление не пришло, панель сама опрашивает `status`
  для открытых заказов.
- **Ошибки.** Исключение в `create` — плательщик видит ошибку. В `webhook` — уведомление
  отклоняется и попадает в журнал уведомлений оплаты; платёжной системе панель всё равно
  отвечает `200 ok`, а заказ затем подтвердит опрос `status`. Сбой `webhook` пишется в лог,
  но к остановке плагина не считается.

Полный пример — [examples/plugins/lava](../../examples/plugins/lava).

## Запросы из интернета — onHttp

```json
"provides": {"http": true}
```

```js
export function onHttp({ method, path, query, headers, body, ip }) {
  if (query.token !== panel.config.token) return { status: 403, body: "forbidden" };
  return { status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ok: true }) };
}
```

- **Адрес:** `https://<панель>/<секретный путь>/x/<id>/<path>` — он показан в карточке
  плагина после включения. `path` начинается с `/`, `query` — объект «параметр → значение»,
  `headers` — имена в нижнем регистре, `body` — строка, `ip` — адрес клиента.
- **Ответ:** `{status, headers, body}`, где `body` — строка. Тело в обе стороны до 1 МБ.
- **Безопасность:**
  - Ответ отдаётся с `Content-Security-Policy: sandbox`, `nosniff` и `no-store`: страница
    плагина не может выполнять скрипты от имени панели.
  - `Set-Cookie`, защитные и CORS-заголовки, `Clear-Site-Data`, `Refresh`, `Link` и
    `Service-Worker-Allowed` из ответа отбрасываются, cookie запроса плагину не передаются.
  - Ответ без `Content-Type` или с JavaScript-типом отдаётся как `text/plain`.
- **Проверка звонящего — на плагине:** подпись, токен из настроек.
- **Ошибки** пишутся в лог, но к остановке плагина не считаются: отклонять поддельные
  запросы — его работа.

Пример — [examples/plugins/shop-webhook](../../examples/plugins/shop-webhook).

## Карточка пользователя — userFields

```json
"permissions": ["users.view"],
"provides": {"user_fields": [{"key": "email", "label": "Почта"}, {"key": "ltv", "label": {"ru": "Выручка", "en": "LTV"}}]}
```

```js
export function userFields(userId) {
  const row = panel.db.query("SELECT email, ltv FROM clients WHERE user_id = ?", userId)[0];
  return row ? { email: row.email, ltv: `${row.ltv} ₽` } : {};
}
```

Получает id пользователя (число) и возвращает `{ключ: значение}`. Показываются только ключи из
манифеста, значения — как текст до 1000 символов. Если плагин не ответил за 0,3 с или упал,
его блок просто не появляется.

- **Право:** `users.view`.
- **Кто видит.** Админы со всеми правами, выданными плагину.

## Кнопки в админке — onAction

```json
"provides": {"actions": [
  {"key": "receipt", "label": "Выслать чек", "scope": "user", "confirm": true},
  {"key": "export", "label": "Выгрузить", "scope": "users", "perm": "users.view"},
  {"key": "sync", "label": "Синхронизировать", "scope": "global", "perm": "settings.manage"}
]}
```

```js
export function onAction({ key, user_ids }) {
  if (key === "receipt") { /* user_ids = [42] */ }
  return { ok: true, message: "Готово" };
}
```

| `scope` | Где кнопка | `user_ids` |
|---|---|---|
| `user` | в карточке пользователя | один id |
| `users` | в массовых действиях списка пользователей | от 1 до 1000 id |
| `global` | на карточке плагина | `[]` |

- **Кто видит кнопку.** Админы с правом `perm` (по умолчанию `users.manage`) и со всеми
  правами, выданными плагину.
- **Подтверждение.** `confirm: true` спрашивает «вы уверены?» перед нажатием.
- **Ответ.** `message` показывается тостом. `ok: false` показывает его как ошибку,
  исключение — тоже.
- **Журнал.** Каждое нажатие записывается в журнал панели, а изменения через `panel.api` —
  от имени плагина.

## Виджеты обзора — widget

```json
"provides": {"widgets": [{"key": "today", "label": "Оплаты сегодня"}]}
```

```js
export function widget(key) {
  return { type: "stat", value: 12, hint: "+3 к вчера" };
  // { type: "table", columns: ["Тариф", "Шт."], rows: [["Std", 10], ["Max", 2]] }
  // { type: "list", items: ["первое", "второе"] }
}
```

Ответ — до 64 КБ, кэшируется на минуту. Если плагин не ответил за 2 с, виджет показывает «нет
данных». Виджеты видят админы со всеми правами, выданными плагину.

## Страница подписки — subBlocks

```json
"permissions": ["users.view"],
"provides": {"sub_blocks": true}
```

```js
export function subBlocks({ user, lang }) {
  // user = {id, name, status, expire_at, plan_id, data_limit, used, telegram_id}
  return [
    { type: "notice", text: "Скидка 20% до пятницы" },
    { type: "markdown", text: "**Новый сервер** в Нидерландах" },
    { type: "button", label: "Продлить", url: "https://vpn.example/pay" },
  ];
}
```

- **Блоки.** До 10 блоков: `text`, `notice` (выделенный), `markdown` (без сырого HTML),
  `button` (только `https://`). Текст — до 1000 символов.
- **Право:** `users.view`.
- **Время и кэш.** Ответ ждут до 0,3 с, он кэшируется на 5 минут на пользователя и язык.
  Если `subBlocks` упал, плагин не зовут 30 с.
- **Своя страница.** Если у оператора своя страница, она получает те же блоки как `blocks` в
  `GET /v1/users/{id}/subscription`.

## Канал доставки — channel (экспериментально)

```json
"permissions": ["users.view"],
"provides": {"channel": {"label": "E-mail"}}, "experimental": ["channel"]
```

```js
export const channel = {
  send(msg) {
    // msg = {event_id, kind: "message"|"auto_message"|"broadcast"|"notice", notice, text, buttons, users: [...], data}
    for (const u of msg.users) { /* отправить письмо u.external_id */ }
  },
};
```

- **Что приходит.** Канал получает сообщения, рассылки и напоминания (`user.expiring`,
  `user.traffic_low`) для тех, до кого не дотянулся бот панели: у пользователя нет Telegram,
  бот заблокирован или выключен.
- **Право:** `users.view`.
- **Формат.** `text` — Telegram-HTML.
- **Время.** 15 с на `send`.
- **Повторы.** Доставка повторяется, как у событий. Чтобы не отправить дважды, запоминайте
  `event_id` вместе с id пользователя.

Тип `ChannelMessage` — в `rospanel.d.ts`, пример — [examples/plugins/email](../../examples/plugins/email).

## Решения — beforeSignup, beforeDeviceBind, quotePrice

```json
"permissions": ["users.view", "billing.manage"],
"provides": {"hooks": ["beforeSignup", "beforeDeviceBind"], "price": true}, "experimental": ["price"]
```

```js
export function beforeSignup(req) {
  // req = {channel: "web"|"telegram"|"miniapp"|"bot", telegram_id, username, external_id, ip, ref, source, lang}
  if (req.ip && panel.kv.get("ban/" + req.ip)) return { allow: false, reason: "banned" };
  return { allow: true };
}

export function beforeDeviceBind(req) {
  // req = {user_id, hwid, device_os, device_model, user_agent, ip, count, cap, lang}
  return req.device_os === "Windows" ? false : { allow: true };
}

export function quotePrice(req) {
  // req = {user_id, plan_id, plan, periods, devices, base_rub, lang}
  return req.plan === "Standard" ? { price_rub: req.base_rub - 50, note: "loyal" } : null;
}
```

Общие правила:

- **Права.** `hooks` нужно `users.view`, `price` — `billing.manage`.
- **Не возражаю по умолчанию.** Ошибка, таймаут (0,3 с) или занятый плагин означают «не
  возражаю»: сломанный плагин никому не мешает зарегистрироваться или заплатить.
- **Только чтение.** Внутри решений `panel.api` работает только на чтение (GET).
- **Несколько плагинов.** Их спрашивают по порядку, первый отказ побеждает.
- **Причина отказа.** `reason` (и `note` у цены) — ключ из `i18n/` плагина или просто
  текст. Его читает человек: сайт получает `err.pluginDenied`, бот и Mini App показывают текст.

| Экспорт | Когда спрашивают | Ответ |
|---|---|---|
| `beforeSignup` | перед новым аккаунтом из бота, Mini App или `POST /v1/signup`. Про существующий аккаунт и про возврат отвязанного не спрашивают | `{allow, reason}` или `false` |
| `beforeDeviceBind` | перед тем, как новое для пользователя устройство займёт слот (если включён лимит устройств и клиент прислал HWID). Отказ этому устройству помнится минуту | `{allow, reason}` или `false` |
| `quotePrice` | при расчёте цены тарифа и продления, которые пользователь покупает сам; `base_rub` — цена после скидки за период, до промокода. Автопродление с баланса списывает обычную цену | `{price_rub, note}` или `null` |

- **Границы цены.** Цена принимается от половины `base_rub` до `base_rub`. Другая цена
  игнорируется, а предупреждение пишется в лог плагина.
- **Кэш и промокод.** Цена кэшируется на минуту на пользователя, тариф, период и устройства.
  Промокод применяется поверх неё. Заказ сохраняет цену, с которой создан.

Пример — [examples/plugins/channel-gate](../../examples/plugins/channel-gate).

## Пользовательский бот — bot

```json
"permissions": ["users.view"],
"provides": {"bot": {"menu": true, "commands": [{"command": "bonus", "description": {"ru": "Бонус дня", "en": "Daily bonus"}}]}}
```

```js
export const bot = {
  menu({ user, lang }) {
    return [{ text: "🎁 Бонус", data: "bonus" }, { text: "Сайт", url: "https://vpn.example" }];
  },
  onCallback({ user, data, lang }) {
    return { text: "Бонус начислен", buttons: [[{ text: "Ещё", data: "bonus" }]] };
  },
  onCommand({ user, command, args, lang }) {
    return "Команда /" + command + " " + args;
  },
};
```

- **Право:** `users.view`.
- **`user`.** `{id, name, telegram_id, username}`; `id` = 0, если у этого Telegram нет
  аккаунта.
- **`menu`.** Кнопки встают под встроенными кнопками меню, до 6 от плагина. Ответ ждут до
  0,3 с, он кэшируется на 5 минут. Внутри `menu` `panel.api` только читает (GET). Если `menu`
  упал, его не зовут 30 с.
- **Нажатие.** Приходит в `onCallback` с `data` кнопки. `px:<id>:<data>` должно уместиться в
  64 байта, так что `data` — короткая строка. `data` присылает Telegram-клиент пользователя:
  проверяйте её, как любой ввод.
- **Команды.** Попадают в меню команд бота рядом с `/start` и приходят в `onCommand`; `args` —
  текст после команды.
- **Ответ.** Строка или `{text, buttons}`: текст обычный (панель его экранирует), `buttons` —
  ряды кнопок (до 8 рядов по 3) или плоский список. Кнопка с `data` возвращается в
  `onCallback`, с `url` (только `https://`) открывает ссылку. Кнопку «Меню» бот добавляет сам.
- **Сбой.** На ответ 3 с; если плагин упал или не успел, человек видит «Сейчас это
  недоступно. Попробуйте позже».

## Конфиги подписки — transformSubscription (экспериментально)

```json
"permissions": ["users.manage"],
"provides": {"subscription": true}, "experimental": ["subscription"]
```

```js
export function transformSubscription({ user, format, links, config }) {
  if (format === "links") return { links: links.map((l) => l.replace(/#.*/, "#" + encodeURIComponent("Мой VPN"))) };
  if (format === "singbox") { config.route.rules.unshift({ domain_suffix: ["example.ru"], outbound: "direct" }); return { config }; }
  return null; // как есть
}
```

- **Право:** `users.manage`.
- **Форматы.** `links` — массив ссылок, `clash` — `config` строкой YAML, `singbox` и `xray` —
  `config` как JSON-объект (у Xray может быть и массив).
- **Ответ.** Та же форма или `null`.
- **Проверка ответа.**
  - Ссылки должны быть прокси-ссылками (`vless://`, `hysteria2://`, `trojan://`, `ss://`, …),
    их может быть не больше чем вдвое больше исходных плюс 50.
  - Clash остаётся текстом с `proxies:`, у sing-box непустые `outbounds`.
  - Если проверка не прошла, клиент получает профиль панели, а в лог плагина пишется
    предупреждение.
- **Только чтение.** Внутри `panel.api` только читает (GET).
- **Нагрузка.** Через это проходит каждое обновление подписки. Ответ кэшируется на 5 минут
  на пользователя, формат и содержимое входа. Без изменения входа повторные обновления
  плагин не вызывают. Если преобразование упало, плагин не зовут 30 с: клиенты получают
  профиль панели.

## Тема страницы подписки — theme

```json
"provides": {"theme": true}
```

```
theme/theme.css    стили поверх стилей страницы
theme/bg.webp      картинки (png, jpg, webp, gif, svg) и шрифты (woff2, woff)
```

```css
:root { --bg: #0b1020; --surface: #141a2e; --ink: #e8ecf8; --muted: #9aa3bf; }
body { background-image: url(bg.webp); }
.btn.alt { color: var(--ink); }
```

- **Что меняет тема.** Только вид: страница и все её функции (оплата, устройства, Telegram)
  остаются панельными.
- **Переменные.** `--brand`, `--brand-dark`, `--on-brand`, `--accent-fg`, `--bg`,
  `--surface`, `--ink`, `--muted`, `--success-fg`, `--warning-fg`, `--danger-fg`. Классы
  страницы (`.btn`, `.btn.alt` …) тоже можно стилизовать, но они могут меняться между
  версиями.
- **Ничего извне.**
  - `url()` — только имя файла из `theme/` или `data:image/…`, `data:font/…`.
  - Запрещены `@import`, `image-set(`, `expression(`, `javascript:` и адреса (`://`, `//`).
    Экранирование CSS раскрывается до проверки.
  - Встроенный SVG в `data:image/svg+xml` может содержать `xmlns="http://…"`.
  - Страница подписки ещё и сама не грузит картинки и шрифты не с панели (CSP).
  - Причина: страница, которая грузит что-то со стороннего хоста, висит там, где этот хост
    заблокирован.
- **Размеры.** До 50 файлов, 1 МБ каждый, 3 МБ вместе.
- **Несколько тем.** Если включено несколько, действует первая по порядку.

`main.js` у темы может быть пустым: `export {};`. Пример —
[examples/plugins/theme-night](../../examples/plugins/theme-night).
