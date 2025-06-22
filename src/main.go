package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Container struct {
	ID      string
	Name    string
	Project string
	State   string
}

type AppController struct {
	ServiceStatusView *tview.TreeView
	ServiceLogsView   *tview.TextView
	DockerClient      *client.Client
	DebugOutput       *tview.TextView
	app               *tview.Application
}

type LogsStream struct {
	ContainerID string
	CancelFunc  context.CancelFunc
}

var currentLogsStream *LogsStream = &LogsStream{}

var containers = make(map[string]Container)

func (controller *AppController) setCurrentNodeOnNavigation() {
	for {
		if controller.ServiceStatusView == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		containerId := currentLogsStream.ContainerID
		currentContainerId := controller.ServiceStatusView.GetCurrentNode().GetReference().(string)

		if containerId != currentContainerId {
			currentLogsStream.ContainerID = currentContainerId
		}
	}
}

func (controller *AppController) logContainerController() {
	currentContainerId := currentLogsStream.ContainerID

	for {
		if currentContainerId != currentLogsStream.ContainerID {
			currentContainerId = currentLogsStream.ContainerID
			controller.ServiceLogsView.Clear()
			go controller.feedLogForContainer()
		}
	}

}

func (controller *AppController) restartContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	err := controller.DockerClient.ContainerRestart(ctx, containerId, containertypes.StopOptions{})
	if err != nil {
		fmt.Fprintf(controller.DebugOutput, "Error restarting container %s: %v\n", containerId, err)
		return
	}

	fmt.Fprintf(controller.DebugOutput, "Container %s restarted successfully.\n", containerId)
}

func (controller *AppController) stopContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	err := controller.DockerClient.ContainerStop(ctx, containerId, containertypes.StopOptions{})
	if err != nil {
		fmt.Fprintf(controller.DebugOutput, "Error stopping container %s: %v\n", containerId, err)
		return
	}

	fmt.Fprintf(controller.DebugOutput, "Container %s stopped successfully.\n", containerId)
}

func (controller *AppController) startContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	err := controller.DockerClient.ContainerStart(ctx, containerId, containertypes.StartOptions{})
	if err != nil {
		fmt.Fprintf(controller.DebugOutput, "Error starting container %s: %v\n", containerId, err)
		return
	}

	fmt.Fprintf(controller.DebugOutput, "Container %s started successfully.\n", containerId)
}

func (controller *AppController) feedLogForContainer() {
	currentContainerId := currentLogsStream.ContainerID
	containerName := containers[currentContainerId].Name
	containerProject := containers[currentContainerId].Project
	controller.ServiceLogsView.SetTitle("Logs - " + "(" + containerProject + "/" + containerName + ")")
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := controller.DockerClient.ContainerLogs(ctx, currentLogsStream.ContainerID, containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,  // Stream logs
		Tail:       "400", // Tail the last 100 lines
	})
	defer cancel()

	if err != nil {
		return
	}

	scanner := bufio.NewScanner(reader)

	if controller.ServiceLogsView == nil {
		cancel()
		return
	}

	// Scroll to the end of the logs view after a blink
	go func() {
		time.Sleep(200 * time.Millisecond)
		controller.app.QueueUpdateDraw(func() {
			controller.ServiceLogsView.ScrollToEnd()
		})
	}()

	for scanner.Scan() {
		text := scanner.Text()
		if currentContainerId != currentLogsStream.ContainerID {
			ctx.Done()
			return
		}

		if strings.TrimSpace(text) == "" {
			continue
		}

		controller.app.QueueUpdateDraw(func() {
			fmt.Fprintln(controller.ServiceLogsView, "---------")
			fmt.Fprintln(controller.ServiceLogsView, text)
		})
	}
}

func (controller *AppController) getServiceStatus() {
	ctx := context.Background()

	fetchedContainers, err := controller.DockerClient.ContainerList(ctx, containertypes.ListOptions{All: true})
	if err != nil {
		return
	}

	for _, fetchedContainer := range fetchedContainers {
		name := fetchedContainer.Labels["com.docker.compose.service"]
		if name == "" {
			// If not a compose service, use container name
			if len(fetchedContainer.Names) > 0 {
				name = fetchedContainer.Names[0]
				if len(name) > 0 && name[0] == '/' {
					name = name[1:]
				}
			}
		}

		project := fetchedContainer.Labels["com.docker.compose.project"]
		if project == "" {
			project = "standalone"
		}

		containers[fetchedContainer.ID] = Container{
			ID:      fetchedContainer.ID,
			Name:    name,
			Project: project,
			State:   fetchedContainer.State,
		}
	}
}

func (controller *AppController) selectFirstContainer() {
	// Implement the function logic or leave it empty for now
	fistContainer := controller.ServiceStatusView.GetRoot().GetChildren()[0].GetChildren()[0]

	controller.ServiceStatusView.SetCurrentNode(fistContainer)
}

