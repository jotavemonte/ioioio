package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"jotavemonte/ioioio/internal/core"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type AppController struct {
	ServiceStatusView *tview.TreeView
	ServiceLogsView   *tview.TextView
	Docker            *core.Client
	DebugOutput       *tview.TextView
	ConfiguraitonView *tview.TextView
	HelpView          *tview.TextView
	PagesHub          *tview.Pages
	ButtonsView       *tview.Flex
	app               *tview.Application
	stopLogs          chan bool
	startLogs         chan bool
}

type LogsStream struct {
	ContainerID string
	CancelFunc  context.CancelFunc
}

var currentLogsStream *LogsStream = &LogsStream{}

var containers = make(map[string]core.Container)

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
	containerId := currentLogsStream.ContainerID
	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Restarting container " + containerIdentifier + "...")
	if err := controller.Docker.Restart(context.Background(), containerId); err != nil {
		controller.writeToDebug("Error restarting container " + containerIdentifier + ": " + err.Error())
		return
	}
	go controller.refreshContainerState()
	controller.writeToDebug("Container " + containerIdentifier + " restarted successfully.")
}

func (controller *AppController) stopContainer() {
	containerId := currentLogsStream.ContainerID
	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Stopping container " + containerIdentifier + "...")
	if err := controller.Docker.Stop(context.Background(), containerId); err != nil {
		controller.writeToDebug("Error stopping container " + containerIdentifier + ": " + err.Error())
		return
	}

	go controller.refreshContainerState()
	controller.writeToDebug("Container " + containerIdentifier + " stopped successfully.")
}

func (controller *AppController) startContainer() {
	containerId := currentLogsStream.ContainerID
	if containerId == "" {
		return
	}

	container := containers[containerId]
	containerIdentifier := container.Project + "/" + container.Name

	controller.writeToDebug("Starting container " + containerIdentifier + "...")
	if err := controller.Docker.Start(context.Background(), containerId); err != nil {
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
	controller.ServiceLogsView.SetTitle("Logs - " + "(" + containerProject + "/" + containerName + ")")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if controller.ServiceLogsView == nil {
		return
	}

	w := tview.ANSIWriter(controller.ServiceLogsView)

	go func() {
		<-controller.stopLogs
		cancel()
	}()

	go func() {
		time.Sleep(200 * time.Millisecond)
		controller.app.QueueUpdateDraw(func() {
			controller.ServiceLogsView.ScrollToEnd()
		})
	}()

	controller.app.QueueUpdateDraw(func() {
		controller.ServiceLogsView.Clear()
	})
	controller.Docker.StreamLogs(ctx, currentContainerId, w)
	cancel()
}

func (controller *AppController) getServiceStatus() {
	fetched, err := controller.Docker.List(context.Background())
	if err != nil {
		return
	}
	for id, c := range fetched {
		containers[id] = c
	}
}

func (controller *AppController) selectFirstContainer() {
	defer func() {
		if err := recover(); err != nil {
			fmt.Fprint(os.Stderr, "No containers found. Please ensure Docker is running and you have containers available.\n")
			os.Exit(1)
		}
	}()

	fistContainer := controller.ServiceStatusView.GetRoot().GetChildren()[0].GetChildren()[0]

	controller.ServiceStatusView.SetCurrentNode(fistContainer)
}

func (controller *AppController) getHelpView() {
	helpView := tview.NewTextView()
	helpView.SetRegions(true)
	helpView.SetBorder(true)
	helpView.SetTitle("Help")
	helpView.SetTitleColor(tcell.ColorLimeGreen)
	helpView.SetBorderColor(tcell.ColorLimeGreen)
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
	serviceTreeView.SetTitle("Service Status")
	serviceTreeView.SetTitleColor(tcell.ColorLimeGreen)
	serviceTreeView.SetBorderColor(tcell.ColorLimeGreen)
	serviceTreeView.SetGraphicsColor(tcell.ColorLimeGreen)

	root := tview.NewTreeNode("Services").
		SetColor(tcell.ColorRed).
		SetSelectable(false)
	serviceTreeView.SetRoot(root)

	projects := core.GroupByProject(containers, os.Args[1:])

	for _, project := range projects {
		projectNode := tview.NewTreeNode(project.Name).
			SetColor(tcell.ColorYellow).
			SetSelectable(false).
			SetExpanded(true)
		root.AddChild(projectNode)

		for _, container := range project.Containers {
			containerText := buildContainerText(container)
			containerNode := tview.NewTreeNode(containerText).
				SetReference(container.ID). // Store container ID as reference
				SetColor(tcell.ColorBlue)

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
	})

	controller.ServiceStatusView = serviceTreeView
}

func buildContainerText(container core.Container) string {
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
	logs_view.SetRegions(true)
	logs_view.SetBorder(true)
	logs_view.SetTitle("Logs")
	logs_view.SetTitleColor(tcell.ColorLimeGreen)
	logs_view.SetBorderColor(tcell.ColorLimeGreen)
	logs_view.SetScrollable(true)
	logs_view.SetChangedFunc(func() {
		controller.app.Draw()
	})
	controller.ServiceLogsView = logs_view
	controller.PagesHub.AddPage("logs", controller.ServiceLogsView, true, true)
}

func (controller *AppController) updateServicesStatus() {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		oldContainers := make(map[string]core.Container)
		for k, v := range containers {
			oldContainers[k] = v
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
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	app := tview.NewApplication()
	controller.app = app

	controller.PagesHub = tview.NewPages()
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
	horizontalFlex.AddItem(controller.ButtonsView, 3, 0, false) // Add buttons view at the top
	horizontalFlex.AddItem(controller.PagesHub, 0, 6, false)

	controller.DebugOutput = tview.NewTextView()
	controller.DebugOutput.SetBorder(true).SetBorderColor(tcell.ColorDarkOliveGreen)
	controller.DebugOutput.SetTitle("Debug Output").SetTitleColor(tcell.ColorDarkOliveGreen)
	horizontalFlex.AddItem(controller.DebugOutput, 10, 0, false) // Add the bottom flex containing debug output and legend

	baseFlex := tview.NewFlex()
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
	logsButton.SetBackgroundColorActivated(tcell.ColorDefault)
	logsButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorDefault))
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
	configButton.SetBackgroundColorActivated(tcell.ColorDefault)
	configButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorDefault))
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
	helpButton.SetBackgroundColorActivated(tcell.ColorDefault)
	helpButton.SetStyle(tcell.StyleDefault.Background(tcell.ColorDefault))
	helpButton.SetBorderColor(tcell.ColorLimeGreen)

	buttonsView.AddItem(logsButton, 15, 0, false).
		AddItem(configButton, 15, 0, false).
		AddItem(helpButton, 15, 0, false).
		AddItem(tview.NewBox(), 0, 1, false)
	controller.ButtonsView = buttonsView
}

