package main

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/iconpack"
	"gorex/internal/theme"
	"gorex/locales"
)

// iconPacksUI is the state of the Extensions page (the "icons" section):
// icon packs (docs/MOD-012-program-icon-packs.md) and theme packs
// (docs/DSK-008-theme.md).
var iconPacksUI struct {
	mu              sync.Mutex
	installed       []iconpack.Summary
	themes          []theme.Summary
	installedLoaded bool
	reg             *iconpack.RegistryIndex
	regLoaded       bool
	regLoading      bool
	regErr          string
	query           string
	installing      map[string]bool // by registryKey
	actionErr       string
	themeErr        string
}

func resetIconPacksUI() {
	iconPacksUI.mu.Lock()
	defer iconPacksUI.mu.Unlock()
	iconPacksUI.installed = nil
	iconPacksUI.themes = nil
	iconPacksUI.installedLoaded = false
	iconPacksUI.reg = nil
	iconPacksUI.regLoaded = false
	iconPacksUI.regLoading = false
	iconPacksUI.regErr = ""
	iconPacksUI.query = ""
	iconPacksUI.installing = nil
	iconPacksUI.actionErr = ""
	iconPacksUI.themeErr = ""
}

func (a *App) refreshInstalledIconPacks() {
	list, err := iconpack.ListPacks(iconpack.Dir())
	themes := theme.List(theme.Dir(config.Dir()))
	iconPacksUI.mu.Lock()
	iconPacksUI.installed = list
	iconPacksUI.themes = themes
	iconPacksUI.installedLoaded = true
	if err != nil {
		iconPacksUI.actionErr = err.Error()
	}
	iconPacksUI.mu.Unlock()
	reloadIconPacks(a.luaConfig)
}

func (a *App) fetchIconPackRegistry() {
	iconPacksUI.mu.Lock()
	if iconPacksUI.regLoading {
		iconPacksUI.mu.Unlock()
		return
	}
	iconPacksUI.regLoading = true
	iconPacksUI.regErr = ""
	iconPacksUI.mu.Unlock()

	win := settingsUI.win
	go func() {
		idx, err := iconpack.FetchRegistry(context.Background(), "")
		apply := func() {
			iconPacksUI.mu.Lock()
			iconPacksUI.regLoading = false
			iconPacksUI.regLoaded = true
			if err != nil {
				iconPacksUI.regErr = err.Error()
			} else {
				iconPacksUI.reg = idx
				iconPacksUI.regErr = ""
			}
			iconPacksUI.mu.Unlock()
		}
		if win != nil {
			win.Update(apply)
		} else {
			apply()
		}
	}()
}

// registryKey tells the entries of the registry apart: an icon pack and a
// theme may have the same ID.
func registryKey(rp iconpack.RegistryPack) string {
	if rp.Kind == "" {
		return rp.ID
	}
	return rp.Kind + "/" + rp.ID
}

// installIconPack installs a registry entry of either kind: an icon pack,
// which it then turns on, or a theme pack, as gorex pack install does;
// using the theme is the Window Theme group's choice.
func (a *App) installIconPack(rp iconpack.RegistryPack) {
	key := registryKey(rp)
	iconPacksUI.mu.Lock()
	if iconPacksUI.installing == nil {
		iconPacksUI.installing = map[string]bool{}
	}
	if iconPacksUI.installing[key] {
		iconPacksUI.mu.Unlock()
		return
	}
	iconPacksUI.installing[key] = true
	iconPacksUI.actionErr = ""
	iconPacksUI.mu.Unlock()

	win := settingsUI.win
	packsDir, themesDir := iconpack.Dir(), theme.Dir(config.Dir())
	go func() {
		var err error
		if rp.Kind == iconpack.KindTheme {
			err = iconpack.Checkout(context.Background(), rp, func(dir string) error {
				_, err := theme.Install(dir, themesDir, rp.ID)
				return err
			})
		} else {
			_, err = iconpack.InstallFromRegistry(context.Background(), rp, packsDir, "")
		}
		apply := func() {
			iconPacksUI.mu.Lock()
			delete(iconPacksUI.installing, key)
			if err != nil {
				iconPacksUI.actionErr = err.Error()
			}
			iconPacksUI.mu.Unlock()
			if err == nil {
				if rp.Kind != iconpack.KindTheme {
					a.setPref(func(s *settings) {
						if !slices.Contains(s.IconPacks, rp.ID) {
							s.IconPacks = append(s.IconPacks, rp.ID)
						}
					})
				}
				a.refreshInstalledIconPacks()
			}
		}
		if win != nil {
			win.Update(apply)
		} else {
			apply()
		}
	}()
}

