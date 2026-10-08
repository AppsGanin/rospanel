# Первый плагин: шаг за шагом

[English version](tutorial.md) · [Оглавление](README-RU.md)

Напишем плагин, который сообщает оператору в Telegram о каждом новом пользователе и раз в день
присылает сводку. По пути встретятся манифест, события, задачи по расписанию, настройки, своя
база, запросы в интернет, переводы и тесты — почти всё, из чего состоит любой плагин.

Готовый результат лежит в [examples/plugins/new-users](../../examples/plugins/new-users).

Нужен только бинарник `rospanel` (тот же, что стоит на сервере; подойдёт и собранный под ваш
компьютер). Node.js не нужен.

## 1. Создаём заготовку

```sh
rospanel plugin new new-users
cd new-users
```

```
new-users/
  plugin.json             манифест: кто плагин, что ему можно, на что он отвечает
  main.js                 код
  migrations/0001_init.sql  таблицы своей базы
  i18n/ru.json, en.json   строки для panel.t()
  test.js                 тесты (в пакет не попадают)
  dev.config.json         настройки для тестов и dev (в пакет не попадают)
  rospanel.d.ts           типы для редактора (в пакет не попадает)
  jsconfig.json           подключает типы в VS Code
  README.md
```

Заготовка уже рабочая: `rospanel plugin test .` проходит. Откройте папку в VS Code —
подсказки по `panel.*` появятся сами, благодаря `rospanel.d.ts`.

## 2. Манифест

`plugin.json` описывает плагин. Перепишите его так:

```json
{
  "id": "new-users",
  "version": "1.0.0",
  "api": 1,
  "panel": ">=4.4.0",
  "name": {"ru": "Новые пользователи — в Telegram", "en": "New users to Telegram"},
  "description": {
    "ru": "Пишет в ваш чат Telegram о каждом новом пользователе и раз в день присылает сводку.",
    "en": "Tells your Telegram chat about every new user and sends a summary once a day."
  },
  "author": "you",
  "license": "MIT",
  "permissions": ["users.view"],
  "net": ["api.telegram.org"],
  "settings": [
    {"key": "bot_token", "kind": "secret", "label": {"ru": "Токен бота", "en": "Bot token"},
     "help": {"ru": "Создайте бота у @BotFather", "en": "Create a bot with @BotFather"}},
    {"key": "chat_id", "kind": "text", "label": {"ru": "ID чата", "en": "Chat ID"}, "placeholder": "-1001234567890"},
    {"key": "lang", "kind": "select", "label": {"ru": "Язык сообщений", "en": "Message language"}, "default": "ru",
     "options": [{"value": "ru", "label": "Русский"}, {"value": "en", "label": "English"}]}
  ],
  "provides": {
    "events": ["user.created", "user.registered"],
    "cron": [{"name": "summary", "schedule": "0 9 * * *"}]
  }
}
```

Что здесь важно:

- **`id`** никогда не меняется: по нему называется база плагина и записи в журнале панели.
- **`permissions`** — что плагин может делать через API панели и что ему передают. События
  пользователей несут пользователя, поэтому им нужно `users.view`; оператор увидит этот
  список перед установкой.
- **`net`** — единственные хосты, куда плагин может ходить. Без `api.telegram.org` запрос в
  Telegram будет отклонён.
- **`settings`** — форма, которую оператор заполнит после установки. `secret` хранится
  зашифрованным и больше никому не показывается.
- **`provides`** — на что плагин отвечает: два события и одна задача, каждый день в 9:00 по
  часовому поясу панели.

Каждое поле описано в [справочнике по манифесту](manifest-RU.md).

## 3. Своя база

`migrations/0001_init.sql`:

```sql
-- Один ряд на каждого объявленного пользователя: это и считает сводка, и не даёт
-- объявить одного человека дважды, если событие придёт повторно.
CREATE TABLE announced (
    user_id INTEGER PRIMARY KEY,
    name    TEXT    NOT NULL,
    source  TEXT    NOT NULL,
    at      INTEGER NOT NULL
);
```

Миграции применяются по порядку имён при включении плагина. Выпущенную миграцию не меняют —
для изменений добавляют новую (`0002_….sql`).

## 4. Строки

`i18n/ru.json`:

```json
{
  "new_user": "🆕 Новый пользователь: {name} (#{id})",
  "self_registered": "зарегистрировался сам",
  "created": "создан в панели",
  "summary": "📊 За сутки новых пользователей: {count}"
}
```

И то же по-английски в `i18n/en.json`. `panel.t("new_user", {name, id}, "ru")` подставит
значения в `{name}` и `{id}`.

## 5. Код

`main.js` — один ES-модуль. Панель вызывает то, что он экспортирует:

```js
// Пишет в Telegram оператора о новых пользователях и раз в день — сколько их пришло.

/** Отправляет сообщение через бота оператора; бросает ошибку, если Telegram отказал. */
function send(text) {
  const res = panel.http.fetch(`https://api.telegram.org/bot${panel.config.bot_token}/sendMessage`, {
    method: "POST",
    body: { chat_id: panel.config.chat_id, text },
  });
  if (res.status !== 200) throw new Error(`Telegram answered ${res.status}: ${res.body.slice(0, 200)}`);
}

