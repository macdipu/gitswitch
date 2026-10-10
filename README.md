# GitSwitch

[![test](https://github.com/macdipu/gitswitch/actions/workflows/test.yml/badge.svg)](https://github.com/macdipu/gitswitch/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/macdipu/gitswitch?sort=semver)](https://github.com/macdipu/gitswitch/releases/latest)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8?logo=go)](go.mod)
![platforms](https://img.shields.io/badge/platforms-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)

**Use several git accounts (work, personal, clients) on one computer with normal clone URLs.**
GitSwitch picks the right SSH key and commit email for every clone, pull and push, in every terminal and IDE.

```
$ git push

Git account for my-company/api?
  [Enter] personal   (default)
  1  personal         you@gmail.com
  2  work             you@company.com
Type a number for this time, or add ! to always use it for this repo (e.g. 2!): 2!
```

- **One file, one-time install.** A single binary for Windows, macOS and Linux. Keeps working after restarts and updates.
- **Nothing running in the background.** Git starts GitSwitch for a moment on each network command (~15 ms, ~10 MB), then it exits.
- **Two front ends, same settings:** a terminal app (`gitswitch`) and a browser app (`gitswitch ui`).
- **Works everywhere git runs:** Windows Terminal, PowerShell, cmd, macOS Terminal, iTerm, VS Code, Android Studio and other JetBrains IDEs, GitHub Desktop, SourceTree.

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [How an account is chosen](#how-an-account-is-chosen)
- [Terminal app and browser app](#terminal-app-and-browser-app)
- [IDE notes](#ide-notes)
- [Commit history](#commit-history)
- [Health check](#health-check)
- [Command reference](#command-reference)
- [How it works](#how-it-works)
- [Uninstall](#uninstall)
- [Development](#development)

## Install

The installer detects your system, downloads the matching binary from the [latest release](https://github.com/macdipu/gitswitch/releases/latest), verifies its SHA-256 checksum and installs it.

**macOS, Linux, WSL, Git Bash**

```sh
curl -fsSL https://raw.githubusercontent.com/macdipu/gitswitch/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/macdipu/gitswitch/main/install.ps1 | iex
```

Run the same line again to update. To pin a version, set `GITSWITCH_VERSION=v1.2.0` first.

<details>
<summary>Manual install</summary>

Download `gitswitch-<os>-<cpu>` for your computer from the [Releases page](https://github.com/macdipu/gitswitch/releases) and run it with `install`. On Windows you can simply double-click it.

</details>

Then open a **new** terminal.

## Quick start

Answer a few questions:

```sh
gitswitch setup
```

Or script it, one command per account:

```sh
gitswitch account add work --email you@company.com --name "Your Name" --key new --default
gitswitch account add personal --email you@gmail.com --key new
gitswitch key show work --open        # copies the public key, opens GitHub's SSH key page
gitswitch test                        # checks each key logs in, and as whom
gitswitch rule add owner:my-company work
gitswitch rule add folder:~/personal personal
```

Add each public key on GitHub (**Settings → SSH and GPG keys → New SSH key**) while signed in to the matching account.

From then on, clone and push as usual:

```sh
git clone git@github.com:owner/repo.git
```

## How an account is chosen

For each repo, GitSwitch checks in this order and uses the first match:

1. The account saved for the repo (`gitswitch use work`).
2. An **owner rule**: `owner:my-company` matches every repo of that org, even during `git clone`.
3. A **folder rule**: `folder:~/work`.
4. In a terminal, it **asks**. Type `2` for this time only, or `2!` to save it for the repo.
5. With no terminal (e.g. an IDE's Source Control panel), a **small popup** asks.
6. Otherwise, the **default account**. Nothing ever hangs waiting for an answer.

It also sets the repo's `user.name` and `user.email` to the chosen account.

## Terminal app and browser app

Both edit the same settings. A change in one shows up in the other.

| | Terminal app | Browser app |
| --- | --- | --- |
| Open | `gitswitch` (or `gitswitch tui`) | `gitswitch ui`, or GitSwitch in the Start / app menu |
| Best for | IDE terminals, SSH sessions, WSL, servers | Mouse users, the visual history graph |
| Pages | Accounts, Repos, History, Rules, Settings | Accounts, Repos, History, Rules, Settings |
| Memory | a few MB | ~8 MB plus the browser tab |
| Closes | press `q` | ~30 s after you close the tab |

<details>
<summary>Terminal app keys</summary>

`1`–`5` or Tab switch pages, ↑↓ move, `?` shows all keys, `q` quits.

| Page | Keys |
| --- | --- |
| Accounts | `a` add · `e` edit · `t` test login · `k` public key (`c` copy, `o` open GitHub page) · `d` make default · `p` load passphrase into agent · `x` remove |
| Repos | Enter choose account · `h` history · `f` add projects folder · `r` rescan |
| History | `o` other repo · `f` only wrong-account commits · `x` fix not-pushed commits · `r` reload |
| Rules | `a` add · `x` remove |
| Settings | Enter toggles an option or fixes a failed check |

When the terminal app creates a key or tests a login, it steps aside so `ssh-keygen` and `ssh` can ask for your passphrase, then comes back.

</details>

## IDE notes

- **VS Code:** nothing to set. The integrated terminal prompts; the Source Control panel uses rules, the popup or the default.
- **Android Studio / IntelliJ / other JetBrains IDEs:** if your version has **Settings → Version Control → Git → SSH executable**, choose **Native**. The Built-in option ignores git's settings.
- **Key passphrases:** IDEs can't type a passphrase for you. Run `gitswitch key agent <account>` once in a terminal to store it in the OS agent or macOS Keychain.
- **Windows:** the Windows `ssh-agent` service remembers passphrases. `gitswitch doctor` tells you if it is off and how to turn it on (one admin command).
- **WSL:** WSL is a separate Linux system with its own git. Install the Linux binary inside WSL too.

## Commit history

- `gitswitch log` shows the commit graph with the account of each commit.
- `gitswitch log --flagged` lists commits made with another of your accounts.
- `gitswitch fix-author` corrects those that are not pushed yet, on the current branch. Pushed commits are never changed.

The apps' History page shows the same graph.

## Health check

`gitswitch doctor` finds anything that can make git skip GitSwitch or log in as the wrong account; `gitswitch doctor --fix` repairs what it can:

- `GIT_SSH_COMMAND` or `GIT_SSH` set in your environment or shell profile (git then ignores GitSwitch)
- repos with their own `core.sshCommand` (left by other tools)
- `~/.ssh/config` blocks that add other keys for your git hosts
- host keys not yet recorded (a first push from an IDE would fail)
- Windows ssh-agent service off, missing key files, a global commit email

## Command reference

Run `gitswitch help` for details. Add `--dry-run` to any command to see what it would change without changing anything.

| Area | Commands |
| --- | --- |
| Setup | `install`, `setup`, `uninstall [--purge]` |
| Apps | `tui`, `ui` |
| Accounts | `account add\|edit\|list\|remove [--delete-keys]\|rename`, `default`, `test` |
| Keys | `key new\|show\|pick\|agent` |
| Repos | `use`, `forget`, `status` |
| Rules | `rule add\|list\|remove` |
| History | `log`, `fix-author` |
| Diagnostics | `doctor [--fix]` |
| Settings | `set gui-prompt on\|off`, `set prompt-timeout <s>` |
| Shell | `completions bash\|zsh\|fish\|powershell` |

### Shell completions

Completions include your account names.

```sh
eval "$(gitswitch completions bash)"                                   # ~/.bashrc
eval "$(gitswitch completions zsh)"                                    # ~/.zshrc
gitswitch completions fish > ~/.config/fish/completions/gitswitch.fish
```

```powershell
gitswitch completions powershell | Out-String | Invoke-Expression      # $PROFILE
```

## How it works

`gitswitch install` does this once:

- Copies itself to a fixed folder (`%LOCALAPPDATA%\GitSwitch` on Windows, `~/.local/bin` on macOS and Linux) and adds it to your PATH.
- Sets git's global `core.sshCommand` to GitSwitch. Every program that runs git reads this setting, which is why it works in every terminal and IDE without per-app setup.
- Sets `user.useConfigOnly = true` and moves your global `user.email` aside, so a commit can never silently use the wrong email.

All settings live in git config, so the terminal, the browser app and git always agree:

| Scope | File | Keys |
| --- | --- | --- |
| Global | `~/.gitconfig` | `gitswitch-account.<id>.*`, `gitswitch.default`, `gitswitch.rule`, `gitswitch.guiPrompt`, `gitswitch.promptTimeout`, `gitswitch.scanFolder` |
| Per repo | `.git/config` | `gitswitch.account` |

**Security:** GitSwitch never stores passphrases or tokens. The browser app listens only on `127.0.0.1` with a random one-time token.

See [ARCHITECTURE.md](ARCHITECTURE.md) for how the code is organised.

## Uninstall

```sh
gitswitch uninstall           # restores your previous git settings
gitswitch uninstall --purge   # also deletes your saved accounts
```

## Development

Requires Go 1.22 or newer. No other dependencies.

```sh
go build -o gitswitch ./cmd/gitswitch
go test ./...
./build.sh v1.0.0             # all platforms + SHA256SUMS into dist/
```

The tests build the real binary and run real `git clone`, `pull` and `push` through it against local repos (no network). They cover install and uninstall, every way an account is chosen, clones inside other repos, submodules, port-443 hosts, renames, duplicate keys, dry runs, health checks, history and author fixes, the browser app's security, and the terminal prompt. [GitHub Actions](https://github.com/macdipu/gitswitch/actions) runs them on Linux, macOS and Windows for every push.

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).
