// Command ioioio-mac is a native macOS front-end for the ioioio Docker monitor,
// built with DarwInKit (AppKit bindings for Go). It mirrors the terminal UI's
// feature set — a project/container source list, live log streaming, container
// config, and start/stop/restart actions — in an idiomatic Mac window.
//
// Every AppKit object is mutated only on the main thread; Docker calls run on
// background goroutines and marshal their results back via dispatch.MainQueue.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"jotavemonte/ioioio/internal/core"

	"github.com/progrium/darwinkit/dispatch"
	"github.com/progrium/darwinkit/helper/action"
	"github.com/progrium/darwinkit/macos"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
	"github.com/progrium/darwinkit/objc"
)

// row is a flattened entry in the sidebar table: either a project group header
// or a selectable container beneath it.
type row struct {
	isGroup   bool
	project   string
	container core.Container
}

type ui struct {
	docker *core.Client

	window     appkit.Window
	split      appkit.SplitView
	table      appkit.TableView
	logsView   appkit.TextView
	configView appkit.TextView
	logsScroll appkit.ScrollView
	cfgScroll  appkit.ScrollView
	// logsPane wraps logsScroll plus the overlaid jump button; showTab hides the
	// whole pane so the button doesn't linger over the config view.
	logsPane appkit.View
	// jumpBtn floats over the bottom-right of the logs pane; it appears when the
	// user has scrolled up away from the tail and jumps back to the bottom.
	jumpBtn appkit.Button
	tabs    appkit.SegmentedControl
	status  appkit.TextField

	startBtn   appkit.Button
	stopBtn    appkit.Button
	restartBtn appkit.Button
	shellBtn   appkit.Button

	findBar   appkit.StackView
	findField appkit.SearchField
	findCount appkit.TextField
	matches   []foundation.Range
	matchIdx  int

	rows       []row
	selectedID string
	// restoringSelection suppresses the selection-changed handler while we
	// programmatically re-highlight a row after a reload, so the log stream
	// isn't needlessly restarted.
	restoringSelection bool
	// actionInFlight disables the action buttons while a start/stop/restart
	// Docker call is running, to prevent double-clicks.
	actionInFlight bool

	logsCancel context.CancelFunc
}

// selectedState returns the Docker state of the currently selected container,
// or "" when nothing is selected.
func (u *ui) selectedState() string {
	for _, r := range u.rows {
		if !r.isGroup && r.container.ID == u.selectedID {
			return r.container.State
		}
	}
	return ""
}

// updateButtons enables/disables the action buttons to match the selected
// container's state. Must be called on the main thread.
func (u *ui) updateButtons() {
	state := u.selectedState()
	running := state == "running"
	selected := state != ""

	enabled := selected && !u.actionInFlight
	// Start is available only when the container is not already running.
	u.startBtn.SetEnabled(enabled && !running)
	// Stop, Restart and Shell require a running container.
	u.stopBtn.SetEnabled(enabled && running)
	u.restartBtn.SetEnabled(enabled && running)
	u.shellBtn.SetEnabled(enabled && running)
}

// activeTextView returns the text view for the pane currently shown.
func (u *ui) activeTextView() appkit.TextView {
	if u.tabs.SelectedSegment() == 1 {
		return u.configView
	}
	return u.logsView
}

func stateColor(state string) appkit.Color {
	switch state {
	case "running":
		return appkit.Color_SystemGreenColor()
	case "exited":
		return appkit.Color_SystemRedColor()
	case "paused":
		return appkit.Color_SystemYellowColor()
	case "restarting":
		return appkit.Color_SystemPurpleColor()
	case "created":
		return appkit.Color_SystemBlueColor()
	default:
		return appkit.Color_SystemGrayColor()
	}
}

func main() {
	docker, err := core.NewClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Docker client: %v\n", err)
		os.Exit(1)
	}
	defer docker.Close()

	u := &ui{docker: docker}

	macos.RunApp(func(app appkit.Application, delegate *appkit.ApplicationDelegate) {
		u.build()
		u.setMainMenu(app)
		app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
		app.ActivateIgnoringOtherApps(true)

		u.reload()
		go u.watch()
	})
}

