# Плагины для RosPanel

[English version](README.md)

Плагин — это JavaScript внутри панели. Он реагирует на события панели, работает по
расписанию, хранит свои данные, обращается к REST API панели и в интернет — ровно
настолько, насколько оператор разрешил при установке.

Плагины в бета-версии. Здесь описано то, что панель выполняет сейчас.

## Быстрый старт

Все инструменты встроены в `rospanel`, Node не нужен.

```sh
rospanel plugin new my-plugin   # рабочий плагин из шаблона
cd my-plugin
rospanel plugin test .          # запустить test.js
rospanel plugin dev .           # попробовать руками, с перезагрузкой при сохранении
rospanel plugin pack .          # my-plugin-0.1.0.zip для установки в панель
```

Zip ставится в панели: **Настройки → Плагины → Установить**. Панель показывает, что
плагин просит, ставит его выключенным, а включаете вы — после того как заполните
настройки.

Готовые плагины с тестами лежат в [examples/plugins](../../examples/plugins).

## Пакет

```
plugin.json            манифест
main.js                весь код, один ES-модуль (можно собрать бандлером)
migrations/0001_*.sql  своя база плагина, применяются по порядку имён
i18n/ru.json, en.json  строки для panel.t()
README.md, icon.svg    показываются в панели
```

Всё остальное `pack` в пакет не кладёт: `test.js`, `rospanel.d.ts`, исходники для
бандлера. Импортировать другие файлы из `main.js` нельзя.

## plugin.json

```json
{
  "id": "my-plugin",
  "version": "1.0.0",
  "api": 1,
  "panel": ">=4.5.0",
  "name": {"ru": "Мой плагин", "en": "My plugin"},
  "description": {"ru": "…", "en": "…"},
  "author": "you",
  "permissions": ["users.view"],
  "net": ["api.example.com"],
  "settings": [
    {"key": "token", "kind": "secret", "label": "API-токен"}
  ],
  "provides": {
    "events": ["user.created", "payment.paid"],
    "cron": [{"name": "sync", "schedule": "*/15 * * * *"}]
  },
  "db_quota_mb": 100
}
```

| Поле | |
|---|---|
| `id` | 3–40 символов `a-z`, `0-9`, `-`. Не меняется: по нему называется файл базы и записи в журнале. |
| `version` | `X.Y.Z`. |
| `api` | `1`. |
| `panel` | Самая старая версия панели, на которой плагин работает, в виде `>=X.Y.Z`. |
| `name`, `description` | Строка или `{"ru": …, "en": …}`. |
| `permissions` | Что можно делать через `panel.api` — те же права, что у API-ключей (`users.view`, `billing.manage`, …). Не выдаются никогда: `api.manage`, `security.manage`, `system.update`, `webhooks.manage`, `plugins.*`. |
| `net` | Хосты, куда может ходить `panel.http.fetch`: `api.example.com`, `*.example.com`, можно с `:port`. |
| `settings` | Форма, которую заполняет оператор. `kind`: `text`, `secret`, `bool`, `number`, `textarea`, `select` (с `options`). Ещё `optional`, `default`, `help`, `placeholder`. `secret` хранится зашифрованным и больше не показывается. |
| `provides.events` | Имена событий, те же, что у вебхуков (см. `docs/api.md`). Плагин должен экспортировать `onEvent`. |
| `provides.cron` | Задачи: `name` — экспортируемая функция, `schedule` — cron из пяти полей в часовом поясе панели, не чаще раза в минуту. |
| `provides.payment` | `{"label": …, "note": …}`: способ оплаты. Плагин экспортирует `payment.create`, `payment.status`, `payment.webhook` — см. ниже. |
| `provides.http` | `true`: плагин отвечает на запросы по своему адресу. Экспортирует `onHttp`. |
| `provides.hooks` | `beforeSignup`, `beforeDeviceBind`: плагин участвует в этих решениях — см. ниже. |
| `provides.bot` | `{"menu": true, "commands": [{"command", "description"}]}`: кнопки и команды в пользовательском боте панели. |
| `provides.price` | `true`: плагин назначает цену тарифа для пользователя (`quotePrice`). Экспериментально. |
| `provides.subscription` | `true`: плагин меняет то, что получают клиенты (`transformSubscription`). Экспериментально. |
| `experimental` | Экспериментальные точки, которые использует плагин (`channel`, `price`, `subscription`): они ещё могут измениться. |
| `db_quota_mb` | Лимит размера базы, по умолчанию 100, до 1024. |
| `memory_mb` | Лимит памяти JavaScript, по умолчанию 32, до 128. |

