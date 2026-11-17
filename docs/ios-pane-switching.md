# Rex Go iOS：Tab 内面板访问与每端独立查看记录

Status: draft（已按 Rex Beta 0.1.0 build 1987 与 Rex Go 0.1.0 build 1577 对照，并做去重审计）

关联：#521（跨端保留分屏布局，并在 Rex Go iOS 访问同组所有面板）、#518（跨主机 Space 分组）、#520（跨端保留 Tab 与面板名称）。

## 1. 背景

桌面上一个 Tab 可以拆成多个面板（例如「训练 / 日志 / GPU」），服务端 `layout` 保存完整的 split 树、方向、比例、面板名与 revision。Web 客户端已经能渲染这棵树并拖动分隔条。

GoRex 的 iOS 端目前只读 `session.view` 里的 `windows[].focused_block_id`，没有读取同一 Tab 的其他面板，也没有面板切换入口。结果是：

- 手机只能打开每个 Tab 的聚焦面板，其余面板不可达；
- 手机看不到同组还有哪些面板、各自叫什么、什么状态；
- 若把“切换面板”实现成 `session.focus_block`，会同时改掉桌面端的焦点（共享 `Tab.Focus`），违反 #521 第 5 条。

本文只解决“同一台机器上的终端面板”在 iOS 的访问，以及“查看哪个面板”不写共享布局。跨主机面板、浏览器/note 面板、另一台桌面的实时几何跟随不在本期范围，见 §7。

## 2. 现状核查（main）

| 能力 | 现状 | 位置 |
|---|---|---|
| 布局真源 | 服务端保存 `layout.Store`：`Tab.Root` 树、`Vertical`/`Ratio`、`Tab.Focus`、`Tab.Zoom`、`Rev` | `internal/layout/layout.go:64-173` |
| 布局写入 | 仅桌面 App（`tab.set`/`windows.set`）与布局命令；`layout.apply` 以 `Base` 做冲突校验 | `internal/rex/proto.go:99-115`、`internal/rex/layout_apply.go:27` |
| Rex API 暴露 | `session.view` 的 `layer.layout` 是 `{rect, split:{split_id,direction,ratio,before,after}}`；`blocks[]` 按布局顺序 | `internal/rexapi/model.go:159-171,558-604` |
| 面板操作 | `new_split / move_block / swap_blocks / resize_pane / focus_block / zoom_block / set_block_label / close_block` | `internal/rexapi/methods.go:173-188` |
| Web | 渲染嵌套 split、分隔条、比例；点击面板会 `session.focus_block`，并跟随主机焦点 | `web/src/window.ts:141,161-270`、`web/src/main.ts:217` |
| GoRex iOS | 只读 `windows[].focused_block_id` 与 `layers[].blocks`；按 window 列表/切换；无面板列表 | `ios/GoRex/Rex/SessionView.swift:73-110`、`ios/GoRex/Views/SessionSheet.swift:64-79` |
| iOS 视图同步 | `session_view_changed` → 重读 `session.view` | `ios/GoRex/Rex/Host.swift:634,720-745` |
| iOS 输入/尺寸 | 打开面板时 `own_block take` 并 `resize`；关闭时 `disown_block` | `ios/GoRex/Terminal/TerminalSession.swift:229-253,380-400` |

iOS 已经具备实现本期功能所需的数据：

- `SessionView.Window.tiled` 是该 Tab 全部 tiled 面板的 **block ID，按布局顺序**（`SessionView.swift:98`，服务端按 `w.blocks` 顺序生成，`internal/rexapi/model.go:255-300`）；本期重命名为 `panes`，不保留别名；
- 每个 block 的 label 在 `layers[].blocks[].label`；
- 每个 block 的 title/program/status 已由 `Host.describe` 拉取并被 `block_event` 更新（`Host.swift:734-741,760+`）；
- 服务端在会话 revision 变化时已推送事件，iOS 已能重读。

### 2.1 Rex 参考版本与结论

参考：