// setMainMenu installs a minimal menu bar with Quit (⌘Q) and Find (⌘F).
func (u *ui) setMainMenu(app appkit.Application) {
	menuBar := appkit.NewMenuWithTitle("ioioio")

	appItem := appkit.NewMenuItem()
	appMenu := appkit.NewMenuWithTitle("ioioio")
	appMenu.AddItem(appkit.NewMenuItemWithAction("Quit ioioio", "q", func(objc.Object) { app.Terminate(nil) }))
	appItem.SetSubmenu(appMenu)
	menuBar.AddItem(appItem)

	editItem := appkit.NewMenuItem()
	editMenu := appkit.NewMenuWithTitle("Edit")
	// Standard editing items with nil target: AppKit dispatches these selectors
	// down the responder chain to the focused text view, which is what makes
	// ⌘C / ⌘A work on the log and config buffers. Without them the keystrokes
	// have no menu item to trigger and are ignored.
	editMenu.AddItem(appkit.NewMenuItemWithTitleActionKeyEquivalent("Copy", objc.Sel("copy:"), "c"))
	editMenu.AddItem(appkit.NewMenuItemWithTitleActionKeyEquivalent("Select All", objc.Sel("selectAll:"), "a"))
	editMenu.AddItem(appkit.NewMenuItemWithAction("Find", "f", func(objc.Object) { u.toggleFind() }))
	editItem.SetSubmenu(editMenu)
	menuBar.AddItem(editItem)

	app.SetMainMenu(menuBar)
}

func (u *ui) build() {
	w := appkit.NewWindowWithSizeAndStyle(960, 600,
		appkit.WindowStyleMaskTitled|
			appkit.WindowStyleMaskClosable|
			appkit.WindowStyleMaskMiniaturizable|
			appkit.WindowStyleMaskResizable)
	objc.Retain(&w)
	w.SetTitle("ioioio — Docker Container Monitor")
	w.SetContentMinSize(foundation.Size{Width: 720, Height: 420})
	u.window = w

	split := appkit.NewSplitView()
	split.SetVertical(true)
	split.SetTranslatesAutoresizingMaskIntoConstraints(false)
	split.SetDividerStyle(appkit.SplitViewDividerStyleThin)

	split.AddArrangedSubview(u.buildSidebar())
	split.AddArrangedSubview(u.buildDetail())

	content := w.ContentView()
	content.AddSubview(split)
	split.TopAnchor().ConstraintEqualToAnchor(content.TopAnchor()).SetActive(true)
	split.BottomAnchor().ConstraintEqualToAnchor(content.BottomAnchor()).SetActive(true)
	split.LeadingAnchor().ConstraintEqualToAnchor(content.LeadingAnchor()).SetActive(true)
	split.TrailingAnchor().ConstraintEqualToAnchor(content.TrailingAnchor()).SetActive(true)
	u.split = split

	// Start maximized to the screen's visible area.
	w.SetFrameDisplay(appkit.Screen_MainScreen().VisibleFrame(), true)
	w.MakeKeyAndOrderFront(nil)

	// Sidebar 20% / buffer 80%. Positioned once the window has its final size.
	dispatch.MainQueue().DispatchAsync(func() {
		u.split.SetPositionOfDividerAtIndex(u.split.Frame().Size.Width*0.2, 0)
	})
}

func (u *ui) buildSidebar() appkit.ScrollView {
	table := appkit.NewTableView()
	table.SetSelectionHighlightStyle(appkit.TableViewSelectionHighlightStyleSourceList)
	table.SetFloatsGroupRows(false)
	table.SetRowHeight(24)

	col := appkit.NewTableColumn().InitWithIdentifier("service")
	col.SetTitle("Services")
	col.SetWidth(260)
	table.AddTableColumn(col)

	ds := &tableDataSource{rowCount: func() int { return len(u.rows) }}
	table.SetDataSource(ds)

	del := &appkit.TableViewDelegate{}
	del.SetTableViewIsGroupRow(func(_ appkit.TableView, r int) bool {
		return r >= 0 && r < len(u.rows) && u.rows[r].isGroup
	})
	del.SetTableViewShouldSelectRow(func(_ appkit.TableView, r int) bool {
		return r >= 0 && r < len(u.rows) && !u.rows[r].isGroup
	})
	del.SetTableViewViewForTableColumnRow(func(_ appkit.TableView, _ appkit.TableColumn, r int) appkit.View {
		if r < 0 || r >= len(u.rows) {
			return appkit.View{}
		}
		row := u.rows[r]
		if row.isGroup {
			label := appkit.NewLabel(row.project)
			label.SetFont(appkit.Font_BoldSystemFontOfSize(11))
			label.SetTextColor(appkit.Color_SecondaryLabelColor())
			return label.View
		}
		dot := appkit.NewLabel("●")
		dot.SetTextColor(stateColor(row.container.State))
		name := appkit.NewLabel(row.container.Name)
		stack := appkit.StackView_StackViewWithViews([]appkit.IView{dot, name})
		stack.SetOrientation(appkit.UserInterfaceLayoutOrientationHorizontal)
		stack.SetSpacing(6)
		return stack.View
	})
	del.SetTableViewSelectionDidChange(func(foundation.Notification) {
		if u.restoringSelection {
			return
		}
		u.onSelect(u.table.SelectedRow())
	})
	table.SetDelegate(del)

	u.table = table

	scroll := appkit.NewScrollView()
	scroll.SetTranslatesAutoresizingMaskIntoConstraints(false)
	scroll.SetDocumentView(table)
	scroll.SetHasVerticalScroller(true)
	scroll.WidthAnchor().ConstraintGreaterThanOrEqualToConstant(220).SetActive(true)
	return scroll
}

