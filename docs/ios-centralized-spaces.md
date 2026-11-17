# Rex Go: 以 iOS（+ iCloud）为中心化控制端的跨主机 Space 同步设计

Status: approved (设计决策已确认). 阶段 1 至 3 已实现：阶段 1 见 #580，阶段 2 与 3 见 #581。

## 1. 背景与问题诊断

iOS 端（Rex Go）作为纯控制端，自身不运行本地 shell 或 PTY，所有终端窗口均运行在各个 Remote Hosts（MacBook、Mac Mini、Linux 服务器等）上。

但当前的跨主机 Space 实现（[internal/rexapi/spaces.go](../internal/rexapi/spaces.go)、[ios/GoRex/Rex/Spaces.swift](../ios/GoRex/Rex/Spaces.swift)、[ios/GoRex/Views/HostListView.swift](../ios/GoRex/Views/HostListView.swift)）采用的是**去中心化的内存临时拼图**，存在以下结构性缺陷：

1. **无中心化持久化真相源，桌面离线即分组崩塌**：
   - 每个 Host 各自通过 `GET /api/spaces` 汇报“我本地的某个 Space 引用了哪些其它 `machine_id` 的 `block_id`”。
   - iOS 端的 `CrossHost` 只在内存中将**当前在线**的 Host 拼在一起。一旦记录了跨主机分组的桌面端（如 MacBook）休眠或离线，`Host.crossHost` 变为 `nil`，iOS 瞬间丢失所有跨主机分组，其余在线 Remote Hosts（如 Linux）的窗口退化回各自孤立的 `Desktop terminals`。
2. **Space 以 `(host, session)` 为唯一键，多主机同名 Space 无法统一**：
   - `HostListView` 直接把每个在线主机的 `sessions` 平铺到顶部 strip。若两台主机都有 `Work`，顶部会分裂为 `Work · MacBook` 和 `Work · Mac Studio` 两个独立胶囊，无法作为同一个跨主机工作区管理。
3. **跨主机 `machine_id` 依赖桌面端 `hosts.json` 缓存**：
   - [internal/rexapi/model.go](../internal/rexapi/model.go) 中 `mp.machines[n.Host]` 依赖桌面端 `hosts.json` 的 `HostMachines`。若桌面端尚未记录某远端主机的 `machine`，该远端 `remote_block` 会被静默跳过。
4. **远端新建/孤立终端产生大量碎片 Space**：
   - 在远端主机上通过 CLI 或 iOS 直接新建的终端（落在 `gorex-desktop-terminals`），只要未被某个桌面 Space 引用，`CrossHost.isGone` 就为 `false`，导致顶部 strip 为每台主机不断冒出 `Desktop terminals · <Host>` 胶囊。
5. **iOS 无法主动定义与修改跨主机 Space**：
   - 由于 iOS 没有自己的 `rex` 服务端和 `layout.json`，用户在手机端新建 Space、跨主机归类窗口时，当前架构没有统一的持久化载体。

---

## 2. 设计目标与非目标

### 目标

- **控制端中心化**：以 iOS 端持久化的 `SpaceManifest` 作为跨主机 Space 分组的唯一中心依据（Source of Truth），通过 iCloud 在用户自己的 Apple 控制端设备（iPhone / iPad）间自动同步，**不依赖任何常开服务器或 Hub 主机**。
- **桌面端被动同步**：当桌面端（Mac / Linux / Windows GoRex）在线时，由控制端将相关的 Space 归属被动同步给桌面端（同时兼容从桌面端 `layout.json` 合并新变更）；桌面端离线时，iOS 仍保有完整 Space 分组，不影响其余在线 Remote Hosts 的展示与操作。
- **统一跨主机 Space 模型**：以全局 `space.id` 为一等公民，消除同名 Space 分裂与各 host 的 `Desktop terminals` 碎片胶囊。

### 非目标

- 不在 iOS 端运行本地 shell 或 PTY。
- 不引入自建云端中继或中心账号服务器（复用 iCloud + 现有 `tailcat` / Tailscale / `rexapi`）。
- 不破坏对老版本 `rexapi` 服务端的向后兼容（未升级的远端主机仍可作为普通主机接入并在中心 Manifest 中被分组）。

---

## 3. 核心数据模型：`SpaceManifest`

将“Space 分组目录”从单机 `layout.json` 中抽离，定义为一份独立的跨主机索引结构：

```json
{
  "version": 1,
  "revision": 42,
  "spaces": [
    {
      "id": "sp_01J...",
      "name": "Work",
      "color": "blue",
      "order": 0,
      "updated_at": 1763150000,
      "members": [
        {
          "machine_id": "9c63...",
          "block_id": "block:a1",
          "window_id": "win:1",
          "order": 0,
          "added_at": 1763149000
        },
        {
          "machine_id": "f4b7...",
          "block_id": "block:b2",
          "window_id": "win:9",
          "order": 1,
          "added_at": 1763149500
        }
      ]
    }
  ],
  "tombstones": [
    {
      "kind": "space",
      "id": "sp_old",
      "deleted_at": 1763140000
    },
    {
      "kind": "member",
      "machine_id": "9c63...",
      "block_id": "block:x9",
      "deleted_at": 1763145000
    }
  ]
}
```

