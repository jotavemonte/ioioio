package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
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
	ServiceLogsLayout tview.Primitive
	DockerClient      *client.Client
	DebugOutput       *tview.TextView
	ConfiguraitonView *tview.TextView
	HelpView          *tview.TextView
	PagesHub          *tview.Pages
	ButtonsView       *tview.Flex
	app               *tview.Application
	stopLogs          chan bool
	startLogs         chan bool
	SearchInput       *tview.InputField
	LogBuffers        map[string][]string
	isSearching       bool
}

type LogsStream struct {
	ContainerID string
	CancelFunc  context.CancelFunc
}

var currentLogsStream *LogsStream = &LogsStream{}

var containers = make(map[string]Container)

func (controller *AppController) writeToDebug(text string) {
	if controller.DebugOutput == nil {
		return
	}

	controller.app.QueueUpdateDraw(func() {
		fmt.Fprintln(controller.DebugOutput, fmt.Sprint("• ", text))
		controller.DebugOutput.ScrollToEnd()
	})
}

func (controller *AppController) initLogs() {
	controller.startLogs <- true
}

func (controller *AppController) refreshContainerState() {
	controller.stopLogs <- true
	controller.startLogs <- true
}

func (controller *AppController) logContainerController() {
	for {
		if controller.ServiceLogsView != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for {
		<-controller.startLogs
		go controller.feedLogForContainer()
	}
}

func (controller *AppController) restartContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Restarting container " + containerIdentifier + "...")
	err := controller.DockerClient.ContainerRestart(ctx, containerId, containertypes.StopOptions{})
	if err != nil {
		controller.writeToDebug("Error restarting container " + containerIdentifier + ": " + err.Error())
		return
	}
	go controller.refreshContainerState()
	controller.writeToDebug("Container " + containerIdentifier + " restarted successfully.")
}

func (controller *AppController) stopContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Stopping container " + containerIdentifier + "...")
	err := controller.DockerClient.ContainerStop(ctx, containerId, containertypes.StopOptions{})
	if err != nil {
		controller.writeToDebug("Error stopping container " + containerIdentifier + ": " + err.Error())
		return
	}

	go controller.refreshContainerState()
	controller.writeToDebug("Container " + containerIdentifier + " stopped successfully.")
}

func (controller *AppController) startContainer() {
	ctx := context.Background()
	containerId := currentLogsStream.ContainerID

	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Starting container " + containerIdentifier + "...")
	err := controller.DockerClient.ContainerStart(ctx, containerId, containertypes.StartOptions{})
	if err != nil {
		controller.writeToDebug("Error starting container " + containerIdentifier + ": " + err.Error())
		return
	}

	go controller.refreshContainerState()
	controller.writeToDebug("Container " + containerIdentifier + " started successfully.")
}

func (controller *AppController) feedLogForContainer() {
	currentContainerId := currentLogsStream.ContainerID
	containerName := containers[currentContainerId].Name
	containerProject := containers[currentContainerId].Project
	controller.ServiceLogsView.SetTitle("[2]Logs - " + "(" + containerProject + "/" + containerName + ")")
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	reader, err := controller.DockerClient.ContainerLogs(ctx, currentLogsStream.ContainerID, containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "all", // or "200"
	})

	if err != nil {
		return
	}

	defer reader.Close()

	if controller.ServiceLogsView == nil {
		return
	}

	go func() {
		<-controller.stopLogs
		cancel()
	}()

	pr, pw := io.Pipe()
	go func() {
		stdcopy.StdCopy(pw, pw, reader)
		pw.Close()
	}()

	scanner := bufio.NewScanner(pr)

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			// Append to the correct buffer
			controller.LogBuffers[currentContainerId] = append(controller.LogBuffers[currentContainerId], line)
			if !controller.isSearching && currentLogsStream.ContainerID == currentContainerId {
				controller.app.QueueUpdateDraw(func() {
					fmt.Fprintln(controller.ServiceLogsView, line)
					controller.ServiceLogsView.ScrollToEnd()
				})
			}
		}
	}()

	controller.app.QueueUpdateDraw(func() {
		controller.ServiceLogsView.Clear()
		for _, line := range controller.LogBuffers[currentContainerId] {
			fmt.Fprintln(controller.ServiceLogsView, line)
		}
		controller.ServiceLogsView.ScrollToEnd()
	})
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
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprint(os.Stderr, "No containers found. Please ensure Docker is running and you have containers available.\n")
			os.Exit(1)
		}
	}()

	firstContainer := controller.ServiceStatusView.GetRoot().GetChildren()[0].GetChildren()[0]

	controller.ServiceStatusView.SetCurrentNode(firstContainer)
}

