# Run internal unit and equivalence tests
test:
    go test ./internal/bait ./cmd/bait

# Run e2e sandbox tests
e2e:
    go test ./e2e

# Format Go and Fish source files
fmt:
    go fmt ./...
    fd --extension fish --exec-batch fish_indent --write