func (controller *AppController) getServiceListView() {

	serviceTreeView := tview.NewTreeView()
	serviceTreeView.SetBorder(true)
	serviceTreeView.SetTitle("Service Status")
	serviceTreeView.SetTitleColor(tcell.ColorLimeGreen)
	serviceTreeView.SetBorderColor(tcell.ColorLimeGreen)
	serviceTreeView.SetGraphics(true)
	serviceTreeView.SetGraphicsColor(tcell.ColorGreen)

	root := tview.NewTreeNode("Services").
		SetColor(tcell.ColorYellow).
		SetSelectable(false)
	serviceTreeView.SetRoot(root)

	projectMap := make(map[string][]Container)
	for _, container := range containers {
		projectMap[container.Project] = append(projectMap[container.Project], container)
	}

	projects := make([]string, 0, len(projectMap))
	for project := range projectMap {
		projects = append(projects, project)
	}
	sort.Strings(projects)

	for _, project := range projects {
		projectNode := tview.NewTreeNode(project).
			SetColor(tcell.ColorBlue).
			SetSelectable(false).
			SetExpanded(true)
		root.AddChild(projectNode)

		containers := projectMap[project]
		sort.Slice(containers, func(i, j int) bool {
			return containers[i].Name < containers[j].Name
		})

		for _, container := range containers {
			containerText := buildContainerText(container)
			containerNode := tview.NewTreeNode(containerText).
				SetColor(tcell.ColorWhite).
				SetReference(container.ID) // Store container ID as reference

			projectNode.AddChild(containerNode)
		}
	}

	serviceTreeView.SetSelectedFunc(func(node *tview.TreeNode) {
		containerID, ok := node.GetReference().(string)

		if !ok {
			return
		}

		controller.app.SetFocus(controller.ServiceLogsView)

		currentLogsStream.ContainerID = containerID
	})

	serviceTreeView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		currentLogsStream.ContainerID = controller.ServiceStatusView.GetCurrentNode().GetReference().(string)
		switch event.Rune() {
		case 'r', 'R':
			controller.restartContainer()
		case 's', 'S':
			controller.stopContainer()
		case 'x', 'X':
			controller.startContainer()
		default:
			return event
		}
		return event
	})

	controller.ServiceStatusView = serviceTreeView
}

func buildContainerText(container Container) string {
	statusEmojiMap := map[string]string{
		"running":    "🟢",
		"exited":     "🔴",
		"paused":     "🟡",
		"restarting": "🟣",
		"created":    "🔵",
	}
	return statusEmojiMap[container.State] + " " + container.Name
}

func (controller *AppController) getServiceLogsView() {

	logs_view := tview.NewTextView()

	logs_view.SetBorder(true)
	logs_view.SetTitle("Logs")
	logs_view.SetTitleColor(tcell.ColorLimeGreen)
	logs_view.SetBorderColor(tcell.ColorLimeGreen)
	logs_view.SetBackgroundColor(tcell.ColorBlack)
	logs_view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			controller.app.GetFocus()
			controller.app.SetFocus(controller.ServiceStatusView)
			return event
		default:
			return event
		}
	})
	controller.ServiceLogsView = logs_view
}

func (controller *AppController) updateServicesStatus(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			oldContainers := make(map[string]Container)
			for k, v := range containers {
				vCopy := Container{
					ID:      strings.Clone(v.ID),
					Name:    strings.Clone(v.Name),
					Project: strings.Clone(v.Project),
					State:   strings.Clone(v.State),
				}
				oldContainers[strings.Clone(k)] = vCopy
			}

			controller.getServiceStatus()

			containersToUpdate := []string{}
			for k, v := range oldContainers {
				if containers[k].State != v.State {
					containersToUpdate = append(containersToUpdate, k)
				}
			}
			serviceNodes := make(map[string]*tview.TreeNode)
			for _, node := range controller.ServiceStatusView.GetRoot().GetChildren() {
				serviceNodes[node.GetText()] = node
			}

			for _, containerID := range containersToUpdate {
				container := containers[containerID]
				projectNode, ok := serviceNodes[container.Project]
				if !ok {
					continue // Project node not found
				}

				var containerNode *tview.TreeNode
				for _, child := range projectNode.GetChildren() {
					if child.GetReference() == container.ID {
						containerNode = child
						break
					}
				}

				if containerNode == nil {
					continue // Container node not found
				}

				containerText := buildContainerText(container)

				controller.app.QueueUpdateDraw(func() {
					containerNode.SetText(containerText)
				})
			}
		}
	}
}

func (controller *AppController) InitInterface() {
	app := tview.NewApplication()
	controller.app = app

	controller.getServiceStatus()
	controller.getServiceListView()
	controller.getServiceLogsView()
	controller.selectFirstContainer()

	left_box := tview.NewFlex().SetDirection(tview.FlexRow)
	left_box.AddItem(controller.ServiceStatusView, 0, 1, true) // TreeView takes all the space

	horizontal_flex := tview.NewFlex().SetDirection(tview.FlexRow)
	horizontal_flex.AddItem(controller.ServiceLogsView, 0, 6, false)

	if slices.Contains(os.Environ(), "DEBUG=1") {
		controller.DebugOutput = tview.NewTextView()
		horizontal_flex.AddItem(controller.DebugOutput, 0, 1, false)
	}

	base_flex := tview.NewFlex()
	base_flex.AddItem(left_box, 0, 25, true) // Left panel containing tree view
	base_flex.AddItem(horizontal_flex, 0, 75, false)

	if err := controller.app.SetRoot(base_flex, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Application error: %v\n", err)
	}
}

func (controller *AppController) InitDockerCLI() {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
		return
	}

	controller.DockerClient = cli
}

func main() {
	var controller AppController

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller.InitDockerCLI()
	defer controller.DockerClient.Close()

	go controller.logContainerController()
	go controller.updateServicesStatus(ctx)
	go controller.setCurrentNodeOnNavigation()

	controller.InitInterface()
}