func (controller *AppController) getHelpView() {
	helpView := tview.NewTextView()
	helpView.SetDynamicColors(true)
	helpView.SetRegions(true)
	helpView.SetBorder(true)
	helpView.SetTitle("Help")
	helpView.SetTitleColor(tcell.ColorLimeGreen)
	helpView.SetBorderColor(tcell.ColorLimeGreen)
	helpView.SetBackgroundColor(tcell.ColorBlack)
	helpView.SetScrollable(true)

	helpText := `Container Statuses:
---
• 💚 - Running

• 🛑 - Exited

• 🟨 - Paused

• 🟣 - Restarting

• 🔷 - Created

Commands:
---
Global:
• Ctrl + A - Toggle logs view
• Ctrl + S - Toggle configuration view
• ? - Toggle help view
• g - Go to top of logs
• G - Go to bottom of logs
• x - Start selected container
• r - Restart selected container
• s - Stop selected container

Navigation:
• On the left pannel, use arrow keys or the mouse to navigate through services.
• On the left pannel, press enter to navigate to the main view.
• On the main view, press esc to return to the service list.`
	helpView.SetText(helpText)
	controller.HelpView = helpView
	controller.PagesHub.AddPage("help", helpView, true, false)
}

func (controller *AppController) getServiceListView() {
	serviceTreeView := tview.NewTreeView()
	serviceTreeView.SetBorder(true)
	serviceTreeView.SetTitle("[1]Service Status")
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

	argsProjects := os.Args[:]
	argsProjectsMap := make(map[string]bool)

	for _, arg := range argsProjects {
		argsProjectsMap[arg] = true
	}

	filteredProjects := make([]string, 0, len(projects))
	if len(argsProjects) > 1 {
		for _, arg := range projects {
			if exists, ok := argsProjectsMap[arg]; ok && exists {
				filteredProjects = append(filteredProjects, arg)
			}
		}
	} else {
		filteredProjects = append(filteredProjects, projects...)
	}

	for _, project := range filteredProjects {
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

	serviceTreeView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			controller.app.SetFocus(controller.PagesHub)
		default:
			return event
		}
		return event
	})

	serviceTreeView.SetChangedFunc(func(node *tview.TreeNode) {
		currentLogsStream.ContainerID = node.GetReference().(string)
		go controller.refreshContainerState()
		go controller.updateConfigView()

		// Load logs for the selected container
		controller.app.QueueUpdateDraw(func() {
			controller.ServiceLogsView.Clear()
			for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
				fmt.Fprintln(controller.ServiceLogsView, line)
			}
			controller.ServiceLogsView.ScrollToEnd()
		})
	})

	controller.ServiceStatusView = serviceTreeView
}

func buildContainerText(container Container) string {
	statusEmojiMap := map[string]string{
		"running":    "💚",
		"exited":     "🛑",
		"paused":     "🟨",
		"restarting": "🟣",
		"created":    "🔷",
	}
	return statusEmojiMap[container.State] + " " + container.Name
}

