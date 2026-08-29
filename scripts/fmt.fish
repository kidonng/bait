#!/usr/bin/env fish

echo "==> Formatting Go files..."
go fmt ./...; or exit

echo "==> Formatting Fish files..."
fd --extension fish --exec-batch fish_indent --write
