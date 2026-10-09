# API плагина: `panel.*`

[English version](api.md) · [Оглавление](README-RU.md)

`panel` — глобальный объект, через который плагин обращается к панели. Те же типы с
подсказками лежат в `rospanel.d.ts` (его создаёт `rospanel plugin new`).

**Все вызовы синхронные:** результат возвращается сразу, без `await` и колбэков. `async`/`await`
в коде работают, но ждать нечего: таймеров (`setTimeout`) нет. Ошибка любого вызова — обычное
исключение JavaScript, его можно поймать `try/catch`.

**Данные.** Строки передаются как UTF-8. Байты — объектом `{base64: "…"}` (в `crypto`, `db`,
`blob.from`).

---

## panel.plugin и panel.config

```js
panel.plugin   // {id: "my-plugin", version: "1.2.0"}
panel.config   // {"token": "…", "mode": "live", "limit": "100", "enabled_log": "true"}
```

`panel.config` — настройки оператора, расшифрованные, вместе со значениями по умолчанию.
**Все значения — строки**: `bool` всегда `"true"` или `"false"` (не заданный — `"false"`),
`number` приходит как `"100"`.
Незаполненного необязательного поля без `default` в объекте нет. Объект только для чтения;
после смены настроек плагин перезапускается с новым.

```js
const limit = Number(panel.config.limit || 100);
const verbose = panel.config.enabled_log === "true";
```

## panel.log и console

```js
panel.log.info("синхронизация", { users: 12 });
panel.log.warn("сервис ответил 429");
panel.log.error(new Error("…").message);
console.log("то же, что panel.log.info", { a: 1 });
```

Все строки видны в панели (**Плагины → Лог**, последние 1000). Предупреждения и ошибки ещё
копируются в журнал сервера с меткой `plugin=<id>`, до 20 в минуту; строки `info` попадают туда
только на уровне debug. Строка — до 4 КБ. Не пишите в лог секреты.

## panel.kv — простые значения

```js
panel.kv.set("last_sync", { at: Date.now(), count: 12 });
panel.kv.get("last_sync");            // {at: …, count: 12} или undefined
panel.kv.delete("last_sync");
panel.kv.list("user/");               // [{key: "user/1", value: …}, …] по возрастанию ключа
panel.kv.list("user/", { after: "user/500", limit: 100 });
```

| | |
|---|---|
| Ключ | строка, до 512 байт |
| Значение | любое JSON-значение, до 64 КБ после сериализации; `undefined` — ошибка (для удаления есть `delete`) |
| `list` | до 1000 записей за раз (`limit`), страницы через `after` |

`kv` хранится в той же базе плагина (таблица `_kv`), поэтому попадает в бэкапы и
откатывается вместе с ней.

## panel.db — своя база SQLite

У плагина свой файл SQLite (`plugins/<id>.db` в каталоге данных панели). Таблицы создают
миграции — файлы `migrations/NNNN_имя.sql` в пакете. Они применяются по порядку имён при
включении плагина, каждая один раз. Панель запоминает хэш выпущенной миграции и не даст
изменить её в обновлении.

```js
const { changes, last_insert_id } = panel.db.exec(
  "INSERT INTO orders(user_id, amount) VALUES (?, ?)", 42, 199);

const rows = panel.db.query("SELECT id, amount FROM orders WHERE user_id = ? ORDER BY id DESC LIMIT 10", 42);
// [{id: 3, amount: 199}, …]

panel.db.tx(() => {
  panel.db.exec("UPDATE balance SET rub = rub - ? WHERE user_id = ?", 100, 42);
  panel.db.exec("INSERT INTO history(user_id, rub) VALUES (?, ?)", 42, -100);
}); // исключение внутри откатывает всё
```

- **Аргументы `?`:** строка, число (целое пишется как INTEGER), `true`/`false`, `null`,
  `{base64: "…"}` для BLOB.
- **Результат `query`** — массив объектов «столбец → значение»; BLOB возвращается как
  `{base64}`. Не больше 10 000 строк и 4 МБ — для больших выборок используйте `LIMIT`/`OFFSET`.
- **`exec`** возвращает `{changes, last_insert_id}`.
- **`tx(fn)`** выполняет `fn` в транзакции. Если `fn` вернула значение, транзакция
  фиксируется и `tx` возвращает его. Если бросила исключение — откат, и исключение летит
  дальше. Вложенные `tx` не поддерживаются. Транзакцию, оставшуюся открытой к концу вызова,
  панель откатывает сама.