func (controller *AppController) getServiceLogsView() {
	logs_view := tview.NewTextView()
	logs_view.SetDynamicColors(true)
	logs_view.SetRegions(true)
	logs_view.SetBorder(true)
	logs_view.SetTitle("Logs")
	logs_view.SetTitleColor(tcell.ColorLimeGreen)
	logs_view.SetBorderColor(tcell.ColorLimeGreen)
	logs_view.SetBackgroundColor(tcell.ColorBlack)
	logs_view.SetScrollable(true)

	searchInput := tview.NewInputField().
		SetLabel("Search: [Press '/']").
		SetFieldWidth(30).
		SetFieldBackgroundColor(tcell.ColorDarkSlateGray).
		SetLabelColor(tcell.ColorAqua)

	controller.SearchInput = searchInput

	searchInput.SetChangedFunc(func(text string) {
		logs_view.Clear()

		if text == "" {
			for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
				fmt.Fprintln(logs_view, line)
			}
		}

		pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(text))
		if err != nil {
			for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
				fmt.Fprintln(logs_view, line)
			}
			return
		}

		for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
			if pattern.MatchString(line) {
				highlighted := pattern.ReplaceAllStringFunc(line, func(m string) string {
					return "[red]" + m + "[white]"
				})
				fmt.Fprintln(logs_view, highlighted)
			}
		}
	})

	searchInput.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			// Keep filtered/highlighted view, move focus to logs
			controller.isSearching = true
			logs_view.Clear()
			searchText := searchInput.GetText()
			if searchText == "" {
				for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
					fmt.Fprintln(logs_view, line)
				}
			} else {
				pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(searchText))
				if err != nil {
					for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
						fmt.Fprintln(logs_view, line)
					}
				} else {
					for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
						if pattern.MatchString(line) {
							highlighted := pattern.ReplaceAllStringFunc(line, func(m string) string {
								return "[red]" + m + "[white]"
							})
							fmt.Fprintln(logs_view, highlighted)
						}
					}
				}
			}
			controller.app.SetFocus(controller.ServiceLogsView)
		case tcell.KeyEsc:
			// Clear search and restore full log buffer
			controller.isSearching = false
			searchInput.SetText("")
			logs_view.Clear()
			for _, line := range controller.LogBuffers[currentLogsStream.ContainerID] {
				fmt.Fprintln(logs_view, line)
			}
			controller.app.SetFocus(controller.ServiceLogsView)
		}
	})

	logs_view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			controller.app.GetFocus()
			controller.app.SetFocus(controller.ServiceStatusView)
			return event
		case tcell.KeyRune:
			switch event.Rune() {
			case '/':
				searchInput.SetText("")
				controller.app.SetFocus(searchInput)
				return event
			}
			return event
		default:
			return event
		}
	})

	logs_view.SetChangedFunc(func() {
		controller.app.Draw()
	})

	logsLayout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(searchInput, 1, 0, false).
		AddItem(logs_view, 0, 1, true)

	controller.ServiceLogsView = logs_view
	controller.ServiceLogsLayout = logsLayout

	controller.PagesHub.AddPage("logs", controller.ServiceLogsLayout, true, true)
}

func (controller *AppController) updateServicesStatus() {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
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

func (controller *AppController) InitInterface() {
	app := tview.NewApplication()
	controller.app = app

	controller.PagesHub = tview.NewPages()
	controller.PagesHub.SetBackgroundColor(tcell.ColorBlack)
	controller.PagesHub.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			controller.app.GetFocus()
			controller.app.SetFocus(controller.ServiceStatusView)
			return event
		}
		return event
	})

	controller.getServiceStatus()
	controller.getServiceListView()
	controller.getServiceLogsView()
	controller.getServiceConfigurationView()
	controller.selectFirstContainer()
	controller.getButtonsView()
	controller.getHelpView()

	left_box := tview.NewFlex().SetDirection(tview.FlexRow)
	left_box.AddItem(controller.ServiceStatusView, 0, 8, true) // TreeView takes all the space

	horizontalFlex := tview.NewFlex().SetDirection(tview.FlexRow)
	horizontalFlex.SetBackgroundColor(tcell.ColorBlack)
	horizontalFlex.AddItem(controller.ButtonsView, 3, 0, false) // Add buttons view at the top
	horizontalFlex.AddItem(controller.PagesHub, 0, 6, false)

	controller.DebugOutput = tview.NewTextView()
	controller.DebugOutput.SetBorder(true).SetBorderColor(tcell.ColorDarkOliveGreen)
	controller.DebugOutput.SetTitle("Debug Output").SetTitleColor(tcell.ColorDarkOliveGreen)
	horizontalFlex.AddItem(controller.DebugOutput, 10, 0, false) // Add the bottom flex containing debug output and legend

	baseFlex := tview.NewFlex()
	baseFlex.SetBackgroundColor(tcell.ColorBlack)
	baseFlex.AddItem(left_box, 0, 25, true) // Left panel containing tree view
	baseFlex.AddItem(horizontalFlex, 0, 75, false)

	controller.setGlobalCommands()

	if err := controller.app.SetRoot(baseFlex, true).EnableMouse(true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Application error: %v\n", err)
	}
}

