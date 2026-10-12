# The terminal app for the README (docs/images/tui.gif). Run by gifs.sh
# after the demo home folder is made.
Output tui.gif
Set Shell "bash"
Set FontSize 16
Set Width 1440
Set Height 640
Set Padding 20
Set Theme "Catppuccin Mocha"

Hide
Type "exec su - alex" Enter
Sleep 1s
Type "export PS1='$ '; cd ~/code/acme-api; clear" Enter
Sleep 1s
Show

Type "gitswitch" Sleep 300ms Enter
Sleep 3s
Type "2" Sleep 2s
Down Sleep 700ms Down Sleep 700ms Down Sleep 1.5s
Type "3" Sleep 2.5s
Down Sleep 1s
Enter Sleep 4s
Escape Sleep 1s
Type "4" Sleep 2s
Type "5" Sleep 3.5s
Type "1" Sleep 2.5s
Type "q" Sleep 500ms
