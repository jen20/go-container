# Changelog

This file lists the changes in each release of `go-container`. A release
tags both Go modules with the same version: `v<version>` for
`github.com/jen20/go-container`, and `systemconfig/v<version>` for
`github.com/jen20/go-container/systemconfig`.

## Unreleased

Initial pre-release, tested against Apple container 1.7.1, and it runs
on macOS on Apple Silicon only.

### New Features

#### Core Module

- Added a Go client for the Apple container API server. It uses XPC directly,
  in pure Go, with no dependencies. The following features are supported:
    - Containers: create, start, stop, kill and delete containers, read their
      logs and statistics, export them, and copy files in and out.
    - Processes: run more processes in a container, send signals, wait for
      exit, and resize the terminal.
    - Images: list, pull, push, tag, delete, save, load and unpack images.
    - Networks, volumes, kernels, the content store, and disk usage.

#### System Config Module

- Added a `systemconfig` module, which reads the same TOML configuration files
  as the `container` command line tool.
