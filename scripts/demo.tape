# The demo at the top of the README and on the home page, recorded with VHS
# (https://github.com/charmbracelet/vhs). From the repository root:
#
#   go build -o bin/packlab ./examples/packlab
#   ./bin/packlab -product ticketing &          # a fresh TicketLab on :8094
#   mkdir -p /tmp/stampede-demo && cd /tmp/stampede-demo
#   vhs "$OLDPWD/scripts/demo.tape"
#   cp demo.gif demo.mp4 "$OLDPWD/site/public/screenshots/"
#
# Restart packlab before each take: earlier runs sell its seats, and later
# orders are then refused.

Output demo.gif
Output demo.mp4

Set Shell bash
Set FontSize 15
Set Width 1280
Set Height 940
Set Padding 28
Set TypingSpeed 40ms
Set PlaybackSpeed 1.5
Set WindowBar Colorful
Set BorderRadius 10
Set Theme { "name": "stampede", "background": "#18181B", "foreground": "#E4E4E7", "cursor": "#14B8A6", "selection": "#3F3F46", "black": "#18181B", "red": "#F87171", "green": "#34D399", "yellow": "#FBBF24", "blue": "#60A5FA", "magenta": "#C084FC", "cyan": "#14B8A6", "white": "#E4E4E7", "brightBlack": "#71717A", "brightRed": "#FCA5A5", "brightGreen": "#6EE7B7", "brightYellow": "#FDE68A", "brightBlue": "#93C5FD", "brightMagenta": "#D8B4FE", "brightCyan": "#5EEAD4", "brightWhite": "#FAFAFA" }

Hide
Type "rm -rf stampede && export PS1='\[\e[38;2;20;184;166m\]$\[\e[0m\] ' && clear"
Enter
Show

Sleep 600ms
Type "# point Stampede at your API: it works out what kind of app it is"
Sleep 400ms
Enter
Type "stampede init --target http://localhost:8094"
Sleep 400ms
Enter
Sleep 1.5s
Type "y"
Sleep 300ms
Enter
Wait+Screen@90s /Every journey/
Sleep 4s

Hide
Type "clear"
Enter
Show
Sleep 400ms
Type "# then a ticket on-sale rush: 10 to 300 requests a second and back"
Sleep 400ms
Enter
Type "stampede run stampede/ticketing/stresses/on-sale-rush.yaml -e TARGET_URL=http://localhost:8094 --duration 15s"
Sleep 400ms
Enter
Wait+Screen@90s /slowest requests/
Sleep 7s
