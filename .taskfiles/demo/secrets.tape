# kref — secret-aware ingest. Rendered by `task dev:demo`; see demo.sh.
# The sandbox is pre-seeded (kref init + a docs/ tree, one file carrying a
# freshly generated token) so the recording opens straight on the ingest.
# The Output path is a placeholder — the Taskfile overrides it with `vhs -o`.
Output "secrets.gif"

Require kref

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 20
Set Width 1200
Set Height 720
Set Padding 20
Set WindowBar Colorful
Set TypingSpeed 45ms

# --- sandbox setup (hidden): land in the demo repo with a clean prompt --------
# VHS sets NO_COLOR in its session: KREF_COLOR=1 overrides it for kref itself,
# and unsetting NO_COLOR lets any lipgloss/termenv chrome style too. The prompt
# glyph › is spelled in octal (\342\200\272) because VHS's Type drops non-ASCII
# characters — bash prompt expansion emits it (the bundled font lacks the usual
# ❯ U+276F, which renders blank).
Hide
Type `export KREF_COLOR=1 PS1='\[\e[38;5;170m\]\342\200\272\[\e[0m\] '; unset NO_COLOR` Enter
Type `cd "$KREF_DEMO_DIR" && clear` Enter
Show

# --- scene 1: point kref at markdown you already have --------------------------
Type "# ingest markdown you already have — it is secret-scanned on the way in" Enter
Sleep 1s
Enter
Type "ls docs/" Enter
Sleep 2s
Enter
Type "kref ingest docs/" Enter
Sleep 7s

# --- scene 2: the leaked token never reaches a syncable tier -------------------
Hide
Type "clear" Enter
Show
Type "# betterleaks caught a token and quarantined that file to private" Enter
Sleep 1.2s
Enter
Type "kref list --no-pager" Enter
Sleep 3.5s
Enter
Type "# the private tier has no remote — that secret can never be pushed" Enter
Sleep 1.2s
Enter
Type "kref list --tier private --no-pager" Enter
Sleep 4s