- 已安装 `Rex Beta.app` 0.1.0（build **1987**，`snapshot`，commit `6c4ecb4`），helper `rex 0.1.0-preview.19`，**API revision 14**；
- `~/Downloads/Rex.dmg` payload 是 0.1.0（build **1642**，`preview`，commit `a92aace`），比已安装版本旧。本文的行为核对以已安装的 1987 为准；若要与 dmg 严格对齐，需注意这 345 个 build 的差异。
- dmg 内 helper 的 `rex version` 在只读挂载下未取到（Gatekeeper/syspolicyd 持有镜像，命令超时）；API revision 以已安装的 helper 为准。
- dmg 只有 macOS 客户端与 Rex Server helper，**没有 Rex Go iOS 二进制**；手机端交互无法从 dmg 复核，本文按 Rex Go 的公开实测交互（§2.1.1）设计，不是从 dmg 逐像素移植。

从 Rex 二进制内嵌的 API 文档与字符串得到的三条事实：

1. **Rex 的 focus 是共享的会话状态，不是每客户端状态**：`session.focus_block` 的说明是 “Focus a block, and make its window the active window if it isn't.”；`session.focus_direction` / `focus_next_pane` / `focus_previous_pane` 都直接改 session 的 focus。若 iOS 用 `session.focus_block` 来“切换查看”，必然改掉桌面焦点与 active window。
2. **协议里没有 per-client view focus**：`client.list` / `client.inspect` 的 `ClientView` 只有 `client_id`、`principal`、`info`、`connected_at`、`connection_state`、`session_ids`、`actions`；没有“这个客户端正在看哪个 block/window”的字段。`session.attach` 的 `focused` 是尺寸归属/typist 语义，不是查看位置。
3. **Rex 客户端确实把导航状态存在本地**：macOS 侧有 `WorkspaceLastFocusedSessionStore`、`WorkspaceSessionOrderStore`、`WorkspaceOpenWindowsStore`，以及命令面板里的 `PaneNavigatorCommandPalettePage`（“go to anything pane block tab session…”）。Mac 上所有 pane 同屏可见，选中 pane 直接调共享 focus；手机一次只看一个 pane，才需要额外的本地“我在看哪个”记录。

结论：**“本地查看记录”不是第二套焦点模型，而是一个客户端缓存**。它不参与布局、不参与 active window、不与别的设备同步；服务端仍只有一个 truth：`layout` + `Tab.Focus`。Rex 把窗口/会话导航状态放本地，正是同一条分界线的先例。

### 2.1.1 Rex Go 手机端（外部证据）

dmg 里没有 iOS 二进制，但 Rex Go 的 alpha 已通过 TestFlight 分发，公开实测记录了界面（DevelopersIO，2026-10-31，`Rex Dev 0.1.0 (1577)`）：

- 一个 session 下可以有多个 Tab，每个 Tab 下多个 pane（用户确认；公开截图只展示了一个 Tab 打开的状态）。
- 一个 Tab 的多个 panel 以**横向滑动**切换；文章原话：“Switching panes is intuitive with a horizontal swipe”。
- 右上角显示**整张分屏布局的缩略图**，高亮当前 pane；文章截图说明：“The pane layout is shown at the top right and the pane switching buttons at the bottom”。
- 底部是一排**面板切换按钮**（程序图标，当前项高亮），另有 `+` 按钮；截图里 3 个 pane 对应 3 个按钮。
- 顶部是当前 Tab 的标题（程序 + 终端标题）与会话名（`gentle-moor ⌄`）；返回箭头回到上层列表，说明 Tab 有独立的列表轴 [INFERENCE]。
- 同一篇文章确认多主机（MacBook / Mac mini）各自 session 列表可切换，CLI 的 `rex client ls` 里 iPhone 以 `platform=ios` 出现。

结论：Rex Go 的模型是 **session → window(tab) → pane 两轴**，不是“一个 Tab 只能看一个 pane”。GoRex 的 iOS 不需要发明第三套呈现，直接按这个交互做：Tab 轴沿用现有 `SessionSheet`，pane 轴新增“缩略图 + 底部按钮 + 横向滑动”。缩略图需要读 `layer.layout` 的 rect/split，但**只读**，不写布局。

除文章与截图明确描述的部分外，手势阈值、底部按钮的排序算法、`+` 的确切语义在参考版本中未观察到；本文按 GoRex 现有实现与布局顺序推断（标 `[INFERENCE]`），实现时以可用设备复核。

