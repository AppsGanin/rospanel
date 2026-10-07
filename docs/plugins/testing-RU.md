# Тесты и отладка

[English version](testing.md) · [Оглавление](README-RU.md)

Инструменты встроены в `rospanel`:

```sh
rospanel plugin test .        # запустить test.js
rospanel plugin dev .         # интерактивная консоль с перезагрузкой при сохранении
rospanel plugin validate .    # проверить пакет так, как его проверит панель
```

Плагин в тестах и в `dev` работает в той же песочнице, что и в панели: те же лимиты, та же
база SQLite с вашими миграциями. Своя база создаётся заново при каждом запуске
`test`/`dev`.

## dev.config.json

Настройки, с которыми плагин работает в `test` и `dev`, — то, что оператор впишет в форму:

```json
{"settings": {"token": "test-token", "mode": "test", "limit": "100"}}
```

Значения — строки, как в `panel.config`. Если обязательное поле не заполнено, плагин не
стартует: `fill in the settings first: token`.

## test.js

`test.js` выполняется в отдельной песочнице рядом с плагином. Плагин вызывается через
`plugin.*`, его запросы наружу перехватываются `mock.*`.

```js
test("название теста", () => {
  mock.http("https://api.example.com/", { status: 200, body: { ok: true } });
  const r = plugin.call("onAction", { key: "sync", user_ids: [] });
  assert.equal(r.ok, true);
});
```

Тесты выполняются по порядку объявления. **Моки сбрасываются перед каждым тестом, а база и
`kv` плагина — нет**: данные, записанные одним тестом, видит следующий.

### test и assert

| | |
|---|---|
| `test(name, fn)` | объявить тест; исключение внутри `fn` — тест не прошёл |
| `assert.equal(actual, expected, msg?)` | глубокое сравнение через JSON; порядок ключей объектов не важен |
| `assert.ok(value, msg?)` | значение истинно |
| `assert.throws(fn, msg?)` | `fn` бросает исключение |

### plugin — вызвать плагин

| | |
|---|---|
| `plugin.call(export, arg?)` | вызвать экспорт и вернуть его ответ: `plugin.call("payment.create", {...})`, `plugin.call("userFields", 42)`, `plugin.call("bot.onCommand", {...})` |
| `plugin.event(name, data)` | доставить событие в `onEvent` так, как это делает панель: `{id, event, created_at: сейчас, data}` |
| `plugin.cron(name)` | выполнить задачу по расписанию |
| `plugin.kv.get/set/delete/list` | читать и наполнять `kv` плагина |
| `plugin.db.exec/query` | читать и наполнять базу плагина |

Исключение в плагине пробрасывается в тест — `assert.throws(() => plugin.event(...))`
проверяет, что плагин упал.

### mock — ответы на запросы плагина

| | |
|---|---|
| `mock.http(prefix, {status?, headers?, body?})` | ответ на `panel.http.fetch` для URL, начинающихся с `prefix`. `body` строкой или объектом (станет JSON), `status` по умолчанию 200 |
| `mock.api(method, path, {status?, body?})` | ответ на `panel.api`. `path` точный (`/v1/users/7`) или с `*` на конце (`/v1/users/*`) |
| `mock.calls()` | что плагин запросил: `[{kind: "http"\|"api", method, url, body}]` |
| `mock.reset()` | забыть моки и записанные вызовы |

- Незамоканный `fetch` в тесте — исключение (`no mock for GET https://… — add mock.http(…)`).
  Незамоканный `panel.api` отвечает `501` с подсказкой. Тест никогда не ходит в сеть.
- **Ограничения `net` действуют и для моков:** запрос к хосту, которого нет в манифесте,
  отклоняется, как в панели.
- Тело запроса из `form` или blob записывается в `mock.calls()` как текст (первый МБ) —
  проверяйте поля multipart через `includes`.

### crypto — подписать то, что тест отправляет плагину

В тестах есть `crypto.hash`, `crypto.hmac`, `crypto.sign`, `crypto.jwt` — те же функции, что
`panel.crypto`. Ими тест подписывает уведомление «от платёжной системы»:

```js
test("signed webhook is accepted", () => {
  const body = JSON.stringify({ id: "inv-1", status: "paid", amount: 150 });
  const sign = crypto.hmac("sha256", "k3y", body);
  const r = plugin.call("payment.webhook", { body, headers: { "x-signature": sign } });
  assert.equal(r.status, "paid");
});
```

### Пример

Плагин с задачей `sync`, которая читает пользователей через API и запоминает, сколько их:

```js
test("sync counts the users", () => {
  mock.api("GET", "/v1/users*", { body: { data: [{ id: 7 }, { id: 8 }] } });
  plugin.cron("sync");
  assert.equal(plugin.kv.get("last_sync").count, 2);
  assert.equal(mock.calls()[0].url, "/v1/users?limit=1000");
});

test("kv from the previous test is still there", () => {
  assert.ok(plugin.kv.get("last_sync"));
});
```

Ещё примеры — в `test.js` каждого плагина из [examples/plugins](../../examples/plugins).

Вывод:

```
  ✓ sync counts the users
  ✗ kv from the previous test is still there
      expected a truthy value, got undefined

1 passed, 1 failed
```

Код выхода ненулевой, если хоть один тест не прошёл, — `rospanel plugin test` можно ставить в CI.

## dev — консоль

```sh
rospanel plugin dev .
```

```
my-plugin is running. Type help for commands.
> event user.created {"id": 1, "name": "Ann"}
> call userFields 1
{"email":"ann@example.com"}
> cron sync
> kv user/
> sql SELECT * FROM orders LIMIT 5
> mock api GET /v1/users/1 {"data": {"id": 1, "name": "Ann"}}
> mock http https://api.example.com/ {"ok": true}
> logs
> reload
> quit
```

| Команда | |
|---|---|
| `event <имя> [json]` | доставить событие в `onEvent` |
| `call <экспорт> [json]` | вызвать экспорт: `call payment.create {"order_id": 1, "amount_rub": 100}` |
| `cron <имя>` | выполнить задачу |
| `kv [префикс]` | показать `kv` |
| `sql <запрос>` | выполнить запрос к базе плагина |
| `mock api <МЕТОД> <путь> <json>` | ответ для `panel.api` |
| `mock http <префикс-url> <json>` | ответ для `panel.http.fetch` |
| `logs` | лог плагина |
| `reload` | перезагрузить сейчас (файлы и так отслеживаются) |
| `help`, `quit` | |

Отличия от `test`:

- **Сеть настоящая.** Незамоканный `fetch` уходит в интернет (только к хостам из `net`).
  Так удобно проверить настоящий сервис с токеном из `dev.config.json`.
- **Перезагрузка.** При сохранении любого файла плагин перезагружается как при обновлении:
  база и `kv` сохраняются, новые миграции применяются. Если вы поправили уже применённую
  миграцию или `dev.config.json`, перезапустите `dev`.
- **Настоящая панель.** С `--panel` и `--key` вызовы `panel.api` идут в неё:

  ```sh
  rospanel plugin dev . --panel https://vpn.example.com/<путь API> --key rp_…
  ```

  Путь API и ключ — в **Настройки → API** панели. Возьмите ключ только с нужными правами:
  изменения через него настоящие.

## validate — перед публикацией

```sh
rospanel plugin validate .
rospanel plugin validate my-plugin-1.2.0.zip
```

```
ok: my-plugin 1.2.0 — 14 KB, exports onEvent, sync, userFields
```

`validate` проверяет манифест, состав пакета и стили темы, загружает код, проверяет экспорты
и выводит все проблемы списком.

## Отладка в панели

- **Плагины → Лог** — последние 1000 строк: ваши `panel.log`/`console`, ошибки вызовов с
  причиной, остановки плагина.
- **Плагины → Код** — то, что сейчас установлено.
- **Журнал панели** — что плагин изменил через `panel.api` (актор `plugin:<id>`) и нажатия
  его кнопок.
- Если плагин остановлен (`paused`), в его карточке написана причина; включите его снова,
  когда она устранена.

Частые ошибки и их значение — в [лимитах и ошибках](troubleshooting-RU.md).
