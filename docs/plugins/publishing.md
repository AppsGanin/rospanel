# Publishing and updates

[Русская версия](publishing-RU.md) · [Contents](README.md)

## How a plugin gets into a panel

| Way | How |
|---|---|
| A file | `rospanel plugin pack .` → zip → **Settings → Plugins → Install** → choose the file |
| A link | the same place, paste an https link to the zip (a GitHub release, say) |
| The catalog | **Settings → Plugins → Catalog** → **Install** |

Each way, the operator sees the consent screen: permissions, hosts, what the plugin does,
experimental points, and for the catalog the "reviewed" mark. They install it with their
password, and the plugin is installed switched off. The operator switches it on once its
settings are filled in.

## Versions

`version` is `X.Y.Z`. Any change of the package needs a new number: the panel and the catalog
tell versions apart by it.

- **`panel: ">=X.Y.Z"`** is the oldest panel the plugin runs on. Using a point or a field that
  came later? Raise it: an older panel then refuses the plugin, saying why, instead of failing
  at run time.
- **`api: 1`** is the plugin contract. Within one API version things are only added: fields,
  calls, points. What exists is never changed or renamed. The exception is points under
  `experimental`.

## Updating

A new version installs from the **Update** button on the plugin's card (a zip or a link), or
**Update to X** in the catalog. What happens:

1. The panel checks it is the same `id` and shows the consent screen. New permissions and
   hosts are marked and must be approved again.
2. It snapshots the plugin's database.
3. It applies the new migrations and starts the new code, calling `onEnable`.
4. If any of that fails, it puts the previous version and database back by itself.

Settings are kept. New fields get their defaults, and fields removed from the manifest are
dropped.

**Roll back** on the card restores the previous package together with the database snapshot
taken before its migrations. One previous version is kept.

## Migrations

- **A shipped migration is never edited.** The panel keeps its hash and refuses an update
  that changed it. Schema changes go only in new files: `0002_add_email.sql`,
  `0003_index.sql`.
- **The order is by name,** so number them with leading zeros.
- **Check before a release:**

  ```sh
  rospanel plugin pack . --prev my-plugin-1.1.0.zip
  ```

  `--prev` compares the migrations with the previous package and refuses to build if a
  shipped one changed or is gone.
- A migration runs whole or not at all. If one fails, the update is not installed and the
  database stays as it was.

## Data compatibility

Your database lives through updates, and sometimes rollbacks. So:

- add new columns with a `DEFAULT` or as nullable;
- the new version's code must understand the old `kv` entries;
- do not drop tables the previous version reads in the same version that stops using them —
  a rollback would bring back code without its data.

## The catalog

The community catalog is the
[rospanel-plugins](https://github.com/AppsGanin/rospanel-plugins) repository:

```
plugins/<id>/<id>-<version>.zip   packages
verified.json                     versions whose code the maintainers read
index.json, index.json.sig        the index, signed with the catalog's key
```

The panel trusts the index only when it is signed by a key built into the panel, and installs
a package only when its sha256 matches the index. So neither a mirror nor anyone in between
can swap a plugin.

### Publishing

1. Put the sources in an open repository: the plugin's code must be readable.
2. Build the package: `rospanel plugin pack .` (and `--prev` for a new version).
3. Open a pull request in `rospanel-plugins` with `plugins/<id>/<id>-<version>.zip` and links
   to the sources and the commit it was built from.
4. A maintainer checks it, rebuilds the index and signs it. Once the code is read, the version
   gets the "reviewed" mark.

A published zip never changes: a new version is a new file next to it.

### What is checked for "reviewed"

- **Permissions.** The plugin asks only for what it uses; `net` lists only the hosts it needs.
- **Users' data** goes nowhere the description does not say.
- **Secrets** stay out of the log and out of `onHttp` answers.
- **Webhooks and `onHttp`** check a signature or a token; a payment `webhook` does not trust
  an unsigned body and re-checks the payment through `status`.
- **Retries.** Events are handled idempotently: a repeated delivery duplicates nothing.
- **No sandbox games.** No obfuscation, no code loaded from elsewhere (`eval` of what came
  from the network).
- **Migrations** do not break rolling back to the previous version.

### Updates at operators

Every few hours the panel compares installed plugins with the catalog. It tells the admin bot
once about a new version (the "Plugins" category) and shows "X in the catalog" on the card.
The panel updates nothing by itself — that is the operator's call.

### A mirror

Where GitHub is unreachable, the operator sets the address of a copy of the catalog:
**Catalog → Catalog address**. A mirror is the same folder on another host. The same signature
is checked, so the mirror need not be trusted.

## License

Set `license` in the manifest and put `LICENSE` in the package. The catalog takes open
licenses (MIT, Apache-2.0, GPL and the like), so that operators can read and fix the code.