## main.js

Панель вызывает то, что `main.js` экспортирует:

```js
export function onEnable() { /* необязательно: один раз при включении */ }

/** @param {PanelEvent} e — {id, event, created_at, data} */
export function onEvent(e) {
  if (e.event === "user.created") panel.log.info("новый пользователь", e.data);
}

export function sync() { /* задача по расписанию */ }
```

- Все вызовы `panel.*` синхронные и сразу возвращают ответ: без колбэков и таймеров.
  `async`/`await` работают, но ничего не ждёт времени.
- Событие доставляется хотя бы один раз: запоминайте `e.id`, чтобы пропустить повтор.
- Брошенная ошибка попадает в лог. Если `onEvent` бросил ошибку, событие придёт снова
  (через 10 с, 30 с, 2 мин, 10 мин).
- Храните состояние в `panel.kv` или `panel.db`: панель может перезапустить плагин
  между любыми двумя вызовами.

## Способ оплаты

```js
export const payment = {
  create({ amount_rub, order_id, description, return_url, webhook_url, email }) {
    return { provider_id: "inv-1", pay_url: "https://pay.example/inv-1" };
  },
  status(provider_id) {
    return { status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
  webhook({ body, headers }) {          // имена заголовков в нижнем регистре
    return { provider_id: "inv-1", status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
};
```

Способ оплаты появляется в **Настройки → Оплата** как `plugin.<id>` и включается оператором
как любой другой. Панель создаёт заказ, отправляет плательщика на `pay_url`, направляет
уведомления платёжной системы (`webhook_url`) в `payment.webhook`, опрашивает `payment.status`,
если уведомление потерялось, и выдаёт тариф.

- `status` — `paid`, `pending`, `cancelled` или `refunded`.
- Ответ `paid` или `refunded` обязан содержать `amount_kopecks` и `currency`: панель выдаёт тариф,
  только когда они совпадают с заказом.
- `payment.webhook` должен убедиться, что уведомление настоящее: проверить подпись, а если
  платёжная система ничего не подписывает — спросить её API через `status` и вернуть это, а не тело.
- Полный пример — [examples/plugins/lava](../../examples/plugins/lava).

## Запросы из интернета

С `"http": true` плагин отвечает по адресу `https://<панель>/<путь уведомлений>/x/<id>/…` (адрес
показан в карточке плагина): для магазина, который зовёт вас после продажи, формы на сайте,
OAuth-колбэка.

```js
export function onHttp({ method, path, query, headers, body, ip }) {
  return { status: 200, headers: { "Content-Type": "application/json" }, body: "{}" };
}
```

Кто звонит, проверяет сам плагин (подпись, токен из настроек). Тело до 1 МБ в обе стороны; ответ
не может ставить cookie и отдаётся в песочнице. Пример —
[examples/plugins/shop-webhook](../../examples/plugins/shop-webhook).

## Экраны панели

Всё это панель рисует сама из простых значений — плагин никогда не показывает свой HTML
ни админу, ни пользователю.

| `provides` | Экспорт | Что показывает |
|---|---|---|
| `user_fields: [{key, label}]` | `userFields(userId)` → `{key: value}` | значения в карточке пользователя (ответ за 0,3 с) |
| `actions: [{key, label, scope, perm, confirm}]` | `onAction({key, user_ids})` → `{ok, message}` | кнопки: `user` в карточке, `users` для выбранных в списке, `global` на карточке плагина. Видят и нажимают только админы с правом `perm` (по умолчанию `users.manage`); каждое нажатие — в журнале панели |
| `widgets: [{key, label}]` | `widget(key)` → `{type: "stat"\|"table"\|"list", …}` | плитки на обзоре, кэш на минуту |
| `sub_blocks: true` | `subBlocks({user, lang})` → `[{type, text, label, url}]` | блоки наверху страницы подписки (`text`, `notice`, `markdown` без сырого HTML, `button` на https); ответ за 0,3 с, кэш на 5 минут на пользователя. Своя страница получает их как `blocks` в `GET /v1/users/{id}/subscription` |
| `channel: {label}` | `channel.send(msg)` | доставляет сообщения, рассылки и напоминания тем, до кого не дотягивается бот панели — см. `ChannelMessage` в `rospanel.d.ts`. Экспериментально: добавьте `"channel"` в `experimental` |

