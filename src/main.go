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
	statusEmojiMap := map[string]string{
		"running":    "🟢",
		"exited":     "🔴",
		"paused":     "🟡",
		"restarting": "🟣",
		"created":    "🔵",
	}

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
			containerText := statusEmojiMap[container.State] + " " + container.Name
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

	controller.ServiceStatusView = serviceTreeView
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
				oldContainers[k] = v
			}

			controller.getServiceStatus()

			controller.app.QueueUpdateDraw(func() {
				expansionState := make(map[string]bool)

				root := controller.ServiceStatusView.GetRoot()
				if root != nil {
					for _, projectNode := range root.GetChildren() {
						projectName := projectNode.GetText()
						expansionState[projectName] = projectNode.IsExpanded()
					}
				}

				controller.getServiceListView()

				newRoot := controller.ServiceStatusView.GetRoot()
				for _, projectNode := range newRoot.GetChildren() {
					projectName := projectNode.GetText()
					if expanded, exists := expansionState[projectName]; exists {
						projectNode.SetExpanded(expanded)
					}
				}
			})
		}
	}
}

func main() {
	var controller AppController
	app := tview.NewApplication()
	controller.app = app

	// Initialize Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
		return
	}
	defer cli.Close()
	controller.DockerClient = cli

	controller.getServiceStatus()
	controller.getServiceListView()
	controller.getServiceLogsView()

	go controller.logContainerController()

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

	// Create a context that will be canceled when the application exits
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start service status updater in a goroutine
	go controller.updateServicesStatus(ctx)

	// Run the application with better error handling
	if err := app.SetRoot(base_flex, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Application error: %v\n", err)
	}
}