func (u *ui) buildDetail() appkit.View {
	// Action buttons (Start / Stop / Restart / Help) with SF Symbol prefixes.
	start := iconButton("Start", "play.fill", func() { u.act("start") })
	stop := iconButton("Stop", "stop.fill", func() { u.act("stop") })
	restart := iconButton("Restart", "arrow.clockwise", func() { u.act("restart") })
	shell := iconButton("Shell", "terminal", func() { u.openShell() })
	help := iconButton("Help", "questionmark.circle", func() { u.showHelp() })
	u.startBtn, u.stopBtn, u.restartBtn, u.shellBtn = start, stop, restart, shell
	// Disabled until a container is selected; updateButtons() sets them.
	start.SetEnabled(false)
	stop.SetEnabled(false)
	restart.SetEnabled(false)
	shell.SetEnabled(false)

	// Logs / Config toggle.
	tabs := appkit.NewSegmentedControl()
	tabs.SetSegmentCount(2)
	tabs.SetLabelForSegment("Logs", 0)
	tabs.SetLabelForSegment("Config", 1)
	tabs.SetSegmentStyle(appkit.SegmentStyleRounded)
	tabs.SetSelectedSegment(0)
	action.Set(tabs, func(objc.Object) { u.showTab(u.tabs.SelectedSegment()) })
	u.tabs = tabs

	// Clears the on-screen logs buffer for the selected container. Switching
	// away and back re-fetches the tail, so this is purely a view reset.
	clear := iconButton("Clear", "trash", func() { u.clearLogs() })

	// Magnifier button that toggles the find bar (same as ⌘F).
	find := iconButton("", "magnifyingglass", func() { u.toggleFind() })

	toolbar := appkit.StackView_StackViewWithViews([]appkit.IView{
		start, stop, restart, shell, help, tabs, clear, find,
	})
	toolbar.SetOrientation(appkit.UserInterfaceLayoutOrientationHorizontal)
	toolbar.SetSpacing(8)

	// Logs and config text views, each in its own scroll view.
	u.logsView, u.logsScroll = newTextView()
	u.configView, u.cfgScroll = newTextView()
	setText(u.configView, "Select a container to view its configuration.")
	logsPane := u.buildLogsPane()

	// Status bar.
	status := appkit.NewLabel("Ready")
	status.SetTextColor(appkit.Color_SecondaryLabelColor())
	u.status = status

	column := appkit.NewStackView()
	column.SetOrientation(appkit.UserInterfaceLayoutOrientationVertical)
	column.SetSpacing(8)
	column.SetTranslatesAutoresizingMaskIntoConstraints(false)
	column.SetEdgeInsets(foundation.EdgeInsets{Top: 8, Left: 8, Bottom: 8, Right: 8})
	column.AddArrangedSubview(toolbar)
	column.AddArrangedSubview(u.buildFindBar())
	column.AddArrangedSubview(logsPane)
	column.AddArrangedSubview(u.cfgScroll)
	column.AddArrangedSubview(status)

	// Scroll views should expand; toolbar, find bar and status stay compact.
	toolbar.SetContentHuggingPriorityForOrientation(appkit.LayoutPriorityDefaultHigh, appkit.LayoutConstraintOrientationVertical)
	status.SetContentHuggingPriorityForOrientation(appkit.LayoutPriorityDefaultHigh, appkit.LayoutConstraintOrientationVertical)

	u.cfgScroll.SetHidden(true)
	return column.View
}

