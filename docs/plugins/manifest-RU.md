# Манифест и пакет

[English version](manifest.md) · [Оглавление](README-RU.md)

## Пакет

Плагин — это zip-архив с фиксированным набором файлов:

```
plugin.json              манифест (обязателен)
main.js                  весь код, один ES-модуль (обязателен, до 2 МБ)
migrations/NNNN*.sql     своя база, применяется по порядку имён (до 100 файлов, 256 КБ каждый)
i18n/<язык>.json         строки для panel.t(): {"ключ": "текст"} (до 256 КБ)
theme/theme.css, …       тема страницы подписки (см. «Точки расширения»)
README.md                показывается в панели (до 256 КБ)
icon.svg                 значок (до 128 КБ)
LICENSE, CHANGELOG.md
```

- Любой другой файл — ошибка установки: оператор проверяет всё, что есть в пакете.
- Одна общая папка верхнего уровня допустима (так получается, если сжать папку целиком);
  `__MACOSX` и `.DS_Store` пропускаются.
- Пакет до 5 МБ в zip и до 12 МБ в распакованном виде, не больше 200 файлов. Символические
  ссылки и пути вида `../` запрещены.
- `main.js` не может импортировать другие файлы. Если код удобнее писать модулями или на
  TypeScript — соберите один файл бандлером (esbuild, rollup) с форматом ESM.

`rospanel plugin pack .` кладёт в zip только эти файлы, остальное (`test.js`,
`dev.config.json`, `rospanel.d.ts`, исходники) оставляет снаружи.

## plugin.json целиком

```json
{
  "id": "my-plugin",
  "version": "1.2.0",
  "api": 1,
  "panel": ">=5.0.0",
  "name": {"ru": "Мой плагин", "en": "My plugin"},
  "description": {"ru": "Одна-две фразы о том, что он делает", "en": "…"},
  "author": "Имя или ник",
  "homepage": "https://github.com/you/my-plugin",
  "license": "MIT",

  "permissions": ["users.view", "users.manage"],
  "net": ["api.example.com", "*.example.org:8443"],
  "settings": [ … ],
  "provides": { … },
  "experimental": [],

  "db_quota_mb": 100,
  "memory_mb": 32
}
```

## Поля

### Кто плагин

| Поле | Обязательно | |
|---|---|---|
| `id` | да | 3–40 символов: `a-z`, `0-9`, `-`, начинается с буквы. Не `rospanel`, `core`, `builtin`, `panel`, `plugin`, `system`. Никогда не меняется: это имя файла базы, ключ настроек, имя в журнале. |
| `version` | да | `X.Y.Z`. Обновление ставится поверх, только если это тот же `id`. |
| `api` | да | `1`. |
| `panel` | нет | `>=X.Y.Z` — самая старая версия панели, где плагин работает. Старая панель откажется его ставить с понятной причиной. |
| `name` | да | Строка или `{"ru": …, "en": …}`, до 80 символов. |
| `description` | нет | То же, до 500 символов. |
| `author`, `license` | нет | До 100 и 50 символов. |
| `homepage` | нет | `http(s)://…` — где исходники и вопросы. |

Текст на нескольких языках — объект с кодами из двух букв: `{"ru": "…", "en": "…"}`. Панель
берёт язык админа, иначе английский, иначе русский, иначе любой.

### Что ему можно

**`permissions`** — права для `panel.api`, те же, что у API-ключей:

| Право | Что открывает |
|---|---|
| `users.view` | читать пользователей, их подписки, устройства, трафик |
| `users.manage` | создавать и менять пользователей, продлевать, включать и выключать |
| `users.delete` | удалять пользователей ⚠️ |
| `users.export` | выгрузка пользователей |
| `groups.view`, `groups.manage` | группы доступа |
| `stats.view`, `stats.manage` | статистика; `manage` — сброс ⚠️ |
| `billing.view`, `billing.manage` | тарифы, заказы, баланс, промокоды; `manage` — менять ⚠️ |
| `payments.manage` | настройки платёжных провайдеров ⚠️ |
| `broadcasts.manage` | рассылки и сообщения пользователям |
| `servers.view`, `servers.manage` | сервера и ноды |
| `routing.view`, `routing.manage` | маршрутизация |
| `settings.view`, `settings.manage` | настройки панели; `manage` — менять ⚠️ |
| `security.view` | журнал входов и блокировок |
| `logs.view`, `audit.view` | логи и журнал действий |

⚠️ — «рискованные» права: на экране согласия они отмечены красным. Никогда не выдаются:
`owner`, `api.manage`, `security.manage`, `system.update`, `webhooks.manage`,
`plugins.view`, `plugins.manage` — плагин не может выпускать ключи, менять админов,
обновлять панель или ставить другие плагины.

Какое право нужно конкретному маршруту, видно в `/v1/docs` панели. Просите меньше: оператор
читает этот список перед установкой.

