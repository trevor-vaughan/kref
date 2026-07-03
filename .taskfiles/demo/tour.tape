# kref — the tour. Rendered by `task dev:demo`; see .taskfiles/scripts/demo.sh.
# The Output path here is a placeholder — the Taskfile overrides it with `vhs -o`.
# The sandbox is pre-seeded with a committed docs/auth-flow.md (a rich spec) so
# the capture step can pull a real body in with $(< file) instead of typing it.
Output "tour.gif"

Require kref

Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 20
Set Width 1200
Set Height 640
Set Padding 20
Set WindowBar Colorful
Set TypingSpeed 34ms

# --- sandbox setup (hidden): land in the demo repo with a clean prompt --------
# VHS sets NO_COLOR in its session: KREF_COLOR=1 overrides it for kref itself,
# and unsetting NO_COLOR lets the pager chrome (lipgloss/termenv) style too.
# The prompt glyph › is spelled in octal (\342\200\272) because VHS's Type
# drops non-ASCII characters — bash prompt expansion emits it (the bundled
# font lacks the usual ❯ U+276F, which renders blank).
Hide
Type `export KREF_COLOR=1 PS1='\[\e[38;5;170m\]\342\200\272\[\e[0m\] '; unset NO_COLOR` Enter
Type `cd "$KREF_DEMO_DIR" && clear` Enter
Show

# --- scene 1: init and capture -------------------------------------------------
Type "# kref keeps specs, ADRs and notes in git refs — off your file tree" Enter
Sleep 1s
Type "kref init" Enter
Sleep 2s
Enter
Type "# capture knowledge — typed, kinded, and tiered" Enter
Sleep 600ms
Type `kref new --kind adr --title "Use Postgres" --label area:db --body "Chosen over SQLite for concurrent writers."` Enter
Sleep 1.2s
Type `kref new --kind memory --tier private --title "prod DB host" --body "db.internal:5432 via the bastion"` Enter
Sleep 1.2s
Type `kref new --kind spec --tier shared --label area:auth --body "$(< docs/auth-flow.md)"` Enter
Sleep 2s

# --- scene 2: the tier view, then the pager ------------------------------------
Hide
Type "clear" Enter
Show
Type "# one colour-coded view across every tier" Enter
Sleep 600ms
Type "kref list --no-pager" Enter
Sleep 3.5s
Enter
Type "# open one in the pager — rendered markdown, tables, highlighted code" Enter
Sleep 800ms
Type `kref show $(kref list --label area:auth --plain --columns=id)` Enter
Sleep 3.5s
Ctrl+D
Sleep 2.5s
Ctrl+D
Sleep 3s
Type "q"
Sleep 1s

# --- scene 3: the closer — a clean working tree --------------------------------
Hide
Type "clear" Enter
Show
Type "# and your working tree? untouched." Enter
Sleep 800ms
Type "git status --short --branch" Enter
Sleep 3.5s