func (u *ui) buildFindBar() appkit.View {
	field := appkit.NewSearchField()
	field.SetSendsSearchStringImmediately(true)
	field.SetSendsWholeSearchString(false)
	field.SetPlaceholderString("Find in buffer")
	action.Set(field, func(objc.Object) { u.runSearch(field.StringValue()) })
	u.findField = field

	// Enter → next match, Shift+Enter → previous. In the field editor Return
	// sends insertNewline: and Shift+Return sends insertBacktab:.
	fd := &appkit.SearchFieldDelegate{}
	fd.SetControlTextViewDoCommandBySelector(func(_ appkit.Control, _ appkit.TextView, sel objc.Selector) bool {
		switch sel.Name() {
		case "insertNewline:":
			u.stepMatch(1)
			return true
		case "insertBacktab:":
			u.stepMatch(-1)
			return true
		}
		return false
	})
	field.SetDelegate(fd)

	prev := iconButton("", "chevron.up", func() { u.stepMatch(-1) })
	next := iconButton("", "chevron.down", func() { u.stepMatch(1) })

	count := appkit.NewLabel("")
	count.SetTextColor(appkit.Color_SecondaryLabelColor())
	u.findCount = count

	done := appkit.NewButtonWithTitle("Done")
	action.Set(done, func(objc.Object) { u.hideFind() })

	bar := appkit.StackView_StackViewWithViews([]appkit.IView{field, prev, next, count, done})
	bar.SetOrientation(appkit.UserInterfaceLayoutOrientationHorizontal)
	bar.SetSpacing(6)
	bar.SetContentHuggingPriorityForOrientation(appkit.LayoutPriorityDefaultHigh, appkit.LayoutConstraintOrientationVertical)
	bar.SetHidden(true)
	u.findBar = bar
	return bar.View
}

// arrowCursorButtonClass is an NSButton subclass that forces the arrow cursor
// while the mouse is over it. The jump button floats over the logs NSTextView,
// whose I-beam cursor is driven by its own tracking areas and takes precedence
// over plain cursor rects — so we override cursorUpdate:/mouseEntered: and set
// the arrow explicitly, backed by a tracking area covering the button.
var arrowCursorButtonClass = sync.OnceValue(func() objc.Class {
	c := objc.AllocateClass(objc.GetClass("NSButton"), "IoioioArrowCursorButton", 0)
	setArrow := func(self objc.Object) { appkit.Cursor_ArrowCursor().Set() }
	objc.AddMethod(c, objc.Sel("cursorUpdate:"), func(self, event objc.Object) { setArrow(self) })
	objc.AddMethod(c, objc.Sel("mouseEntered:"), func(self, event objc.Object) { setArrow(self) })
	objc.AddMethod(c, objc.Sel("mouseMoved:"), func(self, event objc.Object) { setArrow(self) })
	objc.RegisterClass(c)
	return c
})

func newArrowCursorButton() appkit.Button {
	obj := objc.Call[objc.Object](arrowCursorButtonClass(), objc.Sel("alloc"))
	b := appkit.ButtonFrom(objc.Call[objc.Object](obj, objc.Sel("init")).Ptr())
	// CursorUpdate | MouseEnteredAndExited | MouseMoved | ActiveAlways | InVisibleRect.
	const opts = appkit.TrackingAreaOptions(0x04 | 0x01 | 0x02 | 0x80 | 0x200)
	ta := appkit.NewTrackingAreaWithRectOptionsOwnerUserInfo(foundation.Rect{}, opts, obj, foundation.Dictionary{})
	b.AddTrackingArea(ta)
	return b
}