// iconPacksSettings is the Extensions page: the window theme, the icon
// packs and the community registry of both kinds.
func (a *App) iconPacksSettings(c *ui.Context, k *colors) {
	iconPacksUI.mu.Lock()
	installedLoaded := iconPacksUI.installedLoaded
	regLoaded := iconPacksUI.regLoaded
	regLoading := iconPacksUI.regLoading
	installed := slices.Clone(iconPacksUI.installed)
	themes := slices.Clone(iconPacksUI.themes)
	reg := iconPacksUI.reg
	regErr := iconPacksUI.regErr
	actionErr := iconPacksUI.actionErr
	installing := make(map[string]bool, len(iconPacksUI.installing))
	for id, v := range iconPacksUI.installing {
		installing[id] = v
	}
	iconPacksUI.mu.Unlock()

	if !installedLoaded {
		a.refreshInstalledIconPacks()
		iconPacksUI.mu.Lock()
		installed = slices.Clone(iconPacksUI.installed)
		themes = slices.Clone(iconPacksUI.themes)
		iconPacksUI.mu.Unlock()
	}
	if !regLoaded && !regLoading {
		a.fetchIconPackRegistry()
	}

	a.themesGroup(c, k, themes)

	prio := 0
	if prefs.IconPackPriority == "osc" {
		prio = 1
	}
	settingsGroup(c, k, locales.SettingsIconPacksInstalled(), "", actionErr, func() {
		settingRow(c, k, locales.SettingsIconPackPriority(), "", func() {
			if ui.Segmented(c, &prio, locales.SettingsIconPackPriorityPack(), locales.SettingsIconPackPriorityOSC()).Label(locales.SettingsIconPackPriority()).Changed() {
				next := "pack"
				if prio == 1 {
					next = "osc"
				}
				a.setPref(func(s *settings) { s.IconPackPriority = next })
				reloadIconPacks(a.luaConfig)
			}
		})
		if len(installed) == 0 {
			groupRow(c, k, "installed-empty").Children(func() {
				rowHint(c, k, locales.SettingsIconPacksEmpty())
			})
			return
		}
		for _, s := range installed {
			groupRow(c, k, "installed-"+s.ID).Children(func() {
				title := cmp.Or(s.Name, s.ID)
				rowLine(c, k, title, func() {
					enabled := slices.Contains(prefs.IconPacks, s.ID)
					if ui.Switch(c, &enabled).Label(title).Changed() {
						a.setPref(func(st *settings) {
							if enabled {
								if !slices.Contains(st.IconPacks, s.ID) {
									st.IconPacks = append(st.IconPacks, s.ID)
								}
							} else {
								st.IconPacks = slices.DeleteFunc(st.IconPacks, func(x string) bool { return x == s.ID })
							}
						})
						reloadIconPacks(a.luaConfig)
					}
					if ui.Button(c, locales.SettingsIconPacksRemove()).Clicked() {
						if err := iconpack.Remove(iconpack.Dir(), s.ID); err != nil {
							iconPacksUI.mu.Lock()
							iconPacksUI.actionErr = err.Error()
							iconPacksUI.mu.Unlock()
						} else {
							a.setPref(func(st *settings) {
								st.IconPacks = slices.DeleteFunc(st.IconPacks, func(x string) bool { return x == s.ID })
							})
							a.refreshInstalledIconPacks()
						}
					}
				})
				sub := s.ID
				if s.Version != "" {
					sub += " · " + s.Version
				}
				if s.Error != "" {
					sub += " · " + s.Error
				} else {
					sub += " · " + locales.SettingsIconPacksCount(s.Entries)
				}
				rowHint(c, k, sub)
			})
		}
	})

	installedSet := map[string]bool{}
	for _, s := range installed {
		installedSet[s.ID] = true
	}
	for _, s := range themes {
		installedSet[registryKey(iconpack.RegistryPack{ID: s.ID, Kind: iconpack.KindTheme})] = true
	}

	settingsGroup(c, k, locales.SettingsIconPacksRegistry(), "", regErr, func() {
		groupRow(c, k, "registry-toolbar").Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &iconPacksUI.query).Placeholder(locales.SettingsIconPacksSearch()).Label(locales.SettingsIconPacksSearch()).Grow(1).MinWidth(0)
				if ui.Button(c, locales.SettingsIconPacksRefresh()).Clicked() {
					a.fetchIconPackRegistry()
				}
			})
		})
		for _, rp := range registryEntries(reg, iconPacksUI.query) {
			key := registryKey(rp)
			groupRow(c, k, "reg-"+key).Children(func() {
				title := cmp.Or(rp.Name, rp.ID)
				rowLine(c, k, title, func() {
					switch {
					case installedSet[key]:
						ui.Text(c, locales.SettingsIconPacksInstalledBadge()).FontSize(fontFootnote).FontWeight(600).TextColor(k.accent)
					case installing[key]:
						ui.Text(c, locales.SettingsIconPacksInstalling()).FontSize(fontFootnote).TextColor(k.textMuted)
					default:
						if ui.Button(c, locales.SettingsIconPacksInstall()).Label(locales.SettingsIconPacksInstall() + " " + title).Clicked() {
							a.installIconPack(rp)
						}
					}
				})
				meta := extensionKind(rp.Kind) + " · " + rp.ID
				if rp.Author != "" {
					meta += " · " + rp.Author
				}
				if rp.Version != "" {
					meta += " · " + rp.Version
				}
				if rp.Description != "" {
					meta += " — " + rp.Description
				}
				rowHint(c, k, meta)
			})
		}
	})
}

