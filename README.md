# GitSwitch

Use several git accounts (work, personal, clients) on one computer with normal clone URLs. GitSwitch picks the right SSH key and commit email for every clone, pull and push, in every terminal and IDE.

- **One file, one-time install.** A single program for Windows, macOS or Linux. Install once and it keeps working after restarts and updates.
- **Nothing running in the background.** Git starts GitSwitch for a moment on each network command (about 15 ms, about 10 MB), then it exits. The app window runs only while it is open and stops by itself about 30 seconds after you close it.
- **Two ways to manage it, same settings:** a terminal app (`gitswitch`) and a browser app (`gitswitch ui`). Change something in one and the other shows it.
- **Works everywhere git runs:** Windows Terminal, PowerShell, cmd, macOS Terminal, iTerm, VS Code (terminal and Source Control), Android Studio and other JetBrains IDEs, GitHub Desktop, SourceTree.

## Install

One line. It detects your system, downloads the right file from the latest release, checks its SHA-256 checksum, and installs it.

**macOS, Linux, WSL, Git Bash:**

```
curl -fsSL https://raw.githubusercontent.com/OWNER/gitswitch/main/install.sh | sh
```

**Windows (PowerShell):**

```
irm https://raw.githubusercontent.com/OWNER/gitswitch/main/install.ps1 | iex
```

Then open a new terminal and run `gitswitch` (terminal app), `gitswitch ui` (browser app) or `gitswitch setup` (step-by-step questions). Run the same line again to update. To install a specific version, set `GITSWITCH_VERSION=v1.2.0` first.

Manual install: download the file for your computer from the Releases page (`gitswitch-<os>-<cpu>`), then run it with `install`. On Windows you can simply double-click it.

### Publishing a release (maintainers)

1. Replace `OWNER/gitswitch` in `install.sh`, `install.ps1` and this README with your GitHub repo.
2. Push a tag: `git tag v1.0.0 && git push origin v1.0.0`.
3. The `release` workflow builds all six files plus `SHA256SUMS` and attaches them to the release. The one-line installers always fetch the latest release.

What `install` does, once:

- Copies itself to a fixed folder (`%LOCALAPPDATA%\GitSwitch` on Windows, `~/.local/bin` on macOS and Linux) and adds it to your PATH.
- Sets git's global `core.sshCommand` to GitSwitch. Every program that runs git reads this setting, which is why it works in every terminal and IDE without per-app setup.
- Sets `user.useConfigOnly = true` and moves your global `user.email` aside, so a commit can never silently use the wrong email. `gitswitch uninstall` puts everything back.

## Terminal app or browser app

| | Terminal app | Browser app |
| --- | --- | --- |
| Open it | `gitswitch` (or `gitswitch tui`) | `gitswitch ui`, or GitSwitch in the Start menu / app menu |
| Best for | VS Code and Android Studio terminals, SSH sessions, WSL, servers | Mouse users, the visual history graph |
| Pages | Accounts, Repos, History, Rules, Settings | Accounts, Repos, History, Rules, Settings |
| Memory while open | a few MB | about 8 MB plus the browser tab |
| Closes | press q | about 30 s after you close the tab |

Terminal app keys: `1`–`5` or Tab to switch pages, ↑↓ to move, `?` for all keys, `q` to quit.

| Page | Keys |
| --- | --- |
| Accounts | `a` add · `e` edit · `t` test login · `k` public key (`c` copy, `o` open GitHub page) · `d` make default · `p` load passphrase into agent · `x` remove |
| Repos | Enter choose account · `h` history · `f` add projects folder · `r` rescan |
| History | `o` other repo · `f` only wrong-account commits · `x` fix not-pushed commits · `r` reload |
| Rules | `a` add · `x` remove |
| Settings | Enter toggles an option or fixes a failed check |

When the terminal app creates a key or tests a login, it steps aside so `ssh-keygen` and `ssh` can ask for your passphrase, then comes back.

## Set up your accounts

In either app, or with step-by-step questions in the terminal:

```
gitswitch setup
```

Or one command per account (good for scripts):

```
gitswitch account add work --email you@company.com --name "Your Name" --key new --default
gitswitch account add personal --email you@gmail.com --key new
gitswitch key show work --open        # copies the public key, opens GitHub's SSH key page
gitswitch test                        # checks each key logs in, and as whom
gitswitch rule add owner:my-company work
gitswitch rule add folder:~/personal personal
```

