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
gorex.bind("cmd+shift+n", "scratch", { dir = "/tmp" })
```

Custom actions appear in the palette and in `gorex action run`. Argument
types are `string`, `number`, `integer`, `boolean` and `any`; a trailing
`?` makes one optional. `gorex.invoke` runs a built-in action by ID.

## Reload

```sh
gorex config check
gorex config reload
```