// registryEntries are the registry's entries that match query, of every
// kind: the icon packs, then the themes, each with its Kind set. An index
// with no themes, as the registry's before them, lists its icon packs.
func registryEntries(reg *iconpack.RegistryIndex, query string) []iconpack.RegistryPack {
	out := iconpack.SearchRegistry(reg, query)
	if reg == nil {
		return out
	}
	for _, rp := range iconpack.SearchRegistry(&iconpack.RegistryIndex{Packs: reg.Themes}, query) {
		rp.Kind = iconpack.KindTheme
		out = append(out, rp)
	}
	return out
}

// extensionKind names an extension's kind as the page shows it.
func extensionKind(kind string) string {
	if kind == iconpack.KindTheme {
		return locales.SettingsExtensionKindTheme()
	}
	return locales.SettingsExtensionKindIcons()
}

// themesGroup is the Window Theme group: the built-in palette and each
// installed theme, the one in use marked and the others with Use, and
// Remove for an installed theme.
func (a *App) themesGroup(c *ui.Context, k *colors, themes []theme.Summary) {
	iconPacksUI.mu.Lock()
	themeErr := iconPacksUI.themeErr
	iconPacksUI.mu.Unlock()
	choice := func(id, title string) {
		if prefs.Theme == id {
			ui.Text(c, locales.SettingsThemeInUse()).FontSize(fontFootnote).FontWeight(600).TextColor(k.accent)
			return
		}
		if ui.Button(c, locales.SettingsThemeUse()).Label(locales.SettingsThemeUse() + " " + title).Clicked() {
			a.useTheme(id)
		}
	}
	settingsGroup(c, k, locales.SettingsWindowTheme(), "", themeErr, func() {
		groupRow(c, k, "theme-builtin").Children(func() {
			rowLine(c, k, locales.SettingsThemeBuiltIn(), func() { choice("", locales.SettingsThemeBuiltIn()) })
			rowHint(c, k, locales.SettingsThemeBuiltInHint())
		})
		for _, s := range themes {
			groupRow(c, k, "theme-"+s.ID).Children(func() {
				title := cmp.Or(s.Name, s.ID)
				rowLine(c, k, title, func() {
					if s.Error == "" {
						choice(s.ID, title)
					}
					if ui.Button(c, locales.SettingsIconPacksRemove()).Label(locales.SettingsIconPacksRemove() + " " + title).Clicked() {
						a.removeTheme(s.ID)
					}
				})
				hint := extensionKind(iconpack.KindTheme) + " · " + s.ID
				if s.Error != "" {
					hint += " · " + s.Error
				}
				rowHint(c, k, hint)
			})
			if prefs.Theme == s.ID && look.Load() != nil {
				a.ambienceControls(c, k)
			}
		}
	})
}