### 2.2 去重审计（本设计不引入第二套）

| 关注点 | 已有真源/机制 | 本设计的处理 | 是否新增重复 |
|---|---|---|---|
| 布局结构、顺序、方向、比例 | `layout.Store`（服务端） | 只读 `session.view` | 否 |
| 焦点、键盘、active window | `Tab.Focus` / `active_window_id` | 不写；只在本地无记录时回退 | 否 |
| 面板集合 | `SessionView.Window.tiled`（已按布局顺序） | **重命名**为 `panes` 并更新全部调用点 | 否（不加别名） |
| 面板名称 | `blocks[].label` + `Host.title` 链 | 把 `Host.title` 抽出 block 参数，窗口标题/面板按钮共用 | 否（不写第二套回退） |
| 面板状态 | `Host.status([block])` | 直接复用 | 否 |
| 每设备查看位置 | 无 | 一个本地小缓存（`UserDefaults`） | 新增，但不是真源 |
| 持久化 | 现有 `UserDefaults` 用法、`SpaceStore` | 复用 `UserDefaults`；不新建同步体系 | 否 |
| Tab 轴 UI | 现有 `SessionSheet`（window 列表） | 保留；本期不改 | 否 |
| Pane 轴 UI | 无 | 缩略图 + 底部按钮 + 滑动，均来自同一 `layer.layout`/`panes` | 新增视图，但不是新数据源 |
| 协议/服务端 | Rex API | 零改动 | 否 |

明确否决的方案：

- **服务端 per-client `view_focus` 字段**：Rex 没有；会变成第二套焦点，还要定义冲突与清理。只有“换一台新手机也记得”才需要，本期不做。
- **`Window.panes` 与 `tiled` 并存**：重复 API，最终会漂移；直接重命名。
- **独立的名称/状态解析**：与 `Host.title`、`Host.status` 漂移；只抽公共函数。
- **把每个 pane 当伪 window 列表**：重复条目、重复 attach 风险，也违反“同组”语义。
- **另做一个面板列表页**：Rex Go 已给出“缩略图 + 底部条”两个互补入口；再加第三个列表只会重复。
- **iCloud 同步查看位置**：与“各端独立”相反。

## 3. 目标与非目标

### 目标

1. 在 session 的 Tab（window）轴下，iOS 列出当前 Tab 的全部 tiled 面板（缩略图 + 底部条）；当前面板显示名称，所有面板有状态标记与可访问名称（tooltip/VoiceOver）；Tab 轴沿用现有 window 列表。
2. 按 Rex Go 的交互提供 pane 切换：右上角布局缩略图（高亮当前面板）、底部面板按钮、横向滑动；切换后面板仍连接原会话，不创建新终端、不丢归属。
3. 切换只影响本机 iPhone 的显示；不改共享布局的 `Focus`/`Zoom`/`Root`/`Ratio`，也不强制桌面一起切。
4. 每台 iPhone 独立记住“每个 window 上次看的面板”，重开回到自己的面板。
5. 桌面布局变化（新增/关闭/重命名面板、比例变化）经现有 revision 同步到 iOS 列表；桌面结构保持不变。
6. 旋转、键盘弹出、全屏查看属于本地呈现；语义上不写布局。

### 非目标

- 不把桌面宫格作为可编辑的分屏布局搬到手机；右上角缩略图只读显示方向/比例，iOS 不做分隔条拖动。
- 在 iPhone 上编辑共享布局：关闭、重命名面板、拖动分隔条仍只在桌面；iOS 只新建一次 split（新增一个面板），与新 Tab 同级。
- 跨主机面板、浏览器/note 面板的完整呈现（§7）。
- 另一台 GoRex 桌面收养远端布局后的实时几何跟随（§7.3）。
- 改变多端共享 PTY 的输入/尺寸语义（§5.7 说明现状）。

## 4. 术语：layout focus 与 viewed block

| 概念 | 归属 | 作用 | 本期是否改变 |
|---|---|---|---|
| **layout focus** | 服务端 `layout.Tab.Focus` | 桌面键盘焦点、Tab 标题回退、split/move/close 的锚点、客户端 view 的 `focused_block_id`、active window 的锚点 | 不改 |
| **viewed block** | 每个客户端本地的一格缓存 | “我这一端上次看的是哪个 block” | iOS 新增；仅 UI 记忆 |