### 关键设计点

1. **稳定锚点 `(machine_id, block_id)`**：
   - `machine_id` 为机器持久身份（GoRex client key hash，与 [docs/rex-api.md](rex-api.md) 一致），不随 IP、Tailscale 域名或主机别名变化。
   - `window_id` 作为辅助提示（同一窗口内多 pane 共享同一窗口归属，展示时按窗口去重）。
2. **Space 作为一等公民**：
   - 使用独立 `space.id`（UUID/ULID）作为主键，不再耦合 `(host.id, session.id)`。
   - 即使某个 Space 下的所有主机暂时离线，该 Space 的定义（名称、颜色、排序、成员引用）依然完整保留。
3. **Tombstones（墓碑记录）**：
   - 记录近期删除的 Space 或手动移出的 Member，防止离线桌面端重连或 iCloud 同步延迟时“复活”已删除项（保留窗口期如 7 天后定期清理）。

---

## 4. 中心化架构与同步机制

```text
                ┌───────────────────────────────────┐
                │       iCloud (KV / Drive)         │
                │   iPhone ◄─── 同步 ───► iPad      │
                └─────────────────▲─────────────────┘
                                  │
                                  ▼
┌───────────────────────────────────────────────────────────────────┐
│                   iOS 控制端 (SpaceStore)                         │
│        本地持久化 spaces-manifest.json（中心化分组依据）           │
└───────┬─────────────────────────┬─────────────────────────┬───────┘
        │ 被动同步 (Read/Push)     │ Read / Attach           │ Read / Attach
        ▼                         ▼                         ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│  Desktop Host │         │  Linux Host A │         │  Linux Host B │
│  (Mac / Win)  │         │  (Headless)   │         │  (Headless)   │
└───────────────┘         └───────────────┘         └───────────────┘
```

### 4.1 控制端本地持久化 + iCloud 同步（iOS 间）

1. **本地持久化**：
   - 在 iOS `Application Support/spaces-manifest.json` 原子持久化 `SpaceManifest`，App 启动即加载，不等待任何 Host 上线。
2. **iCloud 同步载体**：
   - 推荐优先使用 **`NSUbiquitousKeyValueStore`**（或 iCloud Drive 协调文件 `NSFileCoordinator`）：
     - `NSUbiquitousKeyValueStore` 延迟低、无需复杂数据库迁移，1 MB 容量上限对于纯 ID/元数据索引足够容纳数千个终端成员；
     - 需在 [ios/project.yml](../ios/project.yml) 中为 `dev.gorex.ios` 启用 iCloud Key-Value Store entitlement。
3. **多控制端冲突合并（LWW）**：
   - 以 `space.updated_at` 与 `tombstones` 为准做实体级 Last-Write-Wins 合并：
     - 若一端删除了 Space（有较新的 `deleted_at`），另一端未修改该 Space，则删除生效；
     - 成员归属以较新的 `added_at` / `deleted_at` 为准，保证同一 `(machine_id, block_id)` 最多只归属一个 Space。

### 4.2 控制端与 Remote Hosts 同步（桌面端被动同步）

#### A. Host -> iOS：发现与增量合并

每次 Host 连接成功并完成 `session.attach` 与 `GET /api/spaces` 后，执行增量合并（而非覆盖）：

1. **既有桌面 Space 导入**：
   - 若桌面端报告了一个尚未出现在 `SpaceManifest` 中的 Space（且不在 `tombstones` 内），则将其导入为新 Space，并把它的本地窗口与 `remote_blocks` 写入 `members`。
   - **修复 `machine_id` 缺失问题**：iOS 自己已直连所有已添加的 Remote Hosts 并知道每台的 `machine_id`，当桌面端因 `hosts.json` 未缓存而缺漏某 `remote_block` 时，控制端侧可直接基于已有的 `SpaceManifest` 补齐归属。
2. **未归类终端（Unassigned）统一处理**：
   - 在某台主机上通过 CLI / SSH 新建的终端（如落在 `gorex-desktop-terminals`）若不在任何 Space 的 `members` 中，**不再为每台主机生成 `Desktop terminals · <Host>` 胶囊**，而是统一汇总到一个虚拟的 **`Unassigned`（未分组）** 视图（或提示一键归入当前 Space）。
   - 当用户在 iOS **当前选中的 Space** 内点击“新建窗口 / 新建终端”时，控制端在创建成功后直接将该新终端的 `(machine_id, block_id)` 写入当前 Space 的 `members`。

#### B. iOS -> Desktop：桌面端被动同步

