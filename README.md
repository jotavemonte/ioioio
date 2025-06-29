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
go build -o ioioio src/main.go
```

## Usage

Run the application:

```bash
./ioioio
```

For details on how to use it, press `?` with the app running to see the legend and the available shortcuts and navigation tips.

## License

This project is licensed under the terms specified in the LICENSE file.