Точкам расширения, которые передают плагину данные пользователей или дают решать за них,
тоже нужно право — колонка «Нужно» в [`provides`](#на-что-он-отвечает). Без него проверка не
проходит: `provides.price needs the "billing.manage" permission: it sets what users pay`.

Установить или обновить плагин может только админ, у которого есть все запрошенные права;
для отката нужны права той версии, которую он возвращает.

**`net`** — хосты для `panel.http.fetch`, до 20:

- `api.example.com` — ровно этот хост, порты 80 и 443;
- `*.example.com` — любые поддомены (но не сам `example.com`), порты 80 и 443;
- `api.example.com:8443` — только этот порт.

Только имена в нижнем регистре: IP-адреса и имена без точки не принимаются. Даже по
разрешённому имени панель не соединится со своими адресами, с приватными, loopback, CGNAT,
NAT64, 6to4 и site-local (защита от подмены DNS).

### Настройки

`settings` — форма, которую оператор заполняет после установки (до 50 полей). Плагин видит
значения в `panel.config` — **всегда строками**.

```json
"settings": [
  {"key": "token", "kind": "secret", "label": "API-токен", "help": {"ru": "В личном кабинете сервиса"}},
  {"key": "shop_id", "kind": "text", "label": {"ru": "ID магазина", "en": "Shop ID"}, "placeholder": "12345"},
  {"key": "notes", "kind": "textarea", "label": "Текст письма", "optional": true},
  {"key": "enabled_log", "kind": "bool", "label": "Подробный лог", "default": "false"},
  {"key": "limit", "kind": "number", "label": "Лимит в день", "default": "100"},
  {"key": "mode", "kind": "select", "label": "Режим", "default": "test",
   "options": [{"value": "test", "label": "Тест"}, {"value": "live", "label": "Боевой"}]}
]
```

| Свойство | |
|---|---|
| `key` | `a-z`, `0-9`, `_`, начинается с буквы, до 40 символов, уникален. |
| `kind` | `text`, `secret`, `textarea`, `bool` (всегда `"true"` или `"false"`; не заданный — `"false"`), `number` (строка с числом), `select` (одно из `options`). |
| `label` | Подпись, обязательна. |
| `help`, `placeholder` | Подсказка под полем и в пустом поле. |
| `optional` | Необязательное поле. Обязательное незаполненное не даёт включить плагин. |
| `default` | Значение по умолчанию (у `secret` его быть не может). `bool` принимает `"true"`/`"false"` или `"1"`/`"0"` — то же самое. |
| `options` | Для `select`: `[{"value", "label"}]`. |

`secret` хранится зашифрованным, в панели больше не показывается, а пустое значение при
сохранении формы оставляет прежнее. Крестик в поле стирает значение; секрет при этом
помечается «будет удалён» и пропадает при сохранении. Обязательную настройку у включённого плагина стереть нельзя —
сначала выключите его. Значение — до 16 КБ. Смена настроек перезапускает VM плагина, и
следующий вызов видит новый `panel.config`.

### На что он отвечает

`provides` — точки расширения. Подробно каждая — в [справочнике по точкам](exports-RU.md).

| Ключ | Значение | Плагин экспортирует | Нужно |
|---|---|---|---|
| `events` | имена событий, `["user.created", …]` | `onEvent` | `users.view`; для `payment.*`, `plan.*`, `balance.*`, `referral.*`, `promo.*` ещё `billing.view`; для `node.*` ничего |
| `cron` | `[{"name": "sync", "schedule": "*/15 * * * *"}]`, до 10 | функции с этими именами | — |
| `payment` | `{"label": …, "note": …}` | `payment.create`, `payment.status`, `payment.webhook` | `payments.manage` |
| `http` | `true` | `onHttp` | — |
| `hooks` | `["beforeSignup", "beforeDeviceBind"]` | функции с этими именами | `users.view` |
| `user_fields` | `[{"key", "label"}]`, до 20 | `userFields` | `users.view` |
| `actions` | `[{"key", "label", "scope", "perm", "confirm"}]`, до 20 | `onAction` | — |
| `widgets` | `[{"key", "label"}]`, до 20 | `widget` | — |
| `sub_blocks` | `true` | `subBlocks` | `users.view` |
| `bot` | `{"menu": true, "commands": [{"command", "description"}]}` | `bot.menu`, `bot.onCallback` (с `menu`), `bot.onCommand` (с `commands`) | `users.view` |
| `channel` | `{"label": …}` | `channel.send` | `users.view` |
| `price` | `true` | `quotePrice` | `billing.manage` |
| `subscription` | `true` | `transformSubscription` | `users.manage` |
| `theme` | `true` | — (нужен `theme/theme.css`) | — |

Если `main.js` не экспортирует то, что объявлено, плагин не включится: «main.js does not export
what plugin.json declares: …». Объявленная точка, которой нет в этой версии панели, — ошибка
установки.

`cron.schedule` — пять полей (`минута час день месяц день_недели`) в часовом поясе панели;
поддерживаются `*`, списки `1,15`, диапазоны `1-5` и шаги `*/10`. Чаще раза в минуту нельзя.

### Экспериментальные точки

`channel`, `price` и `subscription` ещё могут поменяться. Плагин, который их использует,
перечисляет их в `"experimental": ["price"]` — так автор подтверждает, что знает об этом, а
оператор видит пометку на экране согласия.

### Ресурсы

| Поле | По умолчанию | Максимум | |
|---|---|---|---|
| `db_quota_mb` | 100 | 1024 | размер своей базы; запись сверх него отклоняется ([подробнее](api-RU.md#paneldb--своя-база-sqlite)) |
| `memory_mb` | 32 | 128 | память JavaScript; сверх неё вызов бросает `out of memory` |

## Проверка

```sh
rospanel plugin validate .          # папку
rospanel plugin validate my.zip     # готовый пакет
```

`validate` читает пакет так же, как панель, загружает код и проверяет экспорты. Все проблемы
выводятся сразу списком, например:

```
error: plugin: permissions[1] "owner": never granted to plugins; net[0] "10.0.0.1": a host name like api.example.com or *.example.com, optionally with :port
```
