# What to Test

A build's What to Test, per locale, is what its locale section here gained
since the last build that uploaded: `ios/scripts/test-notes` sets `en-US`
from `## en-US` and `zh-Hans` from `## zh-Hans`.

A pull request that changes what a tester sees adds one bullet to each
section, in the app's terms: what they get to try, not how it is built. A
bullet that reads like a commit subject is one testers cannot read, and the
`notes` job of `.github/workflows/ci.yml` fails it.

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