规则：

- viewed block 不是焦点、不是会话状态、不是布局字段；它没有服务端表示，也不参与 revision。
- iOS 的 viewed block **永远不写** `session.focus_block` / `session.zoom_block` / 任何 `LayoutCommand`。
- layout focus 只作为 iOS 的 fallback（本机没有记录或记录失效时）。
- 桌面端继续用 layout focus 作为自己的“正在看哪个面板”；Web 同屏显示全部面板，不需要 viewed block，其点击面板仍是显式远程操作（现状不改）。

## 5. 设计

### 5.1 数据来源与顺序

两件事分开：

1. **成员与顺序**：`SessionView.Window.tiled` 已经是该 Tab 全部 tiled 面板的 block ID，按布局顺序。本期把它**重命名**为 `panes`，并更新全部调用点（`Host.swift`、`HostListView.swift`、`Inbox.swift`），**不保留别名**：

```swift
var panes: [String] {
    layers?.first?.blocks?.map(\.block_id) ?? []
}
```

2. **几何**：为右上角缩略图与点击命中，解码 tiled layer 已有的 `layout`（与 Web 使用的同一份数据，不新增服务端字段）：

```swift
struct PaneLayout: Decodable {
    var rect: [Double]?          // [x, y, w, h]
    var block_id: String?
    var split: PaneSplit?
}
struct PaneSplit: Decodable {
    var direction: String        // "horizontal" | "vertical"
    var ratio: Double
    var before: PaneLayout
    var after: PaneLayout
}
```

- `layout` 只用于**画缩略图与命中**；成员、顺序、名称、状态仍来自 `blocks`/`panes`，不解析出第二个面板列表。
- 缩略图只画 `panes` 里的 block：遍历 `layout` 时，叶子若不在 `panes` 中（浮层、被过滤的面板）就跳过；保证缩略图与底部条永远是同一个集合，不会出现“图里有、按钮没有”。
- `rect` 缺失时按 `split.direction/ratio` 递归计算（纯函数，同 `assignRect` 的算法）；不要因为服务端省略 rect 就不画。
- 不新增 `Layer.kind`：rexapi 固定先输出 tiled layer、再输出 floating layers（`internal/rexapi/model.go:558-604`），`layers.first` 就是 tiled。只有将来把浮层也列进面板组时，才需要 `kind`。
- 浮层本期不列入面板组；画布 Tab（无 tiled layer）不产生缩略图与面板列表。

### 5.2 名称与状态

不写第二套回退链。把现有窗口标题里的“面板名”部分抽成一个函数，窗口标题与面板按钮共用（`Host.title` 现在的链是 `window.name` → `blockName(focused)` → 终端标题 → `window.label`）：

```swift
// One chain for both a window's title and a pane row.
func paneTitle(_ block: String) -> String?   // blockName → blocks[block].title → program
func title(_ w: SessionView.Window) -> String // w.name → paneTitle(resolved block) → w.label
```

名称优先级保持现状：

1. `blocks[].label`（用户命名，`blockName` 已过滤默认 `"shell"`，`SessionView.swift:87-95`）；
2. `host.blocks[block]?.title`（终端标题）；
3. 程序名。

面板按钮最后回退到 `"Terminal"`；窗口行仍回退到 `window.label`，与今天一致。

状态：对单个 block 调用现有 `host.status([block])`（`Host.swift:677-685`），呈现为与窗口行相同的 `ProgramStatusMark`。窗口行仍显示整窗最紧急状态，不新增聚合器。

### 5.3 viewed block 的本地缓存与解析

新增 `ios/GoRex/Rex/ViewedBlock.swift`（一个缓存，不是焦点系统）：

```swift
struct ViewedBlockKey: Hashable {
    let machine: String   // Host.spaceMachineKey：machine_id，Demo 时回退 host UUID
    let session: String
    let window: String
}

@MainActor
final class ViewedBlockStore {
    static let shared = ViewedBlockStore()  // UserDefaults；按 App 安装，不同步
    func block(for key: ViewedBlockKey) -> String?
    func set(_ block: String, for key: ViewedBlockKey)
    func forget(_ key: ViewedBlockKey)
}
```