// buildLogsPane wraps the logs scroll view in a container and overlays the
// floating "jump to bottom" button on top of it. The button is a sibling of
// the scroll view (not a scroll-view subview, which NSScrollView tiles away),
// pinned to the container's bottom-right so it stays put as logs scroll. It
// starts hidden and is shown whenever the user scrolls away from the tail.
func (u *ui) buildLogsPane() appkit.View {
	container := appkit.NewView()
	container.SetTranslatesAutoresizingMaskIntoConstraints(false)

	u.logsScroll.SetTranslatesAutoresizingMaskIntoConstraints(false)
	container.AddSubview(u.logsScroll)
	u.logsScroll.TopAnchor().ConstraintEqualToAnchor(container.TopAnchor()).SetActive(true)
	u.logsScroll.BottomAnchor().ConstraintEqualToAnchor(container.BottomAnchor()).SetActive(true)
	u.logsScroll.LeadingAnchor().ConstraintEqualToAnchor(container.LeadingAnchor()).SetActive(true)
	u.logsScroll.TrailingAnchor().ConstraintEqualToAnchor(container.TrailingAnchor()).SetActive(true)

	b := newArrowCursorButton()
	if img := appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("chevron.down.circle.fill", "Jump to bottom"); !img.IsNil() {
		cfg := appkit.ImageSymbolConfiguration_ConfigurationWithPointSizeWeight(28, appkit.FontWeightRegular)
		b.SetImage(img.ImageWithSymbolConfiguration(cfg))
		b.SetImagePosition(appkit.ImageOnly)
	} else {
		b.SetTitle("▼")
	}
	// Borderless so the filled-circle symbol reads as the button, with no extra
	// bezel around it.
	b.SetBordered(false)
	b.SetToolTip("Jump to the latest logs")
	b.SetTranslatesAutoresizingMaskIntoConstraints(false)
	b.SetHidden(false) // DEBUG: force visible
	action.Set(b, func(objc.Object) { u.jumpToBottom() })

	container.AddSubview(b)
	b.WidthAnchor().ConstraintEqualToConstant(44).SetActive(true)
	b.HeightAnchor().ConstraintEqualToConstant(44).SetActive(true)
	b.TrailingAnchor().ConstraintEqualToAnchorConstant(container.TrailingAnchor(), -16).SetActive(true)
	b.BottomAnchor().ConstraintEqualToAnchorConstant(container.BottomAnchor(), -16).SetActive(true)
	u.jumpBtn = b
	u.logsPane = container
	return container
}

// clearLogs empties the on-screen logs buffer for the selected container. The
// stream keeps running, so new lines still arrive; switching away and back
// re-fetches the tail.
func (u *ui) clearLogs() {
	u.logsView.SetString("")
	u.jumpBtn.SetHidden(true)
}

// jumpToBottom scrolls the logs view to the end and hides the jump button;
// subsequent streamed writes resume tailing because the view is now at bottom.
func (u *ui) jumpToBottom() {
	length := u.logsView.TextStorage().Length()
	u.logsView.ScrollRangeToVisible(foundation.Range{Location: uint64(length), Length: 0})
	u.jumpBtn.SetHidden(true)
}

// iconButton makes a bezeled button with an SF Symbol prefix and (optional)
// title, falling back to a title-only button when the symbol is unavailable.
func iconButton(title, symbol string, onClick func()) appkit.Button {
	b := appkit.NewButtonWithTitle(title)
	if img := appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription(symbol, title); !img.IsNil() {
		b.SetImage(img)
		b.SetImagePosition(appkit.ImageLeading)
	}
	action.Set(b, func(objc.Object) { onClick() })
	return b
}

// textFont is the monospaced font used by both text panes.
func textFont() appkit.Font {
	return appkit.Font_MonospacedSystemFontOfSizeWeight(11, appkit.FontWeightRegular)
}

// textAttrs is the attribute map (color + font) applied to text appended to the
// panes, so streamed/added runs stay visible instead of falling back to black.
func textAttrs() map[foundation.AttributedStringKey]objc.IObject {
	return map[foundation.AttributedStringKey]objc.IObject{
		foundation.AttributedStringKey("NSColor"): appkit.Color_TextColor(),
		foundation.AttributedStringKey("NSFont"):  textFont(),
	}
}

func newTextView() (appkit.TextView, appkit.ScrollView) {
	const big = 1e7

	textColor := appkit.Color_TextColor()

	tv := appkit.NewTextViewWithFrame(foundation.Rect{Size: foundation.Size{Width: 400, Height: 300}})
	tv.SetEditable(false)
	// Read-only but still selectable, so text can be highlighted and copied
	// (⌘C via the Edit menu's copy: item).
	tv.SetSelectable(true)
	tv.SetRichText(false)
	tv.SetFont(textFont())
	// Draw on the standard text background with the dynamic label color so the
	// text stays legible in both light and dark mode.
	tv.SetDrawsBackground(true)
	tv.SetBackgroundColor(appkit.Color_TextBackgroundColor())
	tv.SetTextColor(textColor)
	tv.SetInsertionPointColor(textColor)
	// Typing attributes drive the color/font of text appended later; the keys
	// are the raw NSAttributedString attribute names.
	tv.SetTypingAttributes(textAttrs())

	// Standard scrollable-text-view geometry: the view grows vertically as text
	// is added, tracks the scroll view's width, and lays glyphs into a container
	// that follows that width. Without this the container has no usable size and
	// text is set but never displayed.
	tv.SetMinSize(foundation.Size{Width: 0, Height: 0})
	tv.SetMaxSize(foundation.Size{Width: big, Height: big})
	tv.SetVerticallyResizable(true)
	tv.SetHorizontallyResizable(false)
	tv.SetAutoresizingMask(appkit.ViewWidthSizable)
	tv.TextContainer().SetWidthTracksTextView(true)

	scroll := appkit.NewScrollView()
	scroll.SetTranslatesAutoresizingMaskIntoConstraints(false)
	scroll.SetDocumentView(tv)
	scroll.SetHasVerticalScroller(true)
	scroll.SetHasHorizontalScroller(false)
	scroll.SetBorderType(appkit.BezelBorder)
	return tv, scroll
}