func (controller *AppController) getButtonsView() {
	buttonsView := tview.NewFlex().SetDirection(tview.FlexColumn)
	logsButton := tview.NewButton("<c-A> Logs").
		SetSelectedFunc(func() {
			controller.PagesHub.SwitchToPage("logs")
			go func() {
				time.Sleep(100 * time.Millisecond)
				controller.app.SetFocus(controller.ServiceLogsView)
			}()
		}).
		SetLabelColor(tcell.ColorLimeGreen)
	logsButton.SetBorder(true)
	logsButton.SetBackgroundColorActivated(tcell.ColorBlack)
	logsButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorBlack))
	logsButton.SetBorderColor(tcell.ColorLimeGreen)
	configButton := tview.NewButton("<c-S> Config").
		SetSelectedFunc(func() {
			controller.PagesHub.SwitchToPage("config")
			go func() {
				time.Sleep(100 * time.Millisecond)
				controller.app.SetFocus(controller.ConfiguraitonView)
			}()
		}).
		SetLabelColor(tcell.ColorLimeGreen)
	configButton.SetBorder(true)
	configButton.SetBackgroundColorActivated(tcell.ColorBlack)
	configButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorBlack))
	configButton.SetBorderColor(tcell.ColorLimeGreen)

	helpButton := tview.NewButton("<?> Help").
		SetSelectedFunc(func() {
			controller.PagesHub.SwitchToPage("help")
			go func() {
				time.Sleep(100 * time.Millisecond)
				controller.app.SetFocus(controller.ConfiguraitonView)
			}()
		}).
		SetLabelColor(tcell.ColorLimeGreen)
	helpButton.SetBorder(true)
	helpButton.SetBackgroundColorActivated(tcell.ColorBlack)
	helpButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorBlack))
	helpButton.SetBorderColor(tcell.ColorLimeGreen)

	buttonsView.AddItem(logsButton, 15, 0, false).
		AddItem(configButton, 15, 0, false).
		AddItem(helpButton, 15, 0, false).
		AddItem(tview.NewBox().SetBackgroundColor(tcell.ColorBlack), 0, 1, false)
	buttonsView.SetBackgroundColor(tcell.ColorBlack)
	controller.ButtonsView = buttonsView
}

