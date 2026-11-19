package cli

import (
	"fmt"
	"io"
)

const commandList = "install uninstall setup tui ui account key test signin signout github prs default use forget rule repo status log blame fix-author changes diff hunks stage unstage discard ignore commit branch tag switch checkout back remote fetch update revert drop rollback cherry-pick undo redo init resolve merge rebase reword fixup-into compare reflog bisect continue skip abort conflict parked stash worktree submodule push show browse doctor set completions version help"

// printCompletions prints a shell completion script. Account names are
// looked up live with `gitswitch __accounts`.
func printCompletions(w io.Writer, shell string) error {
	switch shell {
	case "bash":
		fmt.Fprint(w, `# gitswitch bash completion. Add to ~/.bashrc:  eval "$(gitswitch completions bash)"
_gitswitch() {
  local cur prev words cword
  cur="${COMP_WORDS[COMP_CWORD]}"; prev="${COMP_WORDS[COMP_CWORD-1]}"
  local cmds="`+commandList+`"
  if [ "$COMP_CWORD" -eq 1 ]; then COMPREPLY=( $(compgen -W "$cmds" -- "$cur") ); return; fi
  case "${COMP_WORDS[1]}" in
    account) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "add edit list remove rename" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --email --name --host --key --signing-key --signing-format --default --delete-keys -y" -- "$cur") ) ;;
    key) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "new show pick agent" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --open --copy" -- "$cur") ) ;;
    rule) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "add list remove" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) owner: folder:" -- "$cur") ) ;;
    repo) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "list clone add remove show open pin unpin recent group ungroup" -- "$cur") ) || COMPREPLY=( $(compgen -d -- "$cur") $(compgen -W "--json --pinned --by --sort --editor --account" -- "$cur") ) ;;
    set) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "gui-prompt prompt-timeout ssh-path editor fetch-on-open branch-prefix repo-groups repo-sort pull fetch-prune switch-changes signoff" -- "$cur") )
      elif [ "${COMP_WORDS[2]}" = switch-changes ]; then COMPREPLY=( $(compgen -W "ask smart park global --repo" -- "$cur") )
      else COMPREPLY=( $(compgen -W "on off" -- "$cur") ); fi ;;
    completions) COMPREPLY=( $(compgen -W "bash zsh fish powershell" -- "$cur") ) ;;
    use|default|test|signin|signout) COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --once" -- "$cur") ) ;;
    log) COMPREPLY=( $(compgen -W "--flagged -n --author --grep --since --until --account --code --regex --ignore-case --file --json" -- "$cur") ) ;;
    blame) COMPREPLY=( $(compgen -W "--rev -w -M --json" -- "$cur") $(compgen -f -- "$cur") ) ;;
    branch) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "new prune compare pr delete-remote --json" -- "$cur") )
      elif [ "$prev" = --from ]; then COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null)" -- "$cur") )
      else COMPREPLY=( $(compgen -W "--from --move --no-prefix" -- "$cur") ); fi ;;
    switch) COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --smart --park" -- "$cur") ) ;;
    compare) COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --since-fork --direct --working --file --json" -- "$cur") ) ;;
    reflog) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "recover -n --skip --branch --lost --json" -- "$cur") ); else COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --branch" -- "$cur") ); fi ;;
    bisect) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "start good bad skip status reset" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --json" -- "$cur") ) ;;
    resolve) COMPREPLY=( $(compgen -W "--done --abort --park -y" -- "$cur") ) ;;
    parked) COMPREPLY=( $(compgen -W "show restore drop --json -y" -- "$cur") ) ;;
    drop|rollback) COMPREPLY=( $(compgen -W "-y" -- "$cur") ) ;;
    push) COMPREPLY=( $(compgen -W "--open --force-with-lease -y" -- "$cur") ) ;;
    fetch) COMPREPLY=( $(compgen -W "--prune --json" -- "$cur") ) ;;
    checkout) COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --smart --park --force" -- "$cur") ) ;;
    back) COMPREPLY=( $(compgen -W "--smart --park --force" -- "$cur") ) ;;
    remote) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "list add remove rename set-url --json" -- "$cur") ); else COMPREPLY=( $(compgen -W "--force -y" -- "$cur") ); fi ;;
    commit) COMPREPLY=( $(compgen -W "-m --all --amend --signoff --no-signoff --co-author --recent --json" -- "$cur") ) ;;
    undo|redo) COMPREPLY=( $(compgen -W "-y --show --json" -- "$cur") ) ;;
    init) COMPREPLY=( $(compgen -d -- "$cur") $(compgen -W "--account --branch -b --json" -- "$cur") ) ;;
    doctor) COMPREPLY=( $(compgen -W "--fix" -- "$cur") ) ;;
    uninstall) COMPREPLY=( $(compgen -W "--purge" -- "$cur") ) ;;
  esac
}
complete -F _gitswitch gitswitch
`)
	case "zsh":
		fmt.Fprint(w, `#compdef gitswitch
# gitswitch zsh completion. Add to ~/.zshrc:  eval "$(gitswitch completions zsh)"
_gitswitch() {
  local -a cmds accts
  cmds=(`+commandList+`)
  accts=(${(f)"$(gitswitch __accounts 2>/dev/null)"})
  if (( CURRENT == 2 )); then compadd -a cmds; return; fi
  case "$words[2]" in
    account) (( CURRENT == 3 )) && compadd add edit list remove rename || compadd -a accts -- --email --name --host --key --signing-key --signing-format --default --delete-keys -y ;;
    key) (( CURRENT == 3 )) && compadd new show pick agent || compadd -a accts -- --open --copy ;;
    rule) (( CURRENT == 3 )) && compadd add list remove || compadd -a accts -- owner: folder: ;;
    repo) (( CURRENT == 3 )) && compadd list clone add remove show open pin unpin recent group ungroup || { _files -/; compadd -- --json --pinned --by --sort --editor --account; } ;;
    set) if (( CURRENT == 3 )); then compadd gui-prompt prompt-timeout ssh-path editor fetch-on-open branch-prefix repo-groups repo-sort pull fetch-prune switch-changes signoff
      elif [[ ${words[3]} == switch-changes ]]; then compadd ask smart park global --repo
      else compadd on off; fi ;;
    completions) compadd bash zsh fish powershell ;;
    use|default|test|signin|signout) compadd -a accts -- --once ;;
    log) compadd -- --flagged -n --author --grep --since --until --account --code --regex --ignore-case --file --json ;;
    blame) compadd -- --rev -w -M --json ;;
    branch) if (( CURRENT == 3 )); then compadd -- new prune compare pr delete-remote --json
      elif [[ ${words[CURRENT-1]} == --from ]]; then compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"}
      else compadd -- --from --move --no-prefix; fi ;;
    switch) compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --smart --park ;;
    compare) compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --since-fork --direct --working --file --json ;;
    reflog) if (( CURRENT == 3 )); then compadd recover -n --skip --branch --lost --json; else compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --branch; fi ;;
    bisect) if (( CURRENT == 3 )); then compadd start good bad skip status reset; else compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --json; fi ;;
    resolve) compadd -- --done --abort --park -y ;;
    parked) compadd -- show restore drop --json -y ;;
    drop|rollback) compadd -- -y ;;
    push) compadd -- --open --force-with-lease -y ;;
    fetch) compadd -- --prune --json ;;
    checkout) compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --smart --park --force ;;
    back) compadd -- --smart --park --force ;;
    remote) if (( CURRENT == 3 )); then compadd -- list add remove rename set-url --json; else compadd -- --force -y; fi ;;
    commit) compadd -- -m --all --amend --signoff --no-signoff --co-author --recent --json ;;
    undo|redo) compadd -- -y --show --json ;;
    init) _files -/; compadd -- --account --branch -b --json ;;
    doctor) compadd -- --fix ;;
  esac
}
if [[ $zsh_eval_context[-1] == loadautofunc ]]; then _gitswitch "$@"; else compdef _gitswitch gitswitch; fi
`)
	case "fish":
		fmt.Fprint(w, `# gitswitch fish completion. Save as ~/.config/fish/completions/gitswitch.fish
#   gitswitch completions fish > ~/.config/fish/completions/gitswitch.fish
complete -c gitswitch -f
complete -c gitswitch -n __fish_use_subcommand -a "`+commandList+`"
complete -c gitswitch -n "__fish_seen_subcommand_from account" -a "add edit list remove rename (gitswitch __accounts)"
complete -c gitswitch -n "__fish_seen_subcommand_from key" -a "new show pick agent (gitswitch __accounts)"
complete -c gitswitch -n "__fish_seen_subcommand_from rule" -a "add list remove (gitswitch __accounts)"
complete -c gitswitch -n "__fish_seen_subcommand_from use default test signin signout" -a "(gitswitch __accounts)"
complete -c gitswitch -n "__fish_seen_subcommand_from repo" -a "list clone add remove show open pin unpin recent group ungroup (__fish_complete_directories)"
complete -c gitswitch -n "__fish_seen_subcommand_from set" -a "gui-prompt prompt-timeout ssh-path editor fetch-on-open branch-prefix repo-groups repo-sort pull fetch-prune switch-changes signoff on off"
complete -c gitswitch -n "__fish_seen_subcommand_from branch" -a "new prune compare pr delete-remote --json --from --move --no-prefix -y"
complete -c gitswitch -n "__fish_seen_subcommand_from push" -a "--open --force-with-lease -y"
complete -c gitswitch -n "__fish_seen_subcommand_from fetch" -a "--prune --json"
complete -c gitswitch -n "__fish_seen_subcommand_from checkout" -a "(gitswitch __branches) --smart --park --force"
complete -c gitswitch -n "__fish_seen_subcommand_from back" -a "--smart --park --force"
complete -c gitswitch -n "__fish_seen_subcommand_from remote" -a "list add remove rename set-url --json --force -y"
complete -c gitswitch -n "__fish_seen_subcommand_from commit" -a "-m --all --amend --signoff --no-signoff --co-author --recent --json"
complete -c gitswitch -n "__fish_seen_subcommand_from switch-changes" -a "ask smart park global --repo"
complete -c gitswitch -n "__fish_seen_subcommand_from parked" -a "show restore drop --json -y"
complete -c gitswitch -n "__fish_seen_subcommand_from resolve" -a "--done --abort --park -y"
complete -c gitswitch -n "__fish_seen_subcommand_from completions" -a "bash zsh fish powershell"
complete -c gitswitch -n "__fish_seen_subcommand_from switch" -a "(gitswitch __branches) --smart --park"
complete -c gitswitch -n "__fish_seen_subcommand_from compare" -a "(gitswitch __branches) --since-fork --direct --working --file --json"
complete -c gitswitch -n "__fish_seen_subcommand_from reflog" -a "recover -n --skip --branch --lost --json"
complete -c gitswitch -n "__fish_seen_subcommand_from bisect" -a "start good bad skip status reset (gitswitch __branches) --json"
complete -c gitswitch -l dry-run -d "show changes without making them"
`)
	case "powershell", "pwsh":
		fmt.Fprint(w, `# gitswitch PowerShell completion. Add to your $PROFILE:
#   gitswitch completions powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName gitswitch, gitswitch.exe -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
  $subs = @{
    account = 'add edit list remove rename'; github = 'list clone --refresh --json'; key = 'new show pick agent'; rule = 'add list remove'
    repo = 'list clone add remove show open pin unpin recent group ungroup'; set = 'gui-prompt prompt-timeout ssh-path editor fetch-on-open branch-prefix repo-groups repo-sort pull fetch-prune switch-changes signoff'; completions = 'bash zsh fish powershell'
    parked = 'show restore drop --json'; resolve = '--done --abort --park'; branch = 'new prune compare pr delete-remote --json'; push = '--open --force-with-lease -y'; fetch = '--prune --json'; back = '--smart --park --force'; remote = 'list add remove rename set-url --json --force -y'; commit = '-m --all --amend --signoff --no-signoff --co-author --recent --json'
  }
  if ($words.Count -le 1 -or ($words.Count -eq 2 -and $wordToComplete)) {
    $list = '`+commandList+`' -split ' '
  } elseif ($words.Count -le 2 -or ($words.Count -eq 3 -and $wordToComplete -and $subs.ContainsKey($words[1]))) {
    $list = if ($subs.ContainsKey($words[1])) { $subs[$words[1]] -split ' ' } elseif ($words[1] -eq 'switch') { @(gitswitch __branches) } else { @(gitswitch __accounts) }
  } elseif ($words[2] -eq 'switch-changes') {
    $list = 'ask smart park global --repo' -split ' '
  } else {
    $list = @(gitswitch __accounts) + @('--dry-run')
  }
  $list | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`)
	default:
		return fmt.Errorf("unknown shell %q (bash, zsh, fish, powershell)", shell)
	}
	return nil
}