/** @param {PanelEvent<{id: number, name: string}>} e */
export function onEvent(e) {
  const lang = panel.config.lang;
  const user = e.data;
  // Одна транзакция: если Telegram не ответил, ряд тоже откатится, и повторная доставка
  // события отправит сообщение снова. Событие, пришедшее дважды, найдёт ряд (первичный
  // ключ) и ничего не отправит.
  panel.db.tx(() => {
    const { changes } = panel.db.exec(
      "INSERT OR IGNORE INTO announced(user_id, name, source, at) VALUES (?, ?, ?, ?)",
      user.id, user.name, e.event, e.created_at,
    );
    if (changes === 0) return;
    const how = panel.t(e.event === "user.registered" ? "self_registered" : "created", {}, lang);
    send(`${panel.t("new_user", { name: user.name, id: String(user.id) }, lang)}\n${how}`);
  });
}

export function summary() {
  const since = Math.floor(Date.now() / 1000) - 86400;
  const [{ n }] = panel.db.query("SELECT count(*) AS n FROM announced WHERE at >= ?", since);
  send(panel.t("summary", { count: String(n) }, panel.config.lang));
}
```

Три вещи, которые надо понять про то, как панель вызывает код:

1. **Всё синхронно.** `panel.http.fetch` возвращает ответ сразу — без `await`, колбэков и
   таймеров. Ждать времени (`setTimeout`) нельзя.
2. **Событие может прийти больше одного раза.** Доставка «хотя бы один раз»: при сбое панель
   повторит её. Поэтому плагин помнит, кого уже объявил.
3. **Ошибка — это повтор.** Если `onEvent` бросил исключение, панель доставит событие снова
   через 10 с, 30 с, 2 мин и 10 мин. Транзакция выше нужна ровно для этого: запись о
   пользователе появляется только вместе с отправленным сообщением.

Про `panel.*` — в [справочнике по API](api-RU.md), про события и остальные точки — в
[справочнике по точкам расширения](exports-RU.md).

## 6. Тесты

`test.js` запускается рядом с плагином; сеть и API панели в нём настоящими не бывают — на всё
отвечают моки:

```js
const tg = "https://api.telegram.org/bot123:abc/sendMessage";

test("a new user is announced once", () => {
  mock.http(tg, { body: { ok: true } });

  plugin.event("user.registered", { id: 7, name: "Ann" });
  plugin.event("user.registered", { id: 7, name: "Ann" }); // то же событие ещё раз

  const sent = mock.calls().filter((c) => c.kind === "http");
  assert.equal(sent.length, 1);
  assert.equal(JSON.parse(sent[0].body), {
    chat_id: "-100500",
    text: "🆕 Новый пользователь: Ann (#7)\nзарегистрировался сам",
  });
});

test("after a Telegram error the retry sends it", () => {
  mock.http(tg, { status: 502, body: "bad gateway" });
  assert.throws(() => plugin.event("user.created", { id: 9, name: "Bob" }));

  mock.reset();
  mock.http(tg, { body: { ok: true } });
  plugin.event("user.created", { id: 9, name: "Bob" }); // панель доставляет его снова
  assert.equal(mock.calls().length, 1);
});
```

Настройки для тестов — в `dev.config.json`:

```json
{"settings": {"bot_token": "123:abc", "chat_id": "-100500", "lang": "ru"}}
```

```sh
rospanel plugin test .
```

```
  ✓ a new user is announced once
  ✓ after a Telegram error the retry sends it

2 passed, 0 failed
```

Все возможности тестов — в [справочнике по тестам](testing-RU.md).

## 7. Пробуем руками

```sh
rospanel plugin dev .
```

```
new-users is running. Type help for commands.
> mock http https://api.telegram.org/ {"ok": true}
> event user.created {"id": 1, "name": "Ann"}
> sql SELECT * FROM announced
> logs
```

`dev` перезагружает плагин при каждом сохранении файла. Запросы, которые не замоканы, уходят в
интернет по-настоящему — так можно проверить настоящего бота, вписав его токен в
`dev.config.json`.

## 8. Пакет и установка

```sh
rospanel plugin pack .
```

```
new-users-1.0.0.zip — 2 KB, sha256 …
not packed: dev.config.json, jsconfig.json, rospanel.d.ts, test.js
```

В панели: **Настройки → Плагины → Установить** → выберите zip. Панель покажет экран согласия:
права, хосты, что плагин делает. После установки плагин выключен — заполните настройки (токен,
чат) и включите его. Создайте пользователя — в чат придёт сообщение. **Лог** плагина
показывает, что он делал и где упал.

## Что дальше

- Кнопки в карточке пользователя, виджеты, блоки на странице подписки, свой способ оплаты,
  кнопки в боте — [точки расширения](exports-RU.md).
- Обновления, миграции и как поделиться плагином — [публикация](publishing-RU.md).
- Лимиты и разбор частых ошибок — [лимиты и ошибки](troubleshooting-RU.md).
- Другие готовые плагины — [examples/plugins](../../examples/plugins).
