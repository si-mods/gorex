package main

import (
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/rex"
	"gorex/locales"
)

// App is the state of a window of GoRex's, which shows a workspace.
type App struct {
	win *mygo.Window
	// winR is the radius macOS rounds the window's corners to, as the last
	// frame read it, in DIPs; 0 elsewhere, in full screen, and without a
	// window. Window.CornerRadius asks the main loop, so view reads it
	// once for the frame's cards.
	winR float64
	// pathStats is what stats found of the paths printed in panes of this
	// machine, which isLink answers from; the main thread's.
	pathStats map[string]pathStat
	// st is the app's store of workspaces, ws the one the window shows,
	// and slot the window's place among the windows.
	st        *store
	ws        *Workspace
	slot      int
	client    *rex.Client
	hello     rex.Hello
	hostTheme string
	// keys matches key presses to actions; it runs before the terminal
	// sees a key a binding takes. luaConfig owns the custom actions the
	// keymap and palette use.
	keys      *keyDispatcher
	luaConfig *config.Config
	custom    []*command
	err       string
	// errMsg is a message of the catalog, said when drawn, which wins
	// over err, a message of the server.
	errMsg func(locales.Messages) string
	// errNote is how the banner shows the message it was set with
	// (setNote): as a wait, or with a button.
	errNote *bannerNote
	// sheet is the New Project sheet, and the one that changes a
	// project's folder.
	sheet projectSheet

	tabs   []*Tab
	active int
	nextID int
	// hosts are the remote hosts saved, in their order.
	hosts []*hostConn
	// tailnetHosts are the GoRex hosts the tailnet showed, for the sheet
	// that adds a host, which saves none of them itself; the store's when
	// there is one, shared by the windows.
	tailnetHosts []rex.TailnetHost
	// spaces is the space picker's state and its dialogs', and pickerOpen
	// tells that the title bar's menu of projects shows.
	spaces     spaceUI
	pickerOpen bool
	// want is the host's project the window showed last, by openKey, to
	// show once the host's layout has it (takeWanted); "" for none.
	want string

	// focusReq asks the view to give the keyboard focus to a pane's
	// terminal.
	focusReq *Pane
	// canvasPan is set while Space, pressed over a canvas's empty plane,
	// is held: a drag anywhere pans it. canvasHover is the tab whose plane
	// the pointer is over, and canvasAt where it last pressed there, in the
	// room's DIPs.
	canvasPan   *Tab
	canvasHover *Tab
	canvasAt    [2]float32
	// floatRoom is the tab's room as the floats' drags last measured it.
	floatRoom ui.Rect
	// passes counts the builds of the tab's room: a float gliding that
	// one of them did not build went out of sight, and is at rest.
	passes uint64
	// lastTabID, lastTabIdx, and tabSlideDir track tab switches so the
	// tab content slides left or right in the direction of the switch.
	lastTabID   int
	lastTabIdx  int
	tabSlideDir float32
	tabPill     ui.Rect

	// The command palette, the host's popover and the tab being renamed.
	paletteOpen   bool
	paletteQuery  string
	paletteSel    int
	paletteSelID  any
	paletteList   paletteList
	hostOpen      bool
	renaming      *Tab
	renameText    string
	renameFocus   bool
	paneRenaming  *Pane
	paneNameText  string
	paneNameFocus bool
	// prompt is the question a config action asked, which this window
	// shows, with the answer being typed (docs/MOD-009-client-prompts.md).
	prompt       *rex.PromptEvent
	promptText   string
	promptPicked map[string]bool
	promptOther  string
	// promptSel is the row the keyboard is on, in a pick.
	promptSel int
	// contentFocus tells that a pane's content took the keyboard as the
	// active tab's panes were built, and pageHover the browser pane whose
	// page the pointer was over. A press on a page gives the page the
	// keyboard and clears the content's focus, which MyGo tells no other
	// way: the tab then takes the page's pane as the focused one.
	contentFocus bool
	pageHover    *Pane
	// findPane is the pane whose find bar shows, findText its query, and
	// findStatus how its search goes; lastFind reopens the bar.
	findPane   *Pane
	findText   string
	lastFind   string
	findStatus terminal.SearchStatus
	findFocus  bool
	saveDue    bool
	lastSave   time.Time
	quitting   bool
	focusedWin bool
	title      string
	// focusPresent tells whether the user was at this machine when the
	// focus was last claimed, and focusSampled when that was: a window
	// that kept the focus of an unattended machine claims no session.
	focusPresent bool
	focusSampled time.Time

	// The workspace's popover, the field that renames it, Close
	// Session's question.
	wsOpen     bool
	wsRenaming bool
	// wsColorOnly keeps the popover to the space's colors, as the host
	// view's Color… opens it.
	wsColorOnly   bool
	wsRenameText  string
	wsRenameFocus bool
	// wsColorText is the color the popover's custom field holds.
	wsColorText string
	wsConfirm   bool
	// wsShown tells that the open popover scrolled the window's project
	// into view; stripShown is the project the sidebar's strip last
	// scrolled into view.
	wsShown    bool
	stripShown *Workspace
	// teleportAsk is the question after a tab was dropped on another
	// host's place in the tabs.
	teleportAsk teleportAsk
	// closeAsk is the question before closing a tab or a pane that runs a
	// program; closeOthers the tab whose Close Other Tabs asks first.
	closeAsk        closeAsk
	closeOthers     *Tab
	closeOthersOpen bool
	// sideList is where the sidebar's list of tabs showed in the last
	// frame, which a tab's row scrolled out of leaves its close button
	// clipped.
	sideList ui.Rect
	// paletteFresh tells that the palette was just opened: the frame that
	// shows it first poses it where it comes in from.
	paletteFresh bool

	// The sidebar: the tab last scrolled into view, the tab last chosen
	// on each host by hostKey, the sessions under each host the host view
	// opened or closed, by spaceKey (spaceIsOpen), and a drag of its edge,
	// from the width it began at.
	revealed  *Tab
	chosenOn  map[string]*Tab
	spaceOpen map[string]bool
	edgeDrag  bool
	edgeFrom  float32
	edgeMoved float32
	// paneDrop is the drop zone a dragged pane was last shown over, which
	// a drop on that pane takes.
	paneDrop shownDrop
	// dividerDrag tells that the pointer holding a divider between panes
	// moved it in the frame before: the panes then follow it at once, with
	// no spring.
	dividerDrag bool
	// dividerHeld tells that the pointer held a divider between panes in
	// the frame before: the panes follow it, but their terminals keep the
	// size they had at rest until it is let go, so the program hears of
	// one size at the end rather than of each width the drag passed
	// through. dividerNow is what this frame's dividers found, which the
	// frame after reads as dividerHeld.
	dividerHeld bool
	dividerNow  bool
	// sidebarMoving tells that the sidebar or the bar over the panes
	// moved in the last frame: the terminals there hold their size.
	sidebarMoving bool

	// posted are changes to make in the next frame, from terminals of a
	// view without a window, as in tests.
	postMu sync.Mutex
	posted []func()
	// services keeps redraw requests available to menu callbacks.
	services ui.Services
}

func (a *App) post(fn func()) {
	a.postMu.Lock()
	a.posted = append(a.posted, fn)
	a.postMu.Unlock()
}

// updater returns what runs a change on the main thread, from any
// goroutine: in the window of now, as long as it is open.
func (a *App) updater() func(func()) {
	win := a.win
	return func(fn func()) {
		if win != nil {
			win.Update(fn)
		} else {
			a.post(fn)
		}
	}
}

// later changes the tree of tabs and panes once the frame building now
// is built, as closing what it is building would pull it from under it.
func (a *App) later(c *ui.Context, fn func()) {
	a.post(fn)
	c.Invalidate()
}

// runPosted makes the changes posted since the last frame.
func (a *App) runPosted() {
	a.postMu.Lock()
	fns := a.posted
	a.posted = nil
	a.postMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