- 存储位置：`UserDefaults`，key 形如 `viewedBlock.v1.<machine>.<session>.<window>`；与现有 `backgrounds` 等本地偏好同一机制（`TerminalSession.swift` 的 `UserDefaults` 用法）。不写 iCloud，因为需求是“各端独立”。
- 权威性：缓存只影响“先显示哪个”，**从不**决定 block 是否存在、名称、顺序或状态；这些全部来自 `session.view`。记录失效就丢弃。
- 解析顺序（打开某个 window 时）：
  1. 显式导航目标（通知/Inbox/深链传入的 block）；
  2. 本地 `ViewedBlockStore` 记录，且该 block 仍在该 window 的 `panes` 中；
  3. `window.focused_block_id`；
  4. `panes.first`。
- 写入时机：用户从缩略图/底部条/滑动选择，或窗口列表打开某个 window 后解析出 block。
- 失效：block 不在 `panes` 中即忽略并清理该条目；window 消失时清理该 window 条目（可在收到 revision 后懒清理）。
- key 的 `machine` 用 `spaceMachineKey`：首次连上、`/api/spaces` 尚未报告 `machine_id` 时是 `host:<UUID>`，之后会变成真实 `machine_id`。这种切换只会让旧 key 变成 miss，不会串设备；不做迁移，交给懒清理。

### 5.4 切换流程

所有 pane 入口（横向滑动、缩略图点击、底部按钮）都走同一个 `selectPane(block)`：

1. `ViewedBlockStore.set(block, key)`；
2. `switchTo(TerminalTarget(host:session:block:))` —— 现有实现先启动新 `TerminalSession`、滑入、再关闭旧流（`TerminalScreen.swift:281-330`）；
3. **不调用**任何布局命令（`session.focus_block`、`zoom_block`、`pane.*` 都不调用）。

Tab 切换不进 `selectPane`：仍走现有 window 列表/pill 手势，并各自保留自己的 viewed block。

注意：`switchTo` 目前的方向按 window 索引计算，面板间切换会得到 `+1`。实现时按面板在 `panes` 中的索引差决定滑动方向；索引相同（理论上不发生）则淡入。滑动到第一个/最后一个面板时停住，不循环到另一个 Tab——Rex Go 的滑动只在当前 Tab 内。

### 5.5 UI（按 Rex Go）

两层轴分开：

- **Tab 轴**：沿用现有 `SessionSheet` 的 window 列表与 pill 的 window 手势；不在其中展开面板行（避免与 pane 轴重复）。
- **Pane 轴**：当前 Tab 内两个入口，共用 `panes` 与 `selectPane`：
  1. **底部面板条**：每个 pane 一个程序图标按钮（`ProgramTile` 的 21×16 原生比例），当前项是一圈紧贴 tile 的淡色背景；按钮上叠加该 pane 的状态点（blocked 黄、error 红、done 蓝、working 小灰点）。`+` 在胶囊外单独一个圆钮。`accessibilityLabel` 用 `paneTitle(block)` + 状态。
  2. **双指滑动**：在终端页面上用双指朝上/下/左/右轻扫，切到该方向相邻的 pane（按 `PaneLayout` 的位置找，内容跟手：左扫切到右边的 pane，上扫切到下面的 pane）；该方向没有 pane 时不动，不跨 Tab。单指仍用于滚动、选择和点按。选中文本、鼠标捕获的全屏程序（vim/Claude Code）或切换进行中时，手势让位给终端。
