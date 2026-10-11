# Contributing

Thanks for helping. Before you start, read `ARCHITECTURE.md`: the layers and
rules there keep the project clean as it grows.

## Develop

```
go run ./cmd/gitswitch help      # run from source
go test ./...                    # all tests (unit, integration, architecture)
go vet ./... && gofmt -l .       # must be clean
./build.sh                       # every build this machine can make, into dist/
```

## Try changes safely

Test with real accounts without touching your own git setup. Pick a way:

| | Native | Docker |
|---|---|---|
| Needs | Go | Docker (Go optional) |
| Runs on | your own OS, in a throwaway home folder | a Linux desktop in your browser (Webtop) |
| CLI, setup, terminal app | yes | yes |
| Desktop app, popups, clipboard, app menu | yes, on your own desktop | yes, on Linux |
| Best for | everyday work | Linux behavior, or no Go installed |

### Native: throwaway home folder (macOS, Linux, WSL)

Build first: Go keeps its caches under `$HOME`, so building after the switch
downloads everything again. The switch only affects that one terminal.

```
mkdir -p /tmp/gs-sandbox/bin
go build -tags desktop,production -o /tmp/gs-sandbox/bin/gitswitch ./cmd/gitswitch
export HOME=/tmp/gs-sandbox; unset XDG_CONFIG_HOME
export PATH=/tmp/gs-sandbox/bin:$PATH
gitswitch install && gitswitch setup
```

`~` now means the sandbox, so pass existing keys by full path
(`--key /Users/you/.ssh/id_ed25519`). After a code change, run the `go build`
line again. On Linux add `,webkit2_41` to the tags and install
`libgtk-3-dev libwebkit2gtk-4.1-dev`; drop `-tags ...` to build without the
window. Clean up by closing the terminal and `rm -rf /tmp/gs-sandbox`.

### Docker: Linux desktop (Webtop)

`sandbox/compose.yaml` runs an Ubuntu desktop at http://localhost:3000 (only
reachable from your machine). The desktop user's home is `sandbox/config/`,
which git ignores because it holds SSH private keys. The repo's `dist/` is
mounted read-only at `/opt/gitswitch`: you build on the host and run the
result on the desktop. Run these from the repo root.

1. Start the desktop:

   ```
   docker compose -f sandbox/compose.yaml up -d
   ```

2. Build for Linux with the desktop window. The window needs cgo and
   WebKitGTK, so build in a Linux container (it matches your CPU: arm64 on
   Apple Silicon, amd64 on Intel or AMD). The `gomodcache` volume keeps
   downloads between builds:

   ```
   docker run --rm -v "$PWD":/src -v gomodcache:/go/pkg/mod -w /src golang:1.27 sh -c \
     'apt-get update -qq && apt-get install -y -qq libgtk-3-dev libwebkit2gtk-4.1-dev >/dev/null &&
      go build -buildvcs=false -tags desktop,production,webkit2_41 -o dist/gitswitch-linux ./cmd/gitswitch'
   ```

   For the CLI only (no window, no cgo), `CGO_ENABLED=0 GOOS=linux GOARCH=arm64
   go build -o dist/gitswitch-linux ./cmd/gitswitch` also works from macOS.

3. Add the tools gitswitch uses (ssh, zenity for popups, WebKitGTK for the
   window):

   ```
   docker exec gitswitch-desktop bash -c 'apt-get update && apt-get install -y openssh-client zenity libwebkit2gtk-4.1-0'
   ```

4. Install gitswitch for the desktop user:

   ```
   docker exec -u abc -e HOME=/config gitswitch-desktop /opt/gitswitch/gitswitch-linux install
   ```

5. Open http://localhost:3000, open a terminal from the app menu and run
   `gitswitch setup`, then `gitswitch` (terminal app) or `gitswitch ui`
   (desktop app, opens a window on the desktop). Opening it from the app
   menu works too, and a second `gitswitch ui` brings the open window to
   the front.

After a code change, repeat steps 2 and 4. Stop the desktop with
`docker compose -f sandbox/compose.yaml down`. That removes the container, so
repeat step 3 after the next `up`; accounts and keys stay in
`sandbox/config/`. Start over by deleting `sandbox/config/`.

### What a sandbox does not isolate

- **GitHub.** SSH keys you add to your GitHub accounts are real. Remove them
  when you are done.
- **`~/.ssh/known_hosts` on macOS and Linux** in the native sandbox:
  ssh finds it from your user account, not `$HOME`, so a first connection may
  add a host entry there. This is harmless.

## Planned work

[ROADMAP.md](ROADMAP.md) lists features that are planned but not built yet:
repository management (browsing and cloning repos from every GitHub account,
a shared repo list, favorites, grouping) and a full git client. Each item
says whether it is done, partly done or planned, plus notes on the approach.

If you want to build one:

1. Open an issue named after the item (for example "Roadmap #5: Clone via
   URL") and say you are working on it, so nobody else builds it too.
2. Check [ARCHITECTURE.md](ARCHITECTURE.md) for which layer it goes in.
   Repo features go through `internal/repo` and `internal/app`; GitHub API
   work goes in a new `hosting` package.
3. Build it for every front end that the item names (desktop app, terminal
   app, CLI), and update its row in ROADMAP.md in the same pull request.

## Pull requests

- Put new behavior in the right layer; front ends only call `internal/app`.
- Add tests: a unit test next to the code, and an integration test in
  `test/integration` for anything git or the terminal can observe.
- New packages need a layer in `test/architecture/layers_test.go` and in
  `ARCHITECTURE.md`.
- Keep messages plain and specific: say what happened and how to fix it.
- CI must pass on Linux, macOS and Windows.

## Publishing a release (maintainers)

1. Push a tag: `git tag v1.0.0 && git push origin v1.0.0`.
2. The `release` workflow runs the tests, builds every binary (macOS and
   Linux on their own runners, for the desktop window) plus `SHA256SUMS`, and
   attaches them to the GitHub release.
3. The one-line installers always fetch the latest release.