func (controller *AppController) updateConfigView() {
	resp, err := controller.DockerClient.ContainerInspect(context.Background(), currentLogsStream.ContainerID)
	if err != nil {
		controller.writeToDebug("Error inspecting container: " + err.Error())
		return
	}

	var builder strings.Builder

	containerName := containers[currentLogsStream.ContainerID].Name
	containerProject := containers[currentLogsStream.ContainerID].Project

	builder.WriteString(fmt.Sprintf("Container: %s/%s\n\n", containerProject, containerName))

	builder.WriteString("Configuration Details:\n")
	builder.WriteString("---------------------\n")
	builder.WriteString(fmt.Sprintf("Hostname: %s\n", resp.Config.Hostname))
	builder.WriteString(fmt.Sprintf("Domainname: %s\n", resp.Config.Domainname))
	builder.WriteString(fmt.Sprintf("User: %s\n", resp.Config.User))
	builder.WriteString(fmt.Sprintf("AttachStdin: %t\n", resp.Config.AttachStdin))
	builder.WriteString(fmt.Sprintf("AttachStdout: %t\n", resp.Config.AttachStdout))
	builder.WriteString(fmt.Sprintf("AttachStderr: %t\n", resp.Config.AttachStderr))
	builder.WriteString(fmt.Sprintf("ExposedPorts: %v\n", resp.Config.ExposedPorts))
	builder.WriteString(fmt.Sprintf("Tty: %t\n", resp.Config.Tty))
	builder.WriteString(fmt.Sprintf("OpenStdin: %t\n", resp.Config.OpenStdin))
	builder.WriteString(fmt.Sprintf("StdinOnce: %t\n", resp.Config.StdinOnce))

	builder.WriteString(fmt.Sprintf("Image: %s\n", resp.Config.Image))
	builder.WriteString(fmt.Sprintf("Entrypoint: %v\n", resp.Config.Entrypoint))
	builder.WriteString(fmt.Sprintf("Cmd: %v\n", resp.Config.Cmd))
	builder.WriteString(fmt.Sprintf("WorkingDir: %s\n", resp.Config.WorkingDir))

	builder.WriteString("\nEnvironment Variables:\n")
	envVars := resp.Config.Env
	sort.Strings(envVars)
	for _, value := range envVars {
		builder.WriteString(fmt.Sprintf("• %s\n", value))
	}

	builder.WriteString("\nLabels:\n")
	labels := make([]string, 0, len(resp.Config.Labels))
	for k, v := range resp.Config.Labels {
		labels = append(labels, fmt.Sprintf("%s: %s", k, v))
	}
	sort.Strings(labels)
	for _, label := range labels {
		builder.WriteString(fmt.Sprintf("• %s\n", label))
	}

	builder.WriteString("\nVolumes:\n")
	for vol := range resp.Config.Volumes {
		builder.WriteString(fmt.Sprintf("• %s\n", vol))
	}

	controller.app.QueueUpdateDraw(func() {
		controller.ConfiguraitonView.SetText(builder.String())
		controller.ConfiguraitonView.ScrollToBeginning()
	})
}

func (controller *AppController) getServiceConfigurationView() {
	configView := tview.NewTextView()
	configView.SetDynamicColors(true)
	configView.SetRegions(true)
	configView.SetBorder(true)
	configView.SetTitle("Logs")
	configView.SetTitleColor(tcell.ColorLimeGreen)
	configView.SetBorderColor(tcell.ColorLimeGreen)
	configView.SetBackgroundColor(tcell.ColorBlack)
	configView.SetScrollable(true)
	configView.SetTitle("Service Configuration")

	// Placeholder for service configuration
	configContent := "Service configuration will be displayed here.\n"
	configContent += "This feature is not yet implemented."

	configView.SetText(configContent)

	controller.ConfiguraitonView = configView

	controller.PagesHub.AddPage("config", controller.ConfiguraitonView, true, false)
}

func (controller *AppController) InitDockerCLI() {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
		return
	}

	controller.DockerClient = cli
}

func (controller *AppController) setGlobalCommands() {
	controller.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if controller.SearchInput != nil && controller.SearchInput.HasFocus() {
			// Skip global keybindings while typing in search
			return event
		}
		switch event.Rune() {
		case 'g':
			controller.app.SetFocus(controller.ServiceLogsView)
			controller.ServiceLogsView.ScrollToBeginning()
		case 'G':
			controller.app.SetFocus(controller.ServiceLogsView)
			controller.ServiceLogsView.ScrollToEnd()
		case 'r', 'R':
			go controller.restartContainer()
		case 's', 'S':
			go controller.stopContainer()
		case 'x', 'X':
			go controller.startContainer()
		case '?':
			controller.PagesHub.SwitchToPage("help")
		case '1':
			controller.app.SetFocus(controller.ServiceStatusView)
			return event
		case '2':
			controller.app.SetFocus(controller.ServiceLogsView)
			return event
		case '3':
			controller.app.SetFocus(controller.DebugOutput)
			return event
		}

		switch event.Key() {
		case tcell.KeyCtrlA:
			controller.PagesHub.SwitchToPage("logs")
		case tcell.KeyCtrlS:
			controller.PagesHub.SwitchToPage("config")

		}
		return event
	})
}

func main() {
	var controller AppController
	controller.LogBuffers = make(map[string][]string)
	controller.startLogs, controller.stopLogs = make(chan bool, 2), make(chan bool, 2)

	controller.InitDockerCLI()
	defer controller.DockerClient.Close()

	controller.InitInterface()

	go controller.logContainerController()
	go controller.updateServicesStatus()
	go controller.initLogs()

}
