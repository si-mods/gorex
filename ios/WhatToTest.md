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

## zh-Hans

- 键盘栏的麦克风现在和旁边的按键一样，手指按下时就震动一下。