- **布局缩略图**：没有单独的 panes icon。切换 pane 后，缩略图在工具栏右侧（ellipsis 下方）浮出一次，显示 `PaneLayout` 的 rect/split；当前 pane 用其程序 tile 的颜色，其余灰色。约 4 秒后自动淡出。缩略图只作指示，不接收点按和滑动，触摸穿过它落到终端；方向切换由终端页面的双指滑动完成（45° 锥形范围找最近的 pane）。当前格的颜色用 0.25s 淡入淡出。直接点底部条也可切换。切 Tab 不触发。
- **单 pane**：`panes.count == 1` 时隐藏 icon 与底部条，保持今天的面板；工具栏菜单的 Split Pane（与 ⌘D）仍是入口。
- **标题**：沿用现有 pill，显示当前 pane 的 `paneTitle`/程序与 Tab 名。**去掉标题里的 `ProgramTiles` 堆叠**（同组信息已由缩略图与底部条表达），只保留一个当前程序图标。
- **拆分入口**：当前 pane 就地拆出一个新 shell（`session.new_split`，`direction: horizontal`、`side: after`、`focus: false`），随后切到新面板，桌面布局焦点不变。三个入口共用同一实现：底部条胶囊外的 `+`、工具栏菜单的 Split Pane、外接键盘的 ⌘D（与桌面 Split Right 同键）。单面板窗口只有后两者。`+` 不再表示 New Tab。
- **底部安全区**：面板条在 KeyBar 之上；键盘出现时，它跟着键盘上移，KeyBar 仍在它下面。终端 `paneInset` 让光标保持在两者之上。

缩略图说明**位置/方向/比例**，底部条给出**顺序与切换**；两者来自同一份 `panes`/`PaneLayout`，不新增第二份面板数据。

### 5.6 同步与失效

- 服务端布局变化 → `session_view_changed` → iOS 重读 view（现有链路）。
- 缩略图与底部条随 `panes`/`PaneLayout` 更新：新增/关闭/重命名/重排/拖比例都会反映。
- 正在查看的 block 被关闭：
  - 数据流以 4000 结束（`TerminalSession.swift` 的 `onDrop`）；
  - 本期行为：停留在结束状态，不自动切 pane；返回列表可从同组其他面板重新打开。
- 正在查看的 block 被桌面移动到另一个 window：block 仍存在于 session，`holds` 会命中新 window；保持当前连接，缩略图/底部条按新的 window 归属显示。
- 桌面 zoom：`Tab.Zoom` 只影响桌面显示；iOS 仍可查看其他面板，不改变桌面 zoom。
- 打开/切换面板会调用现有 `host.see(session:block:)`，把该 block 的 done/error 记录标记为已读。这个“已读”是服务端共享状态（`Host.swift:688-705`），不是布局或焦点：用户确实看到了该面板，本期保留；若要求“手机看了也不清桌面的已读”，需要另做 per-client seen，不在本期。

### 5.7 共享输入与尺寸（现状语义，需在 PR 描述与帮助中写明）

本期不改变以下行为，但必须让评审与用户明确：

1. **多端可同时输入**：输入不被 owner 校验，服务端按写入串行排队（`internal/rex/session.go:661-697`、`internal/rexapi/input.go:95-135`）。iPhone 与桌面可以同时打字，字符交错，不会创建第二个 PTY。
2. **尺寸是共享的**：iOS 打开面板会 `own_block take`（`TerminalSession.swift:229-243`），网关替它持有该 session 的 size lock（`internal/rexapi/owners.go:107-136`），PTY 变成手机网格；桌面此期间的 resize 被忽略，关闭面板时 `disown_block` 后桌面尺寸恢复（`state.go:3269-3276`）。
3. 因此“切换面板”会让桌面终端发生**内容重排**（不是布局/分屏结构变化）。这是为了手机可读性而保留的现状。
4. 键盘弹出不 resize（`TerminalView.swift:361` 的注释与实现）；旋转会 `fit` → `resize`（`TerminalSession.swift:380-400`）。
5. 备选方案（后续，不在本期）：只读预览时跳过 `own_block`/`resize`，使用会话当前尺寸；用户首次输入时再取尺寸。需要单独的 UX 决策。

## 6. 代码改动清单

iOS（本期全部改动）：