- **适用对象**：运行完整桌面 UI（有 `layout.json`）的主机（Mac / Linux / Windows Desktop）。对于纯无头服务器（Headless `gorex -server`），其本身不维护跨主机桌面布局，无需接收写回，仅由 iOS 控制端持有分组即可。
- **同步时机**：当桌面端在线且支持新能力 `gorex.spaces_sync` 时，iOS 在连上后比对 `revision` / 差异，将控制端发起的 Space 变更（重命名、新建 Space、把某窗口移入/移出 Space）下发给桌面端。
- **协议扩展（`internal/rexapi`）**：
  - 在 `welcome.features` 中新增 `gorex.spaces_sync`。
  - 新增 RPC 方法 `gorex.spaces_apply`（或 `PUT /api/spaces/manifest`），由桌面端将与自己相关的 Space 变更安全地应用到 `layout.Store`（复用现有 `tab.to_space` / workspace 重命名逻辑，避免直接覆盖桌面端正在编辑的 pane 树）。

### 4.3 离线容错与僵尸成员清理（GC）

1. **主机离线不删分组**：
   - 当某台主机处于 `.offline` 状态时，`SpaceManifest` 中属于该 `machine_id` 的 `members` 完整保留。
   - 该 Space 依然正常出现在顶部 strip，Space 内属于其它**在线**主机的窗口正常显示和交互；离线主机仅在对应区块显示离线状态或按设置折叠。
2. **安全垃圾回收（Safe GC）**：
   - **触发条件**：只有当某个 `machine_id` **当前在线**、且已经完成该主机所有 sessions 的 `session.attach` 视图拉取后，若 `SpaceManifest` 中的某个 `block_id` 已不在该主机的任何窗口中，才将其从 `members` 中移除。
   - 这样可以彻底避免主机暂时离线、网络抖动或刚重连尚未拉全视图时误删分组关系。

---

## 5. iOS UI 与交互调整（`HostListView.swift`）

1. **顶部 Space Strip**：
   - 数据源从 `store.hosts.flatMap(...)` 切换为 `SpaceStore.spaces`（按 `order` 排序）。
   - 仅当存在不属于任何 Space 的在线窗口时，在末尾（或固定入口）显示 `Unassigned` 胶囊。
   - 增加 **+** 按钮或长按菜单：支持新建 Space、重命名、调整颜色、删除 Space。
2. **Space 内窗口列表**：
   - 按主机分组渲染当前 Space 关联的所有**在线**主机窗口；
   - 窗口长按菜单增加 **“Move to Space…”**，可直接将任意远端主机上的窗口归入/移动到任意 Space（即使该窗口来自纯 Linux 服务器）。
3. **保留 `By Host` 模式**：
   - Settings 中的 `Grouping.hosts` 保持不变，作为按物理主机浏览的备选视图。

---

## 6. 分阶段实施路径

1. **Phase 1 — iOS 控制端本地持久化 + 合并引擎（核心止血）**：
   - 新增 `ios/GoRex/Rex/SpaceStore.swift`，实现 `SpaceManifest` 模型、本地 JSON 持久化、与各 Host `/api/spaces` 的增量合并逻辑、Safe GC 以及 `Unassigned` 聚合。
   - 改造 `HostListView.swift` 从 `SpaceStore` 读取 Space 列表，支持手机端新建/重命名/移动窗口到 Space。
   - **收益**：立刻解决“桌面离线丢分组”、“同名 Space 分裂”、“`Desktop terminals` 碎片化”三大核心痛点，且不依赖服务端发版。
2. **Phase 2 — iCloud 多控制端同步**：
   - 配置 `ios/project.yml` 的 iCloud entitlements，接入 `NSUbiquitousKeyValueStore` 同步 `SpaceManifest` 与 LWW 合并。
   - **收益**：iPhone 与 iPad 之间实时共享同一份跨主机 Space 分组。
3. **Phase 3 — 桌面端被动同步（`gorex.spaces_sync`）**：
   - 在 `internal/rexapi/` 与桌面端 `layout` 应用层增加 `gorex.spaces_apply` 能力，让 iOS 端对 Space 的调整也能反向同步回在线的桌面端。

---

## 7. 已确认决策（Confirmed Decisions）

1. **iCloud 同步载体选型**：选用 **`NSUbiquitousKeyValueStore`**（零配置、毫秒级同步，1 MB 上限足够纯元数据索引；通过 `tombstones` 定期清理控制体积）。
2. **同名 Space 的首次合并策略**：**默认按名称自动合并**为同一个跨主机 Space（首次接入多台桌面端时，同名 Space 归并至同一全局 `space.id`，后续也支持在 iOS 端手动重命名或拆分）。
3. **桌面端与 iOS 的冲突优先级**：采用 **时间戳最新者胜（LWW）**，基于实体级 `updated_at` / `added_at` / `deleted_at` 自动合并。
