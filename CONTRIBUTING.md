# Contributing to Panal

Thanks for your interest! Issues and pull requests are welcome. Read
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for how the code fits
together; [AGENTS.md](AGENTS.md) has the same rules in a form AI coding
agents follow, plus a "where to change X" table.

## Build and test

You need Go 1.27 or newer.

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .               # must print nothing
go install ./cmd/panal   # puts panal in ~/go/bin
```

To look at the UI without running it interactively:

```sh
go run ./cmd/panal -preview 80                  # the dashboard at 80 columns
go run ./cmd/panal -preview 132 -screen history # or table, detail, history-detail, report, timeline, help
go run ./cmd/panal -mascots                     # the mascots
go run ./cmd/preview docs/animations.png        # every mascot frame as a PNG
```

## Golden screens

The screens are checked against golden files in
`internal/ui/testdata/screens/` at 80, 100, 132 and 160 columns. If you
change the layout on purpose, regenerate them and review the diff before
committing:

```sh
go test ./internal/ui -run TestScreens -update
git diff internal/ui/testdata
```

Before touching `internal/ui` or `internal/mascots`, read the design guide
in [.claude/skills/panal-ux/SKILL.md](.claude/skills/panal-ux/SKILL.md).

## Ground rules

- **English everywhere**: code, identifiers, comments, UI text, commit
  messages and docs. The only Spanish left on purpose:
  `internal/runs/legacy.go` (reads run files of the older delegar.sh
  helper), the patterns that match tasks written in Spanish (each one says
  so), and real captured tool output in `testdata/`.
- **Read-only**: Panal never writes to the agents' files. Its only writes
  are its own data in `~/.panal` (see
  [docs/configuration.md](docs/configuration.md#files-in-panal)).
- **No real agents in tests**: `internal/delegate` tests run the test
  binary itself as a fake codex, agy and opencode, `internal/router` tests
  run it as a fake external router, and `internal/models` tests answer the
  listing commands with real outputs in `testdata/`. Reader tests use real
  samples; please don't invent formats. Never spend quota in a test.
- **The router stays explainable**: tier rules live in one table
  (`internal/router/tier.go`), each with a name `panal route` prints; a new
  rule needs a test in English and in Spanish. Thompson sampling is random,
  so tests seed it (`math/rand/v2` PCG) and learning tests look at several
  seeded histories.
- **Small, focused pull requests**: one change per PR, with tests, and
  screen diffs reviewed when the UI changes. Update the guide in `docs/`
  that describes what you changed.
- **No personal data**: never commit personal paths, email addresses or
  tokens (tests and samples included).
- **No AI attribution** in commit messages (no `Co-Authored-By` lines for
  tools).

## License

Panal is licensed under the [Apache License 2.0](LICENSE). By sending a pull
request you agree that your contribution is licensed under the same terms
(section 5 of the license); no separate agreement is needed.