- `ios/GoRex/Rex/SessionView.swift`：`tiled` 重命名为 `panes`（更新全部调用点）；新增只读 `PaneLayout`/`PaneSplit`，解码 tiled layer 的 `layout`；抽取 `paneTitle`。
- `ios/GoRex/Rex/ViewedBlock.swift`（新增）：`ViewedBlockKey`、`ViewedBlockStore`（本地缓存）。
- `ios/GoRex/Rex/Host.swift`：`paneTitle` 与现有 `title(_:)` 共用；不新增 API 调用。
- `ios/GoRex/Rex/PaneGeometry.swift`（新增）：`PaneCell`、`PaneRecord`、`paneCells`、`paneNeighbor`，无 UI 依赖。
- `ios/GoRex/Views/PaneSwitcher.swift`（新增）：`PaneMiniMap`（浮动图，仅作指示）、`PaneBar`（底部图标条与紧凑状态点）。
- `ios/GoRex/Views/TerminalScreen.swift`：打开时按 §5.3 解析；`selectPane`；页面级双指四方向滑动；pane 切换后浮出缩略图并自动淡出；接入底部条，并协调 KeyBar 的底部 inset。
- `ios/GoRex/Views/SessionSheet.swift`：保持 window/session 列表，不展开 pane 行。
- `ios/GoRexTests/PaneSwitcherTests.swift`（新增）：嵌套 split 几何、行 fallback、缺失 block、pane 步进边界、viewed block 持久化。

Go / Web：**无改动**。若后续做 §7.2，再改 `internal/rexapi`。

## 7. 边界与后续

### 7.1 本期已明确不做

- **跨主机面板**：`filterNode` 丢弃 `Host != ""` 的面板（`internal/rexapi/model.go:450-457`）；`GET /api/spaces` 只给 `machine_id + block_id`（`internal/rexapi/spaces.go:19-49`），没有 Tab、顺序、名称。因此一台机器上的 Tab 若混有其他主机的面板，iOS 无法重建“同组全部面板”。
- **浏览器 / note 面板**：没有会话，无法“连接原运行会话”，只能占位。
- **浮层与画布**：本期列表只含 tiled 面板；画布 Tab 不列。

### 7.2 跨主机面板（需要协议扩展，另立设计）

二选一：

1. 扩展 `/api/spaces`：按 Tab 给出 `remote_panes[{machine_id, block_id, session_id, label, index, ratio}]`；iOS 再直连对应机器取状态。
2. 新增 `gorex.tab_panes` 只读方法，由服务端合并本机与他机信息；需要桌面端 `hosts.json` 已有 machine 映射。

风险：跨主机面板的状态/名称由两台服务器各自维护，revision 不一致；需要定义合并与过期策略。

### 7.3 另一台桌面的几何跟随

远端桌面收养布局时已保留方向/比例/名称（`hosts.go:867-1010`），但之后只同步名称（`state.go:3319`），不同步比例/方向/顺序。若 #521 中的“换到其他电脑”要求实时镜像，需要新增布局 reconcile；与本地 zoom/focus/拖动冲突，建议独立设计。

## 8. 测试与验收

### 单元测试

- fixture JSON（三层嵌套 split + floats + 默认/自定义 label）→ `panes` 顺序、`paneTitle` 回退、`PaneLayout` 的 rect/split、单面板状态。
- `ViewedBlockStore`：无记录→focused；有记录且有效→记录；记录失效→focused；focused 缺失→first；持久化读写；清理。
- 切换 resolver 不调用任何网络方法（通过依赖注入或 spy 验证）。

### 手工 / 端到端（Mac mini + CuaDriver，按私有测试流程）

1. 桌面打开一个 Tab，拆成 3 个面板，分别命名；再开第二个 Tab（单面板或双面板）。
2. iPhone（模拟器或真机）连接同一 host：
   - 打开第一个 Tab：右上角缩略图与桌面形状一致，当前 pane 高亮；底部 3 个程序按钮，当前项高亮；
   - 点缩略图的另一格 / 底部另一个按钮 / 横向滑动，三种方式都切到对应 pane；
   - 切到第二个 Tab，再切回第一个：各自回到自己的 viewed block。
3. 在 iPhone 切换面板前后记录桌面 `layout.json` 的 `tabs[].focus`、`zoom`、`root` 的 `ratio/vertical` 与 `rev`：必须不变（`rev` 不因 iOS 查看而增加）。
4. iPhone 关闭重开 / 杀掉 App：每个 Tab 回到自己上次的面板；桌面的聚焦面板不变。
5. 桌面新增/关闭/重命名一个面板、拖动分隔条：iPhone 缩略图与底部条在 revision 后更新（形状/比例/名称/状态）；桌面结构不变。
6. 两端同时输入：都能写入同一会话（预期交错），不出现第二个 session。
7. 旋转 iPhone / 弹出键盘：不改布局；PTY 尺寸按 §5.7 变化并在断开后恢复。
8. 在 vim / Claude Code 中选中文本或启用鼠标捕获时横向滑动：不误切 pane；普通滚动/选择优先。