// showTab toggles between the Logs (0) and Config (1) panes. Any active search
// is re-run against the newly shown buffer.
func (u *ui) showTab(segment int) {
	u.logsPane.SetHidden(segment != 0)
	u.cfgScroll.SetHidden(segment != 1)
	if !u.findBar.IsHidden() {
		u.runSearch(u.findField.StringValue())
	}
}

// toggleFind shows or hides the find bar (⌘F / magnifier button).
func (u *ui) toggleFind() {
	if u.findBar.IsHidden() {
		u.findBar.SetHidden(false)
		u.window.MakeFirstResponder(u.findField)
		u.runSearch(u.findField.StringValue())
	} else {
		u.hideFind()
	}
}

func (u *ui) hideFind() {
	u.findBar.SetHidden(true)
	u.matches = nil
	u.findCount.SetStringValue("")
	u.window.MakeFirstResponder(u.activeTextView())
}

// runSearch finds every case-insensitive occurrence of query in the active
// buffer and jumps to the first match.
func (u *ui) runSearch(query string) {
	u.matches = u.matches[:0]
	if query == "" {
		u.findCount.SetStringValue("")
		return
	}

	tv := u.activeTextView()
	hay := strings.ToLower(tv.String())
	needle := strings.ToLower(query)

	// Offsets must be UTF-16 code-unit indices to line up with NSString ranges.
	from := 0
	for {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		start := from + i
		loc := utf16Len(hay[:start])
		length := utf16Len(hay[start : start+len(needle)])
		u.matches = append(u.matches, foundation.Range{Location: uint64(loc), Length: uint64(length)})
		from = start + len(needle)
	}

	if len(u.matches) == 0 {
		u.findCount.SetStringValue("Not found")
		return
	}
	u.matchIdx = 0
	u.focusMatch()
}

// stepMatch moves to the next (dir=1) or previous (dir=-1) match, wrapping.
func (u *ui) stepMatch(dir int) {
	if len(u.matches) == 0 {
		return
	}
	u.matchIdx = (u.matchIdx + dir + len(u.matches)) % len(u.matches)
	u.focusMatch()
}

// focusMatch selects the current match, scrolls it into view and flashes the
// native find indicator over it.
func (u *ui) focusMatch() {
	if len(u.matches) == 0 {
		return
	}
	r := u.matches[u.matchIdx]
	tv := u.activeTextView()
	tv.SetSelectedRange(r)
	tv.ScrollRangeToVisible(r)
	tv.ShowFindIndicatorForRange(r)
	u.findCount.SetStringValue(fmt.Sprintf("%d of %d", u.matchIdx+1, len(u.matches)))
}

// utf16Len returns the number of UTF-16 code units in s, matching how NSString
// measures ranges.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// onSelect starts streaming the newly selected container's logs and refreshes
// its config pane.
func (u *ui) onSelect(rowIndex int) {
	if rowIndex < 0 || rowIndex >= len(u.rows) || u.rows[rowIndex].isGroup {
		return
	}
	c := u.rows[rowIndex].container
	u.selectedID = c.ID
	u.updateButtons()

	u.startLogs(c)
	go u.loadConfig(c)
}

