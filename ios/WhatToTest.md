# What to Test

A build's What to Test, per locale, is what its locale section here gained
since the last build that uploaded: `ios/scripts/test-notes` sets `en-US`
from `## en-US` and `zh-Hans` from `## zh-Hans`.

A pull request that changes what a tester sees adds one bullet to each
section, in the app's terms: what they get to try, not how it is built. A
bullet that reads like a commit subject is one testers cannot read, and the
`web` job of `.github/workflows/ci.yml` fails it.

## en-US

- Tapping a notification opens its terminal, and the app stays open.
- The mic in the key bar now taps your hand as its keys do, as your finger goes down.
- A terminal that loses its connection shows its note above the key bar, near the bottom, and the note moves up with the keyboard.
- No line hides behind the key bar or pane bar now; scroll back and lines fade out under them. The pane bar is compact again, and its + splits the pane you are in.
- The voice capsule's cancel and its red dot tap your hand too.
- A window's title in the title menu keeps to one line, and the open window is marked with a check mark.
- Swipe with two fingers up, down, left or right in a terminal to move to the pane that way; one finger still scrolls. The layout map is now only an indicator.
- A terminal whose connection keeps dropping no longer gives up: it reconnects on its own until the session ends, and a refused stream is the only one that asks you to retry.
- A window's name too long for the header now ends in an ellipsis, and its program's tile stays in view.
- Long-pressing a terminal's title opens the session's sheet now, as a tap does: each window shows the tile of the program it runs, and New Tab and Switch Session are under the list.
- A window with a single pane can split it now: open the menu at the top right and choose Split Pane, or press ⌘D on a hardware keyboard. The new pane opens beside the one you were in.
- Dragging two fingers in a terminal now moves the panes with your fingers, up and down too; let go past a third of the way, or flick, to switch, or the pane springs back.
- A terminal that another computer's shared space lists now joins that space, whichever computer connects first. Terminals you moved yourself stay where you put them.
- Switching between a window's panes, with two fingers or the pane bar, now shows the pane at once: every pane of the window stays connected while its terminal is open.
- Connecting to a computer over Tailscale works as before. If a computer that used to connect no longer does, tell us.
- Adding a computer on the same Wi-Fi now needs the new QR code GoRex shows; if a computer you added before refuses you, scan its QR code again in Hosts.
- Drag a window in the list and let go of it away from the rows: the windows go back to their order at once, and the one you dragged no longer stays dimmed.
- Swiping with two fingers no longer switches panes, for now; tap a pane in the pane bar instead.
- Tapping a terminal's title now answers your finger: its glass lights up as you press, and your hand feels a tap as the session's sheet opens.
- The pane bar answers your finger too: its glass and the + light up as you press, and the pane you are in is outlined while the others fade.
- Tap a file path a program printed in a terminal, such as a screenshot Claude Code saved: the file downloads from the computer and opens in Quick Look, where you can zoom, share or save it.
- After a computer you added restarts GoRex, for example to update, Rex Go reaches it again about 3 seconds sooner.
- Switching panes in the pane bar no longer brings up the keyboard, even when you come back to a pane where you were typing. Tap the terminal to type.
- Close a pane you no longer need: open the menu at the top right and choose Close Pane, or press and hold its tile in the pane bar. The last pane of a window stays.
- Closing a pane, from the top-right menu or by long-pressing its icon in the pane bar, now asks first, because it ends the pane's program. Try Cancel and Close.

## zh-Hans

- 点通知栏的通知会打开它对应的终端，应用不再因此退出。
- 键盘栏的麦克风现在和旁边的按键一样，手指按下时就震动一下。
- 终端断线重连时，提示条显示在按键栏上方、贴近底部，并随键盘一起上移。
- 终端不再被按键栏和面板条挡住；往回滚动时，文字在栏下渐渐淡出。面板条恢复紧凑，它的 + 在当前面板就地拆分。
- 语音胶囊上的取消和红色圆点也同样震动一下。
- 标题菜单里的窗口标题只占一行，当前窗口前面标有勾号。
- 在终端里用双指上下左右轻扫，可切到那个方向的面板；单指仍用来滚动。布局小地图只作指示，不再响应点按和滑动。
- 连接反复断开的终端不再放弃：只要会话还在，它自己一直重连；只有服务端拒绝时才让你重试。
- 窗口名太长时，标题栏里会截断成一行，末尾是省略号，程序图标也不会再被挤出胶囊。
- 长按终端标题现在和点按一样打开会话的标签页列表：每个窗口都带着自己程序的图标，新建标签页和切换会话就在列表下面。
- 只有一个面板的窗口现在也能拆分了：点右上角的菜单选「拆分面板」，或在外接键盘上按 ⌘D；新面板开在你当前面板的旁边。
- 双指拖动终端时，面板现在跟着手指移动，上下切换也是上下滑；拖过约三分之一或快速一甩就切换，否则弹回原处。
- 别的电脑的共享空间列出的终端，现在会归入那个空间，不再取决于哪台电脑先连上；你自己移动过的终端仍留在原处。
- 在同一窗口的面板之间切换（双指轻扫或点面板条）现在立刻显示，不再重新连接：终端打开期间，窗口的每个面板都保持连接。
- 通过 Tailscale 连接电脑和以前一样。如果某台以前能连上的电脑现在连不上，请告诉我们。
- 在同一 Wi-Fi 下添加电脑，现在要扫 GoRex 显示的新二维码；以前添加过的电脑如果拒绝连接，在主机列表里重新扫它的二维码。
- 在列表里拖动窗口，在行外松手：窗口立刻回到原来的顺序，被拖的那一行也不再一直变暗。
- 双指滑动暂时不再切换面板；请点面板条里的面板来切换。
- 点终端顶部的标题现在有反馈：按下时玻璃会亮起，打开会话面板时手上有一下轻触。
- 面板条也有按下反馈：按下时玻璃和 + 会亮起；当前面板加了描边，其他面板变淡。
- 点按终端里程序打印的文件路径，例如 Claude Code 保存的截图：文件从电脑下载后在快速查看中打开，可以缩放、分享或存储。
- 你添加的电脑重启 GoRex（比如更新）之后，Rex Go 重新连上它大约快 3 秒。
- 用面板条切换面板时不再自动弹出键盘，回到之前打过字的面板也一样。要输入时点一下终端。
- 不再需要的面板可以关掉：点右上角菜单选“关闭面板”，或在面板条里长按它的图标。窗口的最后一个面板不会被关。
- 关闭面板（右上角菜单，或在面板条里长按图标）现在会先询问，因为这会结束面板中的程序。试试“取消”和“关闭”。