Примеры: [email](../../examples/plugins/email) (канал, поля в карточке и виджет),
[announcement](../../examples/plugins/announcement) (блоки на странице и кнопка).

## Решения

Панель спрашивает плагины перед действием. На ответ 0,3 с; ошибка, таймаут или плагин, занятый
другим вызовом, означают «не возражаю» — сломанный плагин никому не мешает зарегистрироваться
или заплатить. Внутри `panel.api` только читает.

| Экспорт | Когда спрашивают | Ответ |
|---|---|---|
| `beforeSignup(req)` | перед новым аккаунтом: бот, Mini App, `POST /v1/signup` (какой — в `channel`). Про уже существующий аккаунт не спрашивают | `{allow, reason}` или `false` |
| `beforeDeviceBind(req)` | перед тем как новое устройство займёт слот (при включённом лимите устройств). Отказ помнится минуту | `{allow, reason}` или `false` |
| `quotePrice(req)` | при расчёте цены тарифа или продления: `base_rub` — цена панели после скидки за период, до промокода | `{price_rub, note}` или `null` |

- Несколько плагинов спрашиваются по порядку, первый отказ побеждает.
- `reason` и `note` — ключи из `i18n/` плагина (или просто текст): их читает человек.
- Цена принимается только от половины `base_rub` до `base_rub` — не дороже и не бесплатно; иначе
  она игнорируется, а причина пишется в лог плагина. Заказ сохраняет цену, с которой открыт.
  Цены помнятся минуту на пользователя и тариф.

[examples/plugins/channel-gate](../../examples/plugins/channel-gate) пускает к регистрации через
Telegram только подписчиков канала.

## Бот

```js
export const bot = {
  menu({ user, lang }) { return [{ text: "🎁 Бонус", data: "bonus" }]; },
  onCallback({ user, data, lang }) { return { text: "Готово", buttons: [{ text: "Сайт", url: "https://…" }] }; },
  onCommand({ user, command, args, lang }) { return "Просто текст"; },
};
```

- `menu` добавляет кнопки под меню бота (ответ за 0,3 с, помнится 5 минут). Нажатие приходит в
  `onCallback` с её `data` (до ~20 байт, чтобы `px:<id>:<data>` уместился в 64 байта Telegram).
- `commands` попадают в меню команд бота рядом с `/start`, отвечает на них `onCommand`. `args` —
  текст после команды.
- Ответ — обычный текст (панель его экранирует) и ряды кнопок: `data` вернётся в `onCallback`,
  `url` открывает https-страницу. Кнопку назад в меню бот добавит сам. 3 с на ответ; дольше —
  человек видит «попробуйте позже».
- `user.id` равен 0 для Telegram без аккаунта.

## Подписки (экспериментально)

```js
export function transformSubscription({ user, format, links, config }) {
  if (format === "links") return { links: links.map(l => l.replace(/#.*/, "#Мой VPN")) };
  return null; // без изменений
}
```

`format` — `links` (ссылки), `clash` (`config` — текст YAML), `singbox` или `xray` (`config` —
JSON-документ). Верните ту же форму или `null`. Через это проходит каждое обновление подписки
клиентом, поэтому ответ помнится 5 минут на пользователя и вход, а у плагина 0,3 с; ответ другой
формы, ссылка не на прокси или ошибка — и клиент получает профиль панели.

## API панели

Полный справочник с типами для редактора — `rospanel.d.ts`, его кладёт `plugin new`.

