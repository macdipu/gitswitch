# Contributing

Thanks for helping. Before you start, read `ARCHITECTURE.md`: the layers and
rules there keep the project clean as it grows.

## Develop

```
go run ./cmd/gitswitch help      # run from source
go run -tags desktop,production ./cmd/gitswitch-app   # the desktop app (Linux: add ,webkit2_41)
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
go build -o /tmp/gs-sandbox/bin/gitswitch ./cmd/gitswitch
go build -tags desktop,production -o /tmp/gs-sandbox/bin/gitswitch-app ./cmd/gitswitch-app
export HOME=/tmp/gs-sandbox; unset XDG_CONFIG_HOME
export PATH=/tmp/gs-sandbox/bin:$PATH
gitswitch install && gitswitch setup
```

`~` now means the sandbox, so pass existing keys by full path
(`--key /Users/you/.ssh/id_ed25519`). After a code change, run the `go build`
lines again. `gitswitch install` also installs the app it finds next to
itself (on macOS into `~/Applications/GitSwitch.app` inside the sandbox
home). On Linux add `,webkit2_41` to the tags and install
`libgtk-3-dev libwebkit2gtk-4.1-dev`; skip the second line to try the
terminal programs alone. Clean up by closing the terminal and `rm -rf /tmp/gs-sandbox`.

### Docker: Linux desktop (Webtop)

`sandbox/compose.yaml` runs an Ubuntu desktop at http://localhost:3000
(https on port 3001). Both ports are published on every network interface,
so other machines on your network can reach the desktop too: stop it when
you are done. The desktop user's home is `sandbox/config/`, which git
ignores because it holds SSH private keys. `sandbox/workspace/` is shared
with the desktop at `/workspace`. You build on the host and copy the result
into the container. Run these from the repo root.

1. Start the desktop:

   ```
   docker compose -f sandbox/compose.yaml up -d
   ```

2. Build both programs for Linux. The desktop app needs cgo and
   WebKitGTK, so build in a Linux container (it matches your CPU: arm64 on
   Apple Silicon, amd64 on Intel or AMD). The `gomodcache` volume keeps
   downloads between builds:

   ```
   docker run --rm -v "$PWD":/src -v gomodcache:/go/pkg/mod -w /src golang:1.27 sh -c \
     'apt-get update -qq && apt-get install -y -qq libgtk-3-dev libwebkit2gtk-4.1-dev >/dev/null &&
      mkdir -p dist/linux && CGO_ENABLED=0 go build -buildvcs=false -o dist/linux/gitswitch ./cmd/gitswitch &&
      go build -buildvcs=false -tags desktop,production,webkit2_41 -o dist/linux/gitswitch-app ./cmd/gitswitch-app'
   ```

   `gitswitch` alone (no app, no cgo) also builds from macOS with
   `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/linux/gitswitch ./cmd/gitswitch`.

3. Add the tools gitswitch uses (ssh, zenity for popups, WebKitGTK for the
   window):

   ```
   docker exec linux-desktop bash -c 'apt-get update && apt-get install -y openssh-client zenity libwebkit2gtk-4.1-0'
   ```

4. Copy it in and install it for the desktop user:

   ```
   docker cp dist/linux linux-desktop:/tmp/gs
   docker exec -u abc -e HOME=/config linux-desktop /tmp/gs/gitswitch install
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
2. Check [ARCHITECTURE.md](ARCHITECTURE.md) for where it goes. Repo
   features are use cases in `internal/features/repository/application`;
   GitHub API work goes in a new `internal/features/hosting` feature.
   Front ends reach new use cases through `bootstrap.App`.
3. Build it for every front end that the item names (desktop app, terminal
   app, CLI), and update its row in ROADMAP.md in the same pull request.

## Pull requests

- Put new behavior in the right feature and layer: rules in `domain`, use
  cases in `application`, git/ssh/file code in `infrastructure`. Front ends
  only call use cases (through `bootstrap.App`).
- Add tests: a unit test next to the code, and an integration test in
  `test/integration` for anything git or the terminal can observe.
- New packages must fit the layout in `ARCHITECTURE.md`;
  `test/architecture` fails for a package without a role or with an import
  the rules don't allow.
- Keep messages plain and specific: say what happened and how to fix it.
  For a git or ssh error people often hit, add a case to
  `internal/features/git/domain/explain.go`; the CLI, terminal app and
  desktop app all show it.
- Pin any new GitHub Action to a full commit SHA, with the version in a
  comment, like the existing workflows.
- CI must pass on Linux, macOS and Windows.

## Publishing a release (maintainers)

1. Push a tag: `git tag v1.0.0 && git push origin v1.0.0`.
2. The `release` workflow runs the tests, builds `gitswitch-*` and
   `gitswitch-app-*` for every platform (the app for macOS and Linux on their
   own runners; Linux on Ubuntu 22.04 so it starts on older systems) plus
   `SHA256SUMS`, and
   attaches them to the GitHub release.
3. The one-line installers always fetch the latest release.
