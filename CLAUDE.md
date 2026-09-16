# lcli

Go CLI for reading and commenting on Linear issues across several Linear accounts.

## Commands
- Build: `go build ./...` (binary: `go build -o lcli .`)
- Test: `go test ./...`
- Vet: `go vet ./...`
- Regenerate GraphQL client after editing `internal/linear/queries.graphql`
  or updating `internal/linear/schema.graphql`: `go generate ./...`
  (generated code is committed; CI-style check: regenerate and `git diff --exit-code`)

## Layout
- `internal/linear` — genqlient operations (`queries.graphql`), generated client, auth + error helpers
- `internal/config` — `~/.config/lcli/config.toml` accounts, team-key routing, API key retrieval
- `internal/ident` — issue identifier / linear.app URL parsing
- `internal/cli` — cobra commands; tests use a fake Linear server (`fake_test.go`)