### 验收标准（对应 #521）

- [ ] 同组面板成员、顺序、方向、比例在 iOS 缩略图可见且与桌面一致；桌面结构保持。
- [ ] iPhone 一次显示一个面板，并能看到/切换到同组其他面板（缩略图、底部按钮、横向滑动）。
- [ ] 当前面板显示名称，底部条/缩略图对每个面板显示状态标记与可访问名称；连接原运行会话，不重复、不丢归属。
- [ ] 手机切换不改变桌面 layout focus / zoom / split 结构。
- [ ] 每个 Tab 独立记住自己的面板；桌面调整布局后 iOS 缩略图与底部条同步。

## 9. 分阶段

1. **Phase 1（本文）**：`panes` 重命名、`PaneLayout` 解码、缩略图、底部面板条、横向滑动、本地 viewed block 缓存与失效回退。无 Go/协议改动。
2. **Phase 2**：浮层/画布分组；pane 名称的更多呈现（如长按 tooltip）；Rex Go 交互细节对齐。
3. **Phase 3（另立设计）**：跨主机面板（§7.2）与另一台桌面几何跟随（§7.3）。

## 10. 风险与未决问题

1. **尺寸抢占**：打开面板即改桌面 PTY 尺寸，可能被误认为“手机改了我的布局”。需要文案/帮助说明，或实现“只读预览不取尺寸”。
2. **多端同时输入**：共享 PTY 是产品既有语义，但用户在手机上边看边敲可能打扰桌面操作；是否需要在 iPhone 显示“他人正在输入/共享中”提示，未决。
3. **面板关闭时自动切换**：可能打断用户阅读输出；备选是停留在结束画面并提供“同组其他面板”入口。
4. **手势冲突**：页面级横向滑动必须让位给文本选择、滚动和鼠标捕获的全屏程序；否则 vim/Claude Code 里会误切 pane。需要手势识别优先级测试。
5. **缩略图命中**：pane 多时格子很小，单格点击可能难；底部面板条是 44pt 的保底入口。
6. **本地记录与显式导航冲突**：通知/Inbox 打开特定 block 必须优先，需在实现中集中 resolver，避免各入口各写一套。
7. **缓存不是真源**：实现时禁止用缓存判断 block 存在/可见；每次以最新 `session.view` 校验，否则会出现“缩略图里有、服务端没有”的漂移。
8. **Rex Go 仍是 alpha**：参考版本 0.1.0 (1577)，无法确认它在滑动时是否调用 `session.focus_block`。本设计明确不调用，以满足 #521 的“不强制桌面”；若上游后来改为共享焦点，也不跟。
9. 是否把浮层也纳入“同组面板”需要产品确认。

## 11. 关联

- #521 跨端保留终端分屏布局，并在 Rex Go iOS 访问同组所有面板。
- #518 所有设备共用用途分组（已由 `SpaceStore` 承担，本设计只复用 `spaceMachineKey` 作为持久 key）。
- #520 跨端保留 Tab 与面板名称（名称数据已具备，展示属本期；跨端同步属 #520）。
- `docs/rex-api.md`：Rex API 的布局/空间/尺寸锁语义。
- `docs/ios-centralized-spaces.md`：iOS 中心化分组与 `machine_id` 约定。
- 本机参考：`Rex Beta.app` 0.1.0（build 1987，snapshot，commit `6c4ecb4`），helper 0.1.0-preview.19，API revision 14；`~/Downloads/Rex.dmg` 为 0.1.0（build 1642，preview，commit `a92aace`）。证据来自 bundle 内嵌的 API 文档与字符串，不来自 changelog。
- Rex Go 手机端：TestFlight `Rex Dev 0.1.0 (1577)`，界面与交互见 DevelopersIO 2026-10-31 的实测：<https://dev.classmethod.jp/en/articles/reona-superlogical-rex-beta-try/>（缩略图、底部面板条、横向滑动、多主机）。