- **Что запрещено:** `PRAGMA`, `ATTACH`, `DETACH`, `VACUUM`, `EXPLAIN`, `BEGIN`, `COMMIT`,
  `END`, `ROLLBACK`, `SAVEPOINT`, `RELEASE` — в коде и в миграциях. Транзакции — только
  через `tx`. Триггеры, индексы, представления, `WITH`, `RETURNING`, `UPSERT`
  (`ON CONFLICT`) работают.
- **Квота:** `db_quota_mb` (100 МБ по умолчанию). Запись сверх квоты бросает ошибку с
  `over its quota`. Если после этого вызова база всё ещё заполнена на 90% и больше, плагин
  останавливается до решения оператора; вызов, который освободил место, его не останавливает.
  Страницы удалённых строк используются снова, а журнал записи (WAL) после контрольных точек
  сжимается до 8 МБ.
- **Размеры:** одно значение — до 8 МБ, текст одного запроса — до 1 МБ.

Схему меняйте новой миграцией (`0002_add_email.sql` с `ALTER TABLE …`). Перед каждым
обновлением панель снимает копию базы, и **Откатить** возвращает её.

## panel.api — REST API панели

```js
const r = panel.api("GET", "/v1/users/42");
// r = {status: 200, body: {data: {id: 42, name: "ann", …}}}

const upd = panel.api("PATCH", "/v1/users/42", { data_limit: 10737418240 });
if (upd.status !== 200) throw new Error(upd.body.error.message);

const page = panel.api("GET", "/v1/users?limit=500&offset=0").body.data;
```

- **Маршруты.** Те же, что у внешнего `/v1` (см. `/v1/docs` в панели и
  [docs/api.md](../api.md)), кроме `/v1/mcp`. Методы: `GET`, `POST`, `PUT`, `PATCH`,
  `DELETE`.
- **Права** — те, что плагин попросил в `permissions` и оператор одобрил. Нет права —
  `status: 403`.
- **Ошибки API не бросают исключение:** смотрите `status` и `body.error`
  (`{code, message, key, args}`). Исключение — только при неверном пути, методе или превышении
  лимитов.
- **`body` ответа** — разобранный JSON, а если ответ не JSON, то текст.
- **Лимиты:** тело запроса до 1 МБ, ответ до 4 МБ. Списки забирайте страницами
  (`limit` до 1000, `offset`). Ответ, который надо куда-то отправить целиком, можно получить
  в blob: `panel.api("GET", path, null, {blob: true})`.
- **Журнал.** Изменения записываются в журнал панели от имени плагина
  (`plugin:<id>`), а события, которые они вызывают, приходят и другим плагинам.
- **Только чтение** в решениях (`beforeSignup`, `beforeDeviceBind`, `quotePrice`), в
  `bot.menu` и `transformSubscription`: там разрешён только `GET`.
- **Не в себя.** Запрос, который вызвал бы этот же плагин (заказ с оплатой его же способом),
  не проходит: `plugin: a call into itself (through the panel) is refused`.

Сокращения:

```js
panel.users.get(42)                       // = panel.api("GET", "/v1/users/42")
panel.users.list("status=active&limit=50") // = panel.api("GET", "/v1/users?status=active&limit=50")
panel.users.update(42, { note: "VIP" })    // = panel.api("PATCH", "/v1/users/42", {note: "VIP"})
```

## panel.http.fetch — запросы в интернет

```js
const r = panel.http.fetch("https://api.example.com/v1/items?limit=10", {
  method: "POST",                                   // по умолчанию GET
  headers: { Authorization: "Bearer " + panel.config.token },
  body: { name: "test" },                           // объект → JSON с Content-Type
  timeout_ms: 5000,                                 // до 30 000
});
// r = {status: 201, headers: {"content-type": "application/json"}, body: "{\"id\": 1}"}
const item = JSON.parse(r.body);
```

- **Куда можно.** Только хосты из `net` манифеста; запись без порта разрешает порты 80 и 443.
  Собственные адреса панели, приватные, loopback, CGNAT, NAT64, 6to4 и site-local адреса
  недоступны, даже если их вернул DNS разрешённого имени.
- **Тело.** Строка отправляется как есть, другое значение — как JSON (заголовок
  `Content-Type: application/json` ставится сам, если не задан). Blob — потоком.
  `form: {…}` — multipart/form-data (см. blob ниже).
- **Ответ.** `body` всегда текст — JSON разбирайте сами; имена заголовков в нижнем
  регистре; `{blob: true}` кладёт ответ в blob.
- **Ошибки.** Ответ 4xx/5xx — не исключение, проверяйте `status`. Исключение — при сетевой
  ошибке, таймауте, хосте не из `net`, превышении лимитов. В тексте ошибки только
  `схема://хост`, без пути и параметров.
- **Лимиты.** Тело запроса строкой — до 1 МБ, ответ — до 4 МБ, до 30 с, до 5 редиректов и
  только на хосты из `net`. Заголовок `User-Agent: RosPanel-Plugin/1`, если вы не задали свой.

