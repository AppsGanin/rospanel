# Smoke suite

Playwright specs that drive a **running panel**. They do not start one: a panel needs
a data directory, an Xray binary and a first-run admin, and standing that up inside a
test runner buys nothing that a one-line command does not.

## Running

```sh
# 1. a scratch panel (first run prints the admin password; the default here is admin/admin)
ROSPANEL_HOST= ROSPANEL_DATA=/tmp/rospanel-e2e ROSPANEL_ADMIN_ADDR=127.0.0.1:8099 \
  go run ./cmd/rospanel

# 2. the suite, from web/ — the panel URL INCLUDES the secret path
PANEL_URL=http://127.0.0.1:8099/rospanel/ npm run e2e
```

Point it elsewhere with `PANEL_URL`, `PANEL_USER` and `PANEL_PASS`:

```sh
PANEL_URL=https://panel.example.com/abcdef123/ PANEL_PASS=… npm run e2e
```

`npx playwright install chromium` once, if the browser is not there yet.

## What the panel has to have on it

`console.spec.ts` asserts against real data, so a panel with nothing on it fails six
of its specs for reasons that have nothing to do with the console. A brand-new data
directory also lands on the first-run wizard, which is not a screen the suite knows.
Seeding one, once (`sqlite3` on the panel's own database, the rest through its API):

```sh
D=/tmp/rospanel-e2e; P=http://127.0.0.1:8099/rospanel

# past the wizard and the forced password change
sqlite3 $D/rospanel.db "UPDATE settings SET setup_done=1, must_change_password=0, tls_mode='self';
                        UPDATE admins SET must_change_password=0;"
# (restart the panel here — it reads these at boot)

curl -sc /tmp/jar -H 'X-RosPanel-CSRF: 1' -H 'Content-Type: application/json' \
  -X POST $P/api/login -d '{"username":"admin","password":"admin"}'
for n in Ольга Андрей Мария; do
  curl -sb /tmp/jar -H 'X-RosPanel-CSRF: 1' -H 'Content-Type: application/json' \
    -X POST $P/api/users -d "{\"name\":\"$n\"}"
done
# tariff plans exist by default; billing has to be ON for the plan picker to appear
curl -sb /tmp/jar -H 'X-RosPanel-CSRF: 1' -H 'Content-Type: application/json' \
  -X POST $P/api/billing -d '{"enabled":true,"free_plan_id":1,"payment_note":""}'
# a few weeks of traffic, so the charts have something to draw
sqlite3 $D/rospanel.db "WITH RECURSIVE d(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM d WHERE n<44)
  INSERT OR REPLACE INTO traffic_daily(user_id,node_id,day,up,down)
  SELECT u.id, 0, date('now', -n || ' days'), 40000000+n*700000, 300000000+n*5000000
  FROM users u, d;"
```

The specs still change nothing themselves: they open dialogs and read what is there.

## What is checked

- **`layout.spec.ts`** walks every screen at 320 / 390 / 700 / 1440 and fails on the
  four ways this console has actually broken: the page scrolling sideways, a block
  sticking out of its container, a value landing on its own label, and text cut off
  with no ellipsis to admit it. It also fails on any page error raised during the walk.
- **`console.spec.ts`** covers what the redesign built: a tariff on a new user, the
  user card's icon toolbar and its journal as a table, the server settings drawer with
  all nine tabs, an egress switch that says "unsaved" instead of claiming to be up,
  the node log window that keeps its height when empty, and the two traffic charts.

## What they are not

They read real data from whatever panel they are pointed at, so they assert shapes and
states rather than figures, and they change nothing: no user is created, no setting is
saved. A spec that needs a fixture would need a seeded database, which is a bigger
decision than this suite.