func (controller *AppController) updateConfigView() {
	containerName := containers[currentLogsStream.ContainerID].Name
	containerProject := containers[currentLogsStream.ContainerID].Project

	text, err := controller.Docker.InspectConfigText(context.Background(), currentLogsStream.ContainerID, containerProject, containerName)
	if err != nil {
		controller.writeToDebug("Error inspecting container: " + err.Error())
		return
	}

	controller.app.QueueUpdateDraw(func() {
		controller.ConfiguraitonView.SetText(text)
		controller.ConfiguraitonView.ScrollToBeginning()
	})
}

func (controller *AppController) getServiceConfigurationView() {
	configView := tview.NewTextView()
	configView.SetRegions(true)
	configView.SetBorder(true)
	configView.SetTitle("Logs")
	configView.SetTitleColor(tcell.ColorLimeGreen)
	configView.SetBorderColor(tcell.ColorLimeGreen)
	configView.SetScrollable(true)
	configView.SetTitle("Service Configuration")

	// Placeholder for service configuration
	configContent := "Service configuration will be displayed here.\n"
	configContent += "This feature is not yet implemented."

	configView.SetText(configContent)

	controller.ConfiguraitonView = configView

	controller.PagesHub.AddPage("config", controller.ConfiguraitonView, true, false)
}

func (controller *AppController) setGlobalCommands() {
	controller.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
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

	controller.startLogs, controller.stopLogs = make(chan bool, 2), make(chan bool, 2)

	docker, err := core.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
		os.Exit(1)
	}
	controller.Docker = docker
	defer controller.Docker.Close()

	go controller.logContainerController()
	go controller.updateServicesStatus()
	go controller.initLogs()

	controller.InitInterface()
}
