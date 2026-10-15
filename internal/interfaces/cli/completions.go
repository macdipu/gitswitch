package cli

import (
	"fmt"
	"io"
)

const commandList = "install uninstall setup tui ui account key test signin signout github prs default use forget rule repo status log fix-author commit branch switch update revert drop rollback cherry-pick undo resolve parked push show browse doctor set completions version help"

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
    account) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "add edit list remove rename" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --email --name --host --key --default --delete-keys -y" -- "$cur") ) ;;
    key) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "new show pick agent" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --open --copy" -- "$cur") ) ;;
    rule) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "add list remove" -- "$cur") ) || COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) owner: folder:" -- "$cur") ) ;;
    repo) [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "list clone add remove show open pin unpin recent group ungroup" -- "$cur") ) || COMPREPLY=( $(compgen -d -- "$cur") $(compgen -W "--json --pinned --by --sort --editor --account" -- "$cur") ) ;;
    set) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "gui-prompt prompt-timeout ssh-path editor branch-prefix repo-groups repo-sort pull switch-changes" -- "$cur") )
      elif [ "${COMP_WORDS[2]}" = switch-changes ]; then COMPREPLY=( $(compgen -W "ask smart park global --repo" -- "$cur") )
      else COMPREPLY=( $(compgen -W "on off" -- "$cur") ); fi ;;
    completions) COMPREPLY=( $(compgen -W "bash zsh fish powershell" -- "$cur") ) ;;
    use|default|test|signin|signout) COMPREPLY=( $(compgen -W "$(gitswitch __accounts 2>/dev/null) --once" -- "$cur") ) ;;
    log) COMPREPLY=( $(compgen -W "--flagged -n --author --grep --since --until --account" -- "$cur") ) ;;
    branch) if [ "$COMP_CWORD" -eq 2 ]; then COMPREPLY=( $(compgen -W "new prune compare --json" -- "$cur") )
      elif [ "$prev" = --from ]; then COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null)" -- "$cur") )
      else COMPREPLY=( $(compgen -W "--from --move --no-prefix" -- "$cur") ); fi ;;
    switch) COMPREPLY=( $(compgen -W "$(gitswitch __branches 2>/dev/null) --smart --park" -- "$cur") ) ;;
    resolve) COMPREPLY=( $(compgen -W "--done --abort --park -y" -- "$cur") ) ;;
    parked) COMPREPLY=( $(compgen -W "show restore drop --json -y" -- "$cur") ) ;;
    drop|rollback) COMPREPLY=( $(compgen -W "-y" -- "$cur") ) ;;
    push) COMPREPLY=( $(compgen -W "--open -y" -- "$cur") ) ;;
    commit) COMPREPLY=( $(compgen -W "-m --all" -- "$cur") ) ;;
    undo) COMPREPLY=( $(compgen -W "-y --show --json" -- "$cur") ) ;;
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
    account) (( CURRENT == 3 )) && compadd add edit list remove rename || compadd -a accts -- --email --name --host --key --default --delete-keys -y ;;
    key) (( CURRENT == 3 )) && compadd new show pick agent || compadd -a accts -- --open --copy ;;
    rule) (( CURRENT == 3 )) && compadd add list remove || compadd -a accts -- owner: folder: ;;
    repo) (( CURRENT == 3 )) && compadd list clone add remove show open pin unpin recent group ungroup || { _files -/; compadd -- --json --pinned --by --sort --editor --account; } ;;
    set) if (( CURRENT == 3 )); then compadd gui-prompt prompt-timeout ssh-path editor branch-prefix repo-groups repo-sort pull switch-changes
      elif [[ ${words[3]} == switch-changes ]]; then compadd ask smart park global --repo
      else compadd on off; fi ;;
    completions) compadd bash zsh fish powershell ;;
    use|default|test|signin|signout) compadd -a accts -- --once ;;
    log) compadd -- --flagged -n --author --grep --since --until --account ;;
    branch) if (( CURRENT == 3 )); then compadd -- new prune compare --json
      elif [[ ${words[CURRENT-1]} == --from ]]; then compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"}
      else compadd -- --from --move --no-prefix; fi ;;
    switch) compadd -- ${(f)"$(gitswitch __branches 2>/dev/null)"} --smart --park ;;
    resolve) compadd -- --done --abort --park -y ;;
    parked) compadd -- show restore drop --json -y ;;
    drop|rollback) compadd -- -y ;;
    push) compadd -- --open -y ;;
    commit) compadd -- -m --all ;;
    undo) compadd -- -y --show --json ;;
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
complete -c gitswitch -n "__fish_seen_subcommand_from set" -a "gui-prompt prompt-timeout ssh-path editor branch-prefix repo-groups repo-sort pull switch-changes on off"
complete -c gitswitch -n "__fish_seen_subcommand_from branch" -a "new prune compare --json --from --move --no-prefix -y"
complete -c gitswitch -n "__fish_seen_subcommand_from push" -a "--open -y"
complete -c gitswitch -n "__fish_seen_subcommand_from commit" -a "-m --all"
complete -c gitswitch -n "__fish_seen_subcommand_from switch-changes" -a "ask smart park global --repo"
complete -c gitswitch -n "__fish_seen_subcommand_from parked" -a "show restore drop --json -y"
complete -c gitswitch -n "__fish_seen_subcommand_from resolve" -a "--done --abort --park -y"
complete -c gitswitch -n "__fish_seen_subcommand_from completions" -a "bash zsh fish powershell"
complete -c gitswitch -n "__fish_seen_subcommand_from switch" -a "(gitswitch __branches) --smart --park"
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
    repo = 'list clone add remove show open pin unpin recent group ungroup'; set = 'gui-prompt prompt-timeout ssh-path editor branch-prefix repo-groups repo-sort pull switch-changes'; completions = 'bash zsh fish powershell'
    parked = 'show restore drop --json'; resolve = '--done --abort --park'; branch = 'new prune compare --json'; push = '--open -y'; commit = '-m --all'
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
