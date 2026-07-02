# ioioio - Docker Container Monitor

ioioio is a lightweight terminal-based Docker container monitoring tool that provides a simple, intuitive interface for managing and monitoring Docker containers. It allows you to view container status, logs, and perform basic container operations all from a single terminal window.

The project is heavily influenced by [lazydocker](https://github.com/jesseduffield/lazydocker) - it's not a fork.

## 🚀 Elevator Pitch

Imagine you're a fullstack developer working in a microservices-heavy environment. To run end-to-end tests locally, you need to spin up 7+ separate Docker Compose projects—each living in its own directory, each requiring its own terminal session, and each producing logs in its own scattered tab.

Monitoring container statuses? That means jumping into each folder to run docker compose ps. Tail logs? Same story. It's a fragmented, repetitive, and distracting experience.

ioioio changes that.
It's a lightweight, centralized interface that lets you:

- View the status of all your running containers in one place.
- Easily access logs for any service.
- Jump between projects and services without switching tabs or terminals.

With ioioio, you can stay focused on what matters—developing and debugging, not wrangling sessions.

## Features

- **Real-time container monitoring**: View the status of all your Docker containers with automatic updates
- **Container grouping**: Containers are organized by project (based on Docker Compose labels) for better organization
- **Live log streaming**: Watch container logs in real-time
- **Container management**: Restart, stop, and start containers directly from the interface
- **Visual status indicators**: Colorful status indicators (💚🛑🟨🟣🔷) show container states at a glance
- **Display only containers for the projects that matter**: Filter projects using the command line `ioioio <project-1> <project-2>`

## Installation

### Prerequisites

- Go 1.18 or higher
- Docker installed and running on your system

### Building from source

```bash
git clone https://github.com/yourusername/ioioio.git
cd ioioio
go build -o ioioio ./cmd/ioioio
```

## Usage

Run the application:

```bash
./ioioio
```

For details on how to use it, press `?` with the app running to see the legend and the available shortcuts and navigation tips.

## macOS app

A native macOS interface built with [DarwInKit](https://github.com/progrium/darwinkit) is available alongside the terminal UI. It offers the same features — a source-list sidebar of projects and containers, live log streaming, container config, and Start/Stop/Restart actions — in an idiomatic Mac window.

### Prerequisites

- macOS with the Xcode Command Line Tools (`xcode-select --install`) — DarwInKit links against AppKit via cgo.
- Docker installed and running.

### Building and running

```bash
go build -o ioioio-mac ./cmd/ioioio-mac
./ioioio-mac
```

Pass project names as arguments to filter the sidebar, just like the terminal UI:

```bash
./ioioio-mac project-a project-b
```

The two front-ends share their Docker logic in `internal/core`, so both stay in sync.

### Installing via .dmg

Instead of running the raw binary, you can build a `.app` bundle wrapped in a disk image and install it into `/Applications`.

**1. Build the DMG:**

```bash
packaging/build-dmg.sh 0.1.0
```

This produces two artifacts under `dist/`:

- `dist/ioioio.app` — an ad-hoc-signed app bundle
- `dist/ioioio-0.1.0.dmg` — the distributable disk image

To give the app an icon, drop a 1024×1024 `packaging/icon.png` in place before running the script.

**2. Install from the DMG:**

1. Open the disk image: `open dist/ioioio-0.1.0.dmg` (or double-click it in Finder).
2. In the window that appears, **drag `ioioio` onto the `Applications` shortcut**. The DMG only provides that shortcut — it does not copy the app automatically, so this drag is the actual install step.
3. Eject the disk image (⌘E in Finder, or `hdiutil detach /Volumes/ioioio`).
4. Launch **ioioio** from `/Applications`, Spotlight, or Launchpad.

> **First launch:** because the app is ad-hoc signed (no Apple Developer ID), macOS Gatekeeper may block it the first time. If it won't open, right-click the app → **Open** → **Open** to approve it once.

Prefer to skip the DMG entirely? Copy the bundle straight in:

```bash
packaging/build-dmg.sh 0.1.0
cp -R dist/ioioio.app /Applications/
```

## License

This project is licensed under the terms specified in the LICENSE file.