Add each public key on GitHub (Settings, SSH and GPG keys, New SSH key) while signed in to the matching account.

## Daily use

Clone and push as usual: `git clone git@github.com:owner/repo.git`. For each repo GitSwitch decides in this order:

1. The account saved for the repo (`gitswitch use work`).
2. An owner rule (`owner:my-company` matches every repo of that org, even during `git clone`).
3. A folder rule (`folder:~/work`).
4. If you're in a terminal, it asks. Type `2` for this time or `2!` to save it for the repo.
5. If git runs with no terminal (an IDE's Source Control panel), a small popup asks.
6. Otherwise, the default account. Nothing ever hangs waiting for an answer.

It also sets the repo's `user.name` and `user.email` to the chosen account.

## IDE notes

- **VS Code:** nothing to set. The integrated terminal prompts; the Source Control panel uses rules, the popup or the default.
- **Android Studio / IntelliJ / other JetBrains IDEs:** if your version has Settings, Version Control, Git, **SSH executable**, choose **Native**. The Built-in option ignores git's settings.
- **Key passphrases in IDEs:** IDEs can't type a passphrase for you. Run `gitswitch key agent <account>` once in a terminal to store it in the OS agent or macOS Keychain.
- **Windows:** the Windows `ssh-agent` service remembers passphrases. `gitswitch doctor` tells you if it is off and how to turn it on (one admin command).
- **WSL:** WSL is a separate Linux system with its own git. Install the Linux file inside WSL too.

## History

`gitswitch log` shows the commit graph with the account of each commit. `gitswitch log --flagged` lists commits made with another of your accounts. `gitswitch fix-author` corrects those that are not pushed yet, on the current branch. Pushed commits are never changed. The app's History page shows the same graph.

## All commands

Run `gitswitch help`. Add `--dry-run` to any command to see what it would change without changing anything. Main ones: `install`, `setup`, `tui`, `ui`, `account add|edit|list|remove [--delete-keys]|rename`, `key new|show|pick|agent`, `completions bash|zsh|fish|powershell`, `test`, `default`, `use`, `forget`, `rule add|list|remove`, `status`, `log`, `fix-author`, `doctor [--fix]`, `set gui-prompt on|off`, `set prompt-timeout <s>`, `uninstall [--purge]`.

## Shell completions

```
eval "$(gitswitch completions bash)"                                   # ~/.bashrc
eval "$(gitswitch completions zsh)"                                    # ~/.zshrc
gitswitch completions fish > ~/.config/fish/completions/gitswitch.fish
gitswitch completions powershell | Out-String | Invoke-Expression      # $PROFILE
```

Completions include your account names.

## Health check

`gitswitch doctor` checks everything that can make git skip GitSwitch or log in as the wrong account, and `gitswitch doctor --fix` repairs what it can:

- `GIT_SSH_COMMAND` or `GIT_SSH` set in your environment or shell profile (git then ignores GitSwitch)
- repos with their own `core.sshCommand` (left by other tools)
- `~/.ssh/config` blocks that add other keys for your git hosts
- host keys not yet recorded (first push from an IDE would fail)
- Windows ssh-agent service off, missing key files, a global commit email

## Where settings live

Everything is stored in git config, so the terminal, the app and git always agree:

- Global (`~/.gitconfig`): `gitswitch-account.<id>.*`, `gitswitch.default`, `gitswitch.rule`, `gitswitch.guiPrompt`, `gitswitch.promptTimeout`, `gitswitch.scanFolder`.
- Per repo (`.git/config`): `gitswitch.account`.

GitSwitch never stores passphrases or tokens. The app listens only on 127.0.0.1 with a random one-time token.

## Tests

```
go test ./...
```

The tests build the real program and run real `git clone`, `pull` and `push` through it against local repos (no network). They cover installing and uninstalling, every way an account is chosen, clones inside other repos, submodules, port-443 hosts, renames, duplicate keys, dry runs, health checks, history and author fixes, the browser app's security, and the terminal prompt. GitHub Actions runs them on Linux, macOS and Windows for every push.

## Build from source

Needs Go 1.22 or newer. No other dependencies.

```
go build -o gitswitch ./cmd/gitswitch
./build.sh v1.0.0          # all platforms + SHA256SUMS into dist/
```

See `ARCHITECTURE.md` for how the code is organised and `CONTRIBUTING.md` to help.
