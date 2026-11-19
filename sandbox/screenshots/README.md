# Screenshots

Scripts that build a small demo repo (fictional shop, people and accounts, under `/home/dana` inside a container, so nothing from your machine shows) and capture the desktop app for the README and the landing page into `docs/images/v2/`, as `<name>-light.webp`, `<name>-dark.webp` and `themes-dim.webp` (1440x900 at 2x, webp).

Status: `setup.sh` builds the demo; `hero.mjs`, `diffsplit.mjs` and `hunks.mjs` capture those shots, using `lib.mjs`. The other shots in the list (conflict, merge, park, stashes, blame, compare, palette, force-push, themes-dim) and the dark versions are still to do: write one `.mjs` per shot in the same style.

How to run:

1. Create the temporary page server `internal/zzdevserve/main.go` (`package main`; `http.ListenAndServe("0.0.0.0:8091", api.Handler(bootstrap.New(false, nil), nil))`). Delete it when done: `test/architecture` fails while it exists.
2. `sh sandbox/screenshots/run.sh`, then wait until `http://127.0.0.1:8091/` answers.
3. Start headless Edge or Chrome with `--headless=new --remote-debugging-port=9340 --hide-scrollbars`, then `node --experimental-websocket sandbox/screenshots/hero.mjs`.

The page is `go:embed`-ed, so rerun `run.sh` after changing `index.html`.