## panel.blob — большие данные

Blob — данные, которые панель держит во временном файле вне памяти плагина в пределах одного
вызова.

```js
const file = panel.blob.from("id,name\n1,Ann\n");          // из текста или {base64}
panel.blob.hash(file, "sha256");                            // hex (или "base64")
panel.blob.text(file);                                      // обратно в строку, до 4 МБ
panel.blob.text(file, "base64");                            // или base64

// источники
const dump = panel.api("GET", "/v1/users?limit=1000", null, { blob: true }).body;
const img = panel.http.fetch("https://cdn.example.com/a.png", { blob: true }).body;

// приёмники
panel.http.fetch("https://s3.example.com/bucket/a.png", { method: "PUT", body: img });
panel.http.fetch("https://api.telegram.org/bot…/sendDocument", {
  method: "POST",
  form: { chat_id: "42", caption: "отчёт", document: { blob: file, filename: "report.csv", type: "text/csv" } },
});
```

- **Ручка.** Blob — это `{blob: "b…", size: 123}`. Его можно передавать дальше в пределах
  вызова. Сохранённая в `kv` ручка после вызова мертва: файлы удаляются, как только вызов
  закончился.
- **Поля `form`.** Строка или число — текстовое поле. Blob — файл (имя файла = имя поля).
  `{blob, filename, type}` — файл со своим именем и типом.
- **Лимиты.** До 256 МБ на blob, 16 blob и 512 МБ на вызов. Время передачи всё равно
  ограничено временем вызова (например, 60 с у cron).

## panel.crypto

```js
panel.crypto.hash("sha256", "text")                       // hex
panel.crypto.hash("md5", { base64: "AAEC" }, "base64")
panel.crypto.hmac("sha256", panel.config.secret, body)    // подпись вебхука
panel.crypto.sign("RS256", privatePem, data)              // base64
panel.crypto.verify("Ed25519", publicPem, data, sigBase64) // true/false
panel.crypto.jwt("RS256", privatePem, { iss: "…", exp: Math.floor(Date.now() / 1000) + 3600 })
panel.crypto.randomHex(16)                                // 32 hex-символа
panel.crypto.randomUUID()
```

| Функция | |
|---|---|
| `hash(alg, data, enc?)` | `alg`: `md5`, `sha1`, `sha256`, `sha512`; `enc`: `hex` (по умолчанию), `base64`, `base64url` |
| `hmac(alg, key, data, enc?)` | те же `alg` и `enc` |
| `sign(alg, pem, data)` | `RS256`, `RS512`, `ES256` (подпись `r‖s`, как в JWS), `Ed25519`; результат base64 |
| `verify(alg, pem, data, sig)` | `sig` в base64; ключ — открытый, закрытый или сертификат |
| `jwt(alg, pem, claims, header?)` | готовый `header.claims.signature`; `typ: JWT` и `alg` (для Ed25519 — `EdDSA`) ставятся сами, остальное из `header` добавляется |
| `randomHex(n)` | `n` случайных байт (1–1024) в hex |
| `randomUUID()` | UUID v4 |

`data` и `key` — строка или `{base64}`. Ключи в PEM: `PRIVATE KEY` (PKCS#8),
`RSA PRIVATE KEY`, `EC PRIVATE KEY`, `PUBLIC KEY`, `RSA PUBLIC KEY`, `CERTIFICATE`.

Подпись вебхука проверяют так: посчитать `hmac` от тела ровно в том виде, в каком оно
пришло (строка `body`, без повторной сериализации JSON), и сравнить с заголовком целиком.

## panel.time — часовой пояс панели

```js
const at = e.data.expire_at;                          // unix-секунды
const local = new Date((at + panel.time.offset(at)) * 1000);
local.getUTCHours();                                  // час — как его показывает панель
```

`offset(unix)` — на сколько секунд часовой пояс панели (Настройки → Основное) опережает UTC в
этот момент; без аргумента — сейчас. Своей базы часовых поясов в песочнице нет.

## panel.t — переводы

```js
// i18n/ru.json: {"hello": "Привет, {name}!"}
panel.t("hello", { name: "Аня" }, "ru")   // "Привет, Аня!"
panel.t("hello", { name: "Ann" })          // язык вызова, иначе en, иначе ru
panel.t("нет такого ключа")                // возвращает сам ключ
```

Ищет ключ в `i18n/<lang>.json`, затем в `en`, затем в `ru`. Если ключа нет нигде,
возвращает сам ключ. `{имя}` заменяется значением из `params` — значения передавайте
строками. Если `lang` не указан, берётся язык вызова: у решений и бота — язык пользователя.