func (u *ui) startLogs(c core.Container) {
	if u.logsCancel != nil {
		u.logsCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.logsCancel = cancel

	u.logsView.SetString("")
	u.jumpBtn.SetHidden(true)
	u.setStatus(fmt.Sprintf("Streaming logs for %s/%s…", c.Project, c.Name))

	w := &textViewWriter{tv: u.logsView, scroll: u.logsScroll, jumpBtn: u.jumpBtn, attrs: textAttrs()}
	go func() {
		_ = u.docker.StreamLogs(ctx, c.ID, w)
	}()
}

func (u *ui) loadConfig(c core.Container) {
	text, err := u.docker.InspectConfigText(context.Background(), c.ID, c.Project, c.Name)
	if err != nil {
		u.setStatus("Error inspecting container: " + err.Error())
		return
	}
	dispatch.MainQueue().DispatchAsync(func() {
		setText(u.configView, text)
	})
}

// setText replaces a text view's whole content with a colored attributed
// string, so it renders with the correct foreground color and font.
func setText(tv appkit.TextView, s string) {
	tv.TextStorage().SetAttributedString(foundation.NewAttributedStringWithStringAttributes(s, textAttrs()))
}

// act runs a start/stop/restart operation on the selected container.
func (u *ui) act(op string) {
	id := u.selectedID
	if id == "" {
		u.setStatus("No container selected.")
		return
	}

	// Disable the action buttons until the operation finishes.
	u.actionInFlight = true
	u.updateButtons()

	go func() {
		ctx := context.Background()
		var err error
		switch op {
		case "start":
			err = u.docker.Start(ctx, id)
		case "stop":
			err = u.docker.Stop(ctx, id)
		case "restart":
			err = u.docker.Restart(ctx, id)
		}

		dispatch.MainQueue().DispatchAsync(func() {
			u.actionInFlight = false
			u.updateButtons()
		})

		if err != nil {
			u.setStatus(fmt.Sprintf("Error (%s): %v", op, err))
			return
		}
		u.setStatus(fmt.Sprintf("Container %s: %s ok.", id[:12], op))

		// start/restart give the container a new process, so the existing log
		// follow points at the old (now-dead) stream. Re-attach to the fresh
		// one so logs keep flowing without a manual reselect.
		if op == "start" || op == "restart" {
			dispatch.MainQueue().DispatchAsync(func() {
				if i := u.selectedRow(); i >= 0 {
					u.startLogs(u.rows[i].container)
				}
			})
		}

		u.reload()
	}()
}

// openShell opens an interactive shell inside the selected container in the
// user's default terminal application. It runs `docker exec` eagerly launching
// bash, falling back to sh when bash isn't present in the image.
//
// The command is written to a temporary executable .command script which is
// then handed to `open`, so it launches in whatever terminal is registered as
// the handler for .command files (Terminal.app, iTerm, …) rather than a
// hard-coded app.
func (u *ui) openShell() {
	i := u.selectedRow()
	if i < 0 {
		u.setStatus("No container selected.")
		return
	}
	c := u.rows[i].container

	// Inside the container: prefer bash, fall back to sh. -it gives an
	// interactive TTY. Using the container ID avoids name-collision ambiguity.
	inner := fmt.Sprintf(
		"docker exec -it %s sh -c 'command -v bash >/dev/null 2>&1 && exec bash || exec sh'",
		c.ID)
	script := fmt.Sprintf("#!/bin/sh\nexec %s\n", inner)

	f, err := os.CreateTemp("", "ioioio-shell-*.command")
	if err != nil {
		u.setStatus("Error opening shell: " + err.Error())
		return
	}
	name := f.Name()
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		u.setStatus("Error opening shell: " + err.Error())
		return
	}
	f.Close()
	if err := os.Chmod(name, 0o700); err != nil {
		u.setStatus("Error opening shell: " + err.Error())
		return
	}

	go func() {
		if err := exec.Command("open", name).Run(); err != nil {
			u.setStatus("Error opening shell: " + err.Error())
			return
		}
		u.setStatus(fmt.Sprintf("Opened shell for %s/%s in terminal.", c.Project, c.Name))
	}()
}

// selectedRow returns the row index of the currently selected container, or -1.
func (u *ui) selectedRow() int {
	for i, r := range u.rows {
		if !r.isGroup && r.container.ID == u.selectedID {
			return i
		}
	}
	return -1
}

// reload fetches the current containers and rebuilds the sidebar rows on the
// main thread.
func (u *ui) reload() {
	containers, err := u.docker.List(context.Background())
	if err != nil {
		u.setStatus("Error listing containers: " + err.Error())
		return
	}
	projects := core.GroupByProject(containers, os.Args[1:])

	rows := make([]row, 0)
	for _, p := range projects {
		rows = append(rows, row{isGroup: true, project: p.Name})
		for _, c := range p.Containers {
			rows = append(rows, row{container: c})
		}
	}

	dispatch.MainQueue().DispatchAsync(func() {
		u.rows = rows
		u.table.ReloadData()
		u.restoreSelection()
	})
}