// ambienceValues and mascotValues are the values of the Ambience and the
// Mascot controls, in order.
var (
	ambienceValues = []string{"", ambienceStill, ambienceOff}
	mascotValues   = []string{"", mascotOff}
)

// ambienceControls are the Ambience and the Mascot controls under the
// theme in use, when it draws more than colors
// (docs/DSK-010-theme-ambience.md).
func (a *App) ambienceControls(c *ui.Context, k *colors) {
	amb := max(slices.Index(ambienceValues, prefs.Ambience), 0)
	settingRow(c, k, locales.SettingsThemeAmbience(), locales.SettingsThemeAmbienceHint(), func() {
		if ui.Segmented(c, &amb, locales.SettingsAmbienceMoving(), locales.SettingsAmbienceStill(), locales.SettingsAmbienceOff()).Label(locales.SettingsThemeAmbience()).Changed() {
			a.setWindowPref(func(s *settings) { s.Ambience = ambienceValues[amb] })
		}
	})
	mascot := max(slices.Index(mascotValues, prefs.Mascot), 0)
	settingRow(c, k, locales.SettingsThemeMascot(), locales.SettingsThemeMascotHint(), func() {
		if ui.Segmented(c, &mascot, locales.SettingsMascotOn(), locales.SettingsMascotOff()).Label(locales.SettingsThemeMascot()).Changed() {
			a.setWindowPref(func(s *settings) { s.Mascot = mascotValues[mascot] })
		}
	})
}

// useTheme makes id the theme of every window, "" for the built-in
// palette: it saves appearance.theme and applies the theme at once, the
// terminals' colors it names too. applyTheme stores new palettes and drops
// the caches made from the old ones, so it is safe while other windows
// draw; each window then draws again.
func (a *App) useTheme(id string) {
	prefs.Theme = id
	saveSettings()
	err := loadTheme()
	a.applyPrefs()
	iconPacksUI.mu.Lock()
	iconPacksUI.themeErr = ""
	if err != nil {
		iconPacksUI.themeErr = err.Error()
	}
	iconPacksUI.mu.Unlock()
	for _, o := range a.peers() {
		if o.win != nil {
			o.win.Update(func() {})
		}
	}
}

// removeTheme deletes an installed theme, and goes back to the built-in
// palette when it was the one in use.
func (a *App) removeTheme(id string) {
	if err := theme.Remove(theme.Dir(config.Dir()), id); err != nil {
		iconPacksUI.mu.Lock()
		iconPacksUI.themeErr = err.Error()
		iconPacksUI.mu.Unlock()
		return
	}
	if prefs.Theme == id {
		a.useTheme("")
	}
	a.refreshInstalledIconPacks()
}
