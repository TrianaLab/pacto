# The README's "Who breaks before you ship" GIF: pacto impact on auth 2.1.0 -> 3.0.0,
# run against the contracts in fixtures/impact (two auth revisions, two consumers).
# Regenerate from the repository root (needs vhs and Go): vhs docs/assets/tapes/impact.tape

Output docs/assets/gifs/impact.gif

Set Shell bash
Set Theme "Catppuccin Mocha"
Set FontSize 14
Set Width 1200
Set Height 300
Set Padding 16
Set Framerate 15
Set TypingSpeed 40ms
Set CursorBlink false

# Build this checkout's pacto into a throwaway dir and stand in the fixture folder.
Hide
Type `export PS1='$ ' PACTO_GIF_BIN="$(mktemp -d)"` Enter
Type `CGO_ENABLED=0 go build -o "$PACTO_GIF_BIN/pacto" ./cmd/pacto && export PATH="$PACTO_GIF_BIN:$PATH" && cd docs/assets/tapes/fixtures/impact && clear` Enter
Wait@180s /^\$$/
Show

Type "pacto impact ./auth-2.1.0 ./auth-3.0.0 --local ."
Sleep 500ms
Enter
Sleep 7s

Hide
Type `rm -rf "$PACTO_GIF_BIN"` Enter
