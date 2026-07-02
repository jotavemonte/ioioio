// Package core holds the UI-agnostic Docker logic shared by the terminal and
// macOS front-ends: the container model, client setup, listing/grouping, the
// start/stop/restart operations, log streaming and config inspection.
package core

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Container is a flattened view of a Docker container as the UI needs it.
type Container struct {
	ID      string
	Name    string
	Project string
	State   string
}

// Project groups containers under their Compose project name.
type Project struct {
	Name       string
	Containers []Container
}

// Client wraps the Docker client with the operations the UIs need.
type Client struct {
	docker *client.Client
}

// NewClient builds a Docker client from the environment.
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{docker: cli}, nil
}

// Close releases the underlying Docker client.
func (c *Client) Close() error { return c.docker.Close() }

// List returns all containers keyed by ID, using the Compose service/project
// labels for naming and grouping (falling back to the container name and
// "standalone" when the labels are absent).
func (c *Client) List(ctx context.Context) (map[string]Container, error) {
	fetched, err := c.docker.ContainerList(ctx, containertypes.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	out := make(map[string]Container, len(fetched))
	for _, fc := range fetched {
		name := fc.Labels["com.docker.compose.service"]
		if name == "" && len(fc.Names) > 0 {
			name = strings.TrimPrefix(fc.Names[0], "/")
		}

		project := fc.Labels["com.docker.compose.project"]
		if project == "" {
			project = "standalone"
		}

		out[fc.ID] = Container{ID: fc.ID, Name: name, Project: project, State: fc.State}
	}
	return out, nil
}

// GroupByProject turns a container map into a sorted slice of projects, each
// with its containers sorted by name. When wanted is non-empty, only projects
// whose name appears in wanted are returned.
func GroupByProject(containers map[string]Container, wanted []string) []Project {
	byproject := make(map[string][]Container)
	for _, ct := range containers {
		byproject[ct.Project] = append(byproject[ct.Project], ct)
	}

	names := make([]string, 0, len(byproject))
	for name := range byproject {
		names = append(names, name)
	}
	sort.Strings(names)

	names = FilterProjects(names, wanted)

	projects := make([]Project, 0, len(names))
	for _, name := range names {
		cs := byproject[name]
		sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
		projects = append(projects, Project{Name: name, Containers: cs})
	}
	return projects
}

// FilterProjects keeps only the entries of all that also appear in wanted.
// An empty wanted returns all projects unchanged.
func FilterProjects(all, wanted []string) []string {
	if len(wanted) == 0 {
		return all
	}
	set := make(map[string]bool, len(wanted))
	for _, w := range wanted {
		set[w] = true
	}
	filtered := make([]string, 0, len(all))
	for _, name := range all {
		if set[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// Start starts the given container.
func (c *Client) Start(ctx context.Context, id string) error {
	return c.docker.ContainerStart(ctx, id, containertypes.StartOptions{})
}

// Stop stops the given container.
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.docker.ContainerStop(ctx, id, containertypes.StopOptions{})
}

// Restart restarts the given container.
func (c *Client) Restart(ctx context.Context, id string) error {
	return c.docker.ContainerRestart(ctx, id, containertypes.StopOptions{})
}

// StreamLogs follows the container's logs, writing demultiplexed stdout/stderr
// to w until ctx is cancelled or the stream ends.
func (c *Client) StreamLogs(ctx context.Context, id string, w io.Writer) error {
	reader, err := c.docker.ContainerLogs(ctx, id, containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "200",
	})
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = stdcopy.StdCopy(w, w, reader)
	return err
}

// InspectConfigText returns a human-readable dump of the container's config,
// matching the layout the terminal UI has always shown.
func (c *Client) InspectConfigText(ctx context.Context, id string, project, name string) (string, error) {
	resp, err := c.docker.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Container: %s/%s\n\n", project, name)
	b.WriteString("Configuration Details:\n")
	b.WriteString("---------------------\n")
	fmt.Fprintf(&b, "Hostname: %s\n", resp.Config.Hostname)
	fmt.Fprintf(&b, "Domainname: %s\n", resp.Config.Domainname)
	fmt.Fprintf(&b, "User: %s\n", resp.Config.User)
	fmt.Fprintf(&b, "AttachStdin: %t\n", resp.Config.AttachStdin)
	fmt.Fprintf(&b, "AttachStdout: %t\n", resp.Config.AttachStdout)
	fmt.Fprintf(&b, "AttachStderr: %t\n", resp.Config.AttachStderr)
	fmt.Fprintf(&b, "ExposedPorts: %v\n", resp.Config.ExposedPorts)
	fmt.Fprintf(&b, "Tty: %t\n", resp.Config.Tty)
	fmt.Fprintf(&b, "OpenStdin: %t\n", resp.Config.OpenStdin)
	fmt.Fprintf(&b, "StdinOnce: %t\n", resp.Config.StdinOnce)
	fmt.Fprintf(&b, "Image: %s\n", resp.Config.Image)
	fmt.Fprintf(&b, "Entrypoint: %v\n", resp.Config.Entrypoint)
	fmt.Fprintf(&b, "Cmd: %v\n", resp.Config.Cmd)
	fmt.Fprintf(&b, "WorkingDir: %s\n", resp.Config.WorkingDir)

	b.WriteString("\nEnvironment Variables:\n")
	env := append([]string(nil), resp.Config.Env...)
	sort.Strings(env)
	for _, v := range env {
		fmt.Fprintf(&b, "• %s\n", v)
	}

	b.WriteString("\nLabels:\n")
	labels := make([]string, 0, len(resp.Config.Labels))
	for k, v := range resp.Config.Labels {
		labels = append(labels, fmt.Sprintf("%s: %s", k, v))
	}
	sort.Strings(labels)
	for _, l := range labels {
		fmt.Fprintf(&b, "• %s\n", l)
	}

	b.WriteString("\nVolumes:\n")
	for vol := range resp.Config.Volumes {
		fmt.Fprintf(&b, "• %s\n", vol)
	}

	return b.String(), nil
}