| | |
|---|---|
| `panel.config` | Настройки оператора, вместе с секретами. |
| `panel.kv.get/set/delete/list` | Простые значения (любой JSON до 64 КБ). |
| `panel.db.exec/query/tx` | Своя база SQLite. `PRAGMA`, `ATTACH`, `VACUUM` и команды транзакций запрещены — используйте `tx(fn)`. |
| `panel.api(method, path, body)` | REST API панели (`/v1`, см. `/v1/docs`) с правами плагина. Изменения попадают в журнал панели от имени плагина. |
| `panel.users.get/list/update` | Сокращения для `panel.api`. |
| `panel.http.fetch(url, opts)` | Запросы к хостам из `net`. Приватные и локальные адреса недоступны никогда. |
| `panel.crypto.*` | `hash`, `hmac` (md5, sha1, sha256, sha512), `sign`/`verify` (RS256, RS512, ES256, Ed25519), `jwt`, `randomHex`, `randomUUID`. |
| `panel.t(key, params, lang)` | Строки из `i18n/`. |
| `panel.log.*`, `console.*` | Лог плагина в панели. |

## Лимиты

| | |
|---|---|
| Память | `memory_mb` (32 МБ). Сверх лимита вызов бросает `out of memory`. |
| Время | 10 с на событие или запрос, 15 с на вызов платёжки, 60 с на задачу cron, 3 с в боте, 0,3 с на решение или подписку. Дольше — вызов бросает `interrupted`. |
| База | `db_quota_mb`; не больше 10 000 строк или 4 МБ на запрос. |
| HTTP | 4 МБ на ответ, 30 с, 5 редиректов в пределах `net`. |
| `panel.api` | 1 МБ на запрос, 4 МБ на ответ. |
| Пакет | 5 МБ в zip, `main.js` до 2 МБ. |

Панель останавливает плагин после 10 ошибок подряд, когда база перерастает квоту
или когда он три минуты подряд обрабатывает больше 300 событий в минуту (обычно это
плагин, который реагирует на собственные изменения). Оператор получает уведомление и
включает плагин снова.

## Тесты

`test.js` выполняется в отдельной песочнице рядом с плагином:

```js
test("приветствует нового пользователя", () => {
  mock.http("https://api.example.com/", { status: 200, body: { ok: true } });
  mock.api("GET", "/v1/users/*", { body: { id: 7, name: "Ann" } });

  plugin.event("user.created", { id: 7, name: "Ann" });

  assert.equal(plugin.db.query("SELECT count(*) AS n FROM greetings"), [{ n: 1 }]);
  assert.equal(mock.calls().length, 1);
});
```

`plugin.call(export, arg)`, `plugin.event(name, data)`, `plugin.cron(name)` вызывают
плагин; `plugin.kv` и `plugin.db` читают и наполняют его хранилище; `mock.http` и
`mock.api` отвечают на его запросы — всё, что не замокано, отклоняется; `crypto.hmac` и соседи
подписывают то, что тест отправляет плагину, как это сделала бы платёжная система. Моки
сбрасываются перед каждым тестом, база — нет. Настройки для `test` и `dev` лежат в
`dev.config.json`.

`rospanel plugin dev .` даёт командную строку (`event`, `call`, `cron`, `kv`, `sql`,
`mock`, `logs`). С `--panel https://host/<путь API> --key <API-ключ>` вызовы
`panel.api` идут в настоящую панель.

## Обновления

Новый zip ставится поверх старого кнопкой **Обновить** у плагина. Панель хранит
предыдущую версию и копию базы, снятую перед новыми миграциями: **Откатить**
возвращает и то и другое. Выпущенную миграцию не меняйте — добавляйте новую;
`rospanel plugin pack . --prev old.zip` это проверяет.

Если обновление просит больше прав или хостов, оператор видит разницу.

## Как панель обращается с плагином

- Он работает в песочнице WebAssembly: ни файлов, ни процессов, ни сети, кроме
  `panel.http.fetch`.
- Оператор видит, что он просит, до установки и может прочитать `main.js` в панели.
- Он работает только на главном сервере.
- Его база — отдельный файл (`plugins/<id>.db` в каталоге данных) и входит в бэкапы
  панели.