// restoreSelection re-highlights the currently selected container after a
// ReloadData (which clears the table selection). When nothing is selected yet
// it selects the first container and starts streaming it.
func (u *ui) restoreSelection() {
	target := u.selectedID
	if target == "" {
		// First load: pick the first container and stream it for real.
		for i, r := range u.rows {
			if !r.isGroup {
				u.table.SelectRowIndexesByExtendingSelection(foundation.NewIndexSetWithIndex(uint(i)), false)
				u.onSelect(i)
				return
			}
		}
		return
	}

	// Re-highlight the already-selected container without re-triggering the
	// selection handler (its logs are already streaming), then refresh the
	// action buttons in case the container's state changed.
	for i, r := range u.rows {
		if !r.isGroup && r.container.ID == target {
			u.restoringSelection = true
			u.table.SelectRowIndexesByExtendingSelection(foundation.NewIndexSetWithIndex(uint(i)), false)
			u.restoringSelection = false
			u.updateButtons()
			return
		}
	}
}

// watch polls Docker and refreshes the sidebar whenever a container's state
// changes.
func (u *ui) watch() {
	prev := map[string]string{}
	for range time.Tick(time.Second) {
		containers, err := u.docker.List(context.Background())
		if err != nil {
			continue
		}
		changed := len(containers) != len(prev)
		for id, c := range containers {
			if prev[id] != c.State {
				changed = true
			}
		}
		if !changed {
			continue
		}
		next := map[string]string{}
		for id, c := range containers {
			next[id] = c.State
		}
		prev = next
		u.reload()
	}
}

func (u *ui) setStatus(msg string) {
	dispatch.MainQueue().DispatchAsync(func() {
		u.status.SetStringValue(msg)
	})
}

func (u *ui) showHelp() {
	alert := appkit.NewAlert()
	alert.SetMessageText("ioioio — Legend & Actions")
	alert.SetInformativeText(`Status indicators:
● green  — running
● red    — exited
● yellow — paused
● purple — restarting
● blue   — created
● gray   — other

Actions (toolbar):
Start / Stop / Restart act on the selected container.
Shell opens an interactive shell (bash, or sh) inside the
container in your default terminal.
Logs / Config toggles the detail pane.

Pass project names as arguments to filter the sidebar:
    ioioio-mac project-a project-b`)
	alert.RunModal()
}

// textViewWriter appends streamed log bytes to an NSTextView on the main thread.
// It parses ANSI SGR color escapes into per-run foreground colors and appends
// each run as an *attributed* string carrying that color and the monospaced
// font — a plain AppendString would inherit no color and render black
// (invisible in dark mode), and the raw escape bytes would show as garbage.
type textViewWriter struct {
	tv     appkit.TextView
	scroll appkit.ScrollView
	// jumpBtn is revealed when a batch is appended while the user is scrolled up
	// away from the tail, and hidden once they're following the bottom again.
	jumpBtn appkit.Button
	attrs   map[foundation.AttributedStringKey]objc.IObject
	ansi    ansiParser
}

func (w *textViewWriter) Write(p []byte) (int, error) {
	runs := w.ansi.feed(string(p))
	if len(runs) == 0 {
		return len(p), nil
	}
	dispatch.MainQueue().DispatchAsync(func() {
		// Only follow the tail if the user was already at the bottom before this
		// batch is appended; if they scrolled up to read, leave their view put.
		atBottom := w.atBottom()
		storage := w.tv.TextStorage()
		for _, r := range runs {
			attrs := w.attrs
			if r.fg {
				attrs = map[foundation.AttributedStringKey]objc.IObject{
					foundation.AttributedStringKey("NSColor"): r.color,
					foundation.AttributedStringKey("NSFont"):  textFont(),
				}
			}
			storage.AppendAttributedString(foundation.NewAttributedStringWithStringAttributes(r.text, attrs))
		}
		if atBottom {
			w.tv.ScrollRangeToVisible(foundation.Range{Location: uint64(storage.Length()), Length: 0})
		}
		// Show the jump-to-bottom affordance exactly while the user is away from
		// the tail; hide it once following resumes.
		w.jumpBtn.SetHidden(atBottom)
	})
	return len(p), nil
}

// atBottom reports whether the log view is scrolled to (or within a line of)
// the end of its content, so new output should keep the tail in view.
func (w *textViewWriter) atBottom() bool {
	visible := w.scroll.DocumentVisibleRect()
	docHeight := w.tv.Frame().Size.Height
	// One line of slack absorbs sub-pixel rounding and the case where content
	// is shorter than the viewport (nothing to scroll).
	const slack = 24
	return visible.Origin.Y+visible.Size.Height >= docHeight-slack
}
