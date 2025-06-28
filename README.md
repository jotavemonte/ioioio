# ioioio - Docker Container Monitor

ioioio is a lightweight terminal-based Docker container monitoring tool that provides a simple, intuitive interface for managing and monitoring Docker containers. It allows you to view container status, logs, and perform basic container operations all from a single terminal window.

## Features

- **Real-time container monitoring**: View the status of all your Docker containers with automatic updates
- **Container grouping**: Containers are organized by project (based on Docker Compose labels) for better organization
- **Live log streaming**: Watch container logs in real-time
- **Container management**: Restart, stop, and start containers directly from the interface
- **Visual status indicators**: Colorful status indicators (💚🛑🟨🟣🔷) show container states at a glance

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

### Interface Navigation

The interface is split into two main panels:

1. **Left panel (Service Status)**: Displays all Docker containers grouped by project
2. **Right panel (Logs)**: Shows logs for the selected container

### Keyboard Controls

#### In the Service Status panel (left):

- Use arrow keys to navigate between containers
- `r` - Restart the selected container
- `s` - Stop the selected container
- `x` - Start the selected container
- `Enter` - View the logs of the selected container

#### In the Logs panel (right):

- `g` - Scroll to the top of the logs
- `G` - Scroll to the bottom of the logs
- `Esc` - Return focus to the Service Status panel

## Status Indicators

- 💚 - Container is running
- 🛑 - Container is stopped/exited
- 🟨 - Container is paused
- 🟣 - Container is restarting
- 🔷 - Container is created but not yet started

## License

This project is licensed under the terms specified in the LICENSE file.
