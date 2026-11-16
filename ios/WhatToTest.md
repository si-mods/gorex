# What to Test

A build's What to Test, per locale, is what its locale section here gained
since the last build that uploaded: `ios/scripts/test-notes` sets `en-US`
from `## en-US` and `zh-Hans` from `## zh-Hans`.

A pull request that changes what a tester sees adds one bullet to each
section, in the app's terms: what they get to try, not how it is built. A
bullet that reads like a commit subject is one testers cannot read, and the
`notes` job of `.github/workflows/ci.yml` fails it.

## en-US

- The mic in the key bar now taps your hand as its keys do, as your finger goes down.
- A terminal that loses its connection shows its note above the key bar, near the bottom, and the note moves up with the keyboard.
- No line hides behind the key bar or pane bar now; scroll back and lines fade out under them. The pane bar is compact again, and its + splits the pane you are in.
- The voice capsule's cancel and its red dot tap your hand too.

## zh-Hans

- 键盘栏的麦克风现在和旁边的按键一样，手指按下时就震动一下。
- 终端断线重连时，提示条显示在按键栏上方、贴近底部，并随键盘一起上移。
- 终端不再被按键栏和面板条挡住；往回滚动时，文字在栏下渐渐淡出。面板条恢复紧凑，它的 + 在当前面板就地拆分。
- 语音胶囊上的取消和红色圆点也同样震动一下。
