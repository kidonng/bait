#!/usr/bin/env fish

if test (count $argv) -eq 0
    echo "==> Running internal unit and equivalence tests..."
    go test -v ./internal/bait ./cmd/bait; or exit
    echo "==> Running e2e sandbox tests..."
    go test -v ./e2e; or exit
else
    go test -v $argv; or exit
end
