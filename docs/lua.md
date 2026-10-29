# Lua configuration

GoRex reads `~/.config/gorex/init.lua`, or `GOREX_CONFIG`. The file is
read when the app starts and by `gorex config reload`; a syntax error
leaves the previous keymap and actions in place. `gorex config check`
prints the file's bindings, actions and modes, or the error.

## Keybindings

```lua
gorex.bind("ctrl+b>c", "tab.new")
gorex.bind("cmd+shift+l", "scratch")
gorex.unbind("ctrl+b>c")
```

Keys can be sequences joined by `>`; modifiers are `cmd`, `ctrl`, `alt`
(`opt`) and `shift`. Named keys include `enter`, `escape`, `tab`, `space`,
`backspace`, `delete`, arrows, `home`, `end`, `pageup`, `pagedown`, and
`f1`–`f12`.

## Modes

```lua
gorex.mode("resize", { exclusive = true, once = true })
gorex.bind("cmd+r", "mode.enter", { name = "resize" })
gorex.bind("resize/left", "pane.resize.left")
gorex.bind("resize/right", "pane.resize.right")
```

`exclusive` ignores bindings outside the mode; `once` leaves the mode
after its next action. `mode.exit` and `mode.exit_all` leave explicitly.
A binding's mode is written before the key: `resize/left`.

## Actions

```lua
gorex.action{
  name = "scratch",
  title = "Open Scratch Tab",
  args = { dir = "string", ratio = "number?" },
  run = function(ctx, args)
    gorex.invoke("tab.new", { dir = args.dir })
  end,
}
gorex.bind("cmd+shift+j", "scratch", { dir = "/tmp" })
```

Custom actions appear in the palette and in `gorex action run`. Argument
types are `string`, `number`, `integer`, `boolean` and `any`; a trailing
`?` makes one optional. `gorex.invoke` runs a built-in action by ID.

## Events

```lua
gorex.on("program_status_changed", function(ev)
  if ev.state == "blocked" then
    gorex.invoke("app.settings")
  end
end)
```

`program_status_changed` runs when a pane's program reports a new or
changed status (`OSC 7501`), or when a record goes away (`ev.state ==
"clear"`). Fields are `session`, `pane`, `state`, `id`, `kind`, `title`,
`msg`, `app`, `progress`, `program` and `dir`. Events run on the app's
thread after the poll that saw the change.

## App overrides

The app keeps its own overrides apart from Lua, in `shortcuts.json` beside
the settings:

```json
{"bindings":[{"key":"cmd+shift+j","action":"tab.new","args":{"dir":"/tmp"}}]}
```

They load after the Lua file, so they win over it. A binding whose key is
a menu shortcut takes that shortcut over: the menu item no longer answers
it, and the key reaches the terminal and the keymap (unless an exclusive
mode is active).

## Reload

```sh
gorex config check
gorex config reload
```
